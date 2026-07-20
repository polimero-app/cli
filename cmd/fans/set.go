package fans

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/cmdrun"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/profile"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/spf13/cobra"
)

const commandSet = "fans set"

var fanTokenRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func setCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		yes           bool
		timeout       string
		insecure      bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "set <printer> <fan> <percent>",
		Short: "Set fan speed percentage on a printer",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 3 {
				msg := "profile name, fan name, and speed percentage are required"
				if len(args) == 1 {
					msg = "fan name and speed percentage are required"
				} else if len(args) == 2 {
					msg = "speed percentage is required"
				}
				return cmderr.WriteUsage(cmd, commandSet, msg)
			}
			if len(args) > 3 {
				return cmderr.WriteUsage(cmd, commandSet, fmt.Sprintf("expected exactly three arguments, got %d", len(args)))
			}
			return runSet(cmd, args[0], args[1], args[2], flags.yes, flags.timeout, flags.insecure, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "skip interactive confirmation")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func runSet(cmd *cobra.Command, nameArg, fanArg, percentArg string, yes bool, timeoutFlag string, insecureFlag bool, protocolTrace string, deps Deps) (retErr error) {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	// 1. Validate fan name syntax
	if !fanTokenRE.MatchString(fanArg) {
		return cmderr.WriteDetail(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 2,
			"invalid_argument", "invalid fan name syntax", nil)
	}

	// 2. Validate speed percentage syntax
	percent, parseErr := strconv.Atoi(percentArg)
	if parseErr != nil {
		return cmderr.WriteDetail(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 2,
			"invalid_argument", "invalid speed percent: must be an integer", nil)
	}

	// 3. Validate speed percentage range
	if percent < 0 || percent > 100 {
		details := map[string]any{
			"fan":     fanArg,
			"value":   percent,
			"minimum": 0,
			"maximum": 100,
		}
		return cmderr.WriteDetail(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 2,
			"unsafe_value", "fan speed out of range", details)
	}

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return cmdrun.WriteError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, traceErr)
	}
	defer protocoltrace.Finish(traceCleanup, cmd.ErrOrStderr(), &retErr)

	rp, err := profile.Resolve(traceCtx, nameArg, timeoutFlag, insecureFlag, deps.KC, deps.GetDriver)
	if err != nil {
		return cmdrun.WriteError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, err)
	}
	fanDrv, ok := rp.Driver.(driver.FanDriver)
	if !ok || !rp.Driver.Capabilities().FanControl {
		return cmdrun.WriteError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet,
			apperr.Newf(5, "driver %q does not support fan control", rp.Input.Driver))
	}

	precheckCtx, cancelPrecheck := context.WithTimeout(traceCtx, rp.Timeout)
	if _, err := cmdrun.CheckStatePrecondition(precheckCtx, cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet,
		[]string{"idle", "printing", "paused"}, rp, deps.Log); err != nil {
		cancelPrecheck()
		return err
	}
	cancelPrecheck()

	canonicalFan := normalizeFanKey(fanArg)
	prompt := fmt.Sprintf("Set %s fan to %d%% on %s? Type 'yes' to continue: ", fanArg, percent, rp.Name)
	if err := cmdrun.CheckConfirmation(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, yes, prompt, deps.Prompter); err != nil {
		return err
	}

	actionCtx, cancelAction := context.WithTimeout(traceCtx, rp.Timeout)
	defer cancelAction()

	target := driver.FanTarget{
		Fan:          canonicalFan,
		SpeedPercent: percent,
	}

	start := time.Now()
	result, err := fanDrv.FanSet(actionCtx, rp.Input, rp.Secrets, deps.Log, target)
	if err != nil {
		return cmdrun.WriteError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, err)
	}
	durationMs := time.Since(start).Milliseconds()

	// Verify driver-confirmed state matches target
	if result.SpeedPercent != percent || result.Fan != canonicalFan {
		return cmderr.WriteDetail(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 1,
			"fan_action_failed", "fan operation did not result in expected state", map[string]any{
				"profile":              rp.Name,
				"expectedFan":          canonicalFan,
				"expectedSpeedPercent": percent,
				"observedFan":          result.Fan,
				"observedSpeedPercent": result.SpeedPercent,
			})
	}

	var tracePath *string
	if protocolTrace != "" {
		tracePath = &protocolTrace
	}

	return writeSuccess(cmd.OutOrStdout(), format, rp.Name, rp.Input.Driver, result, durationMs, tracePath)
}

func normalizeFanKey(fan string) string {
	switch strings.ToLower(fan) {
	case "part-cooling", "part_cooling", "partcooling":
		return "partCooling"
	case "aux", "auxiliary":
		return "auxiliary"
	case "chamber":
		return "chamber"
	default:
		return fan
	}
}

func fanDisplayName(key string) string {
	switch key {
	case "partCooling":
		return "Part cooling"
	case "auxiliary":
		return "Auxiliary"
	case "chamber":
		return "Chamber"
	default:
		if len(key) > 0 {
			return strings.ToUpper(key[:1]) + key[1:]
		}
		return key
	}
}

func writeSuccess(w io.Writer, format output.Format, profileName, driverName string, result driver.FanControlResult, durationMs int64, tracePath *string) error {
	if format == output.FormatJSON {
		return writeJSONSuccess(w, profileName, driverName, result, durationMs, tracePath)
	}
	return writeHumanSuccess(w, profileName, result)
}

func writeJSONSuccess(w io.Writer, profileName, driverName string, result driver.FanControlResult, durationMs int64, tracePath *string) error {
	dm := durationMs
	type data struct {
		Profile      string                 `json:"profile"`
		Driver       string                 `json:"driver"`
		Fan          string                 `json:"fan"`
		SpeedPercent int                    `json:"speedPercent"`
		Warnings     []driver.StatusWarning `json:"warnings"`
		Capabilities driver.Capabilities    `json:"capabilities"`
	}
	warnings := result.Warnings
	if warnings == nil {
		warnings = []driver.StatusWarning{}
	}
	d := data{
		Profile:      profileName,
		Driver:       driverName,
		Fan:          result.Fan,
		SpeedPercent: result.SpeedPercent,
		Warnings:     warnings,
		Capabilities: result.Capabilities,
	}
	return output.WriteEnvelope(w, output.Envelope{
		OK:    true,
		Data:  d,
		Error: nil,
		Meta:  output.Meta{Command: commandSet, DurationMs: &dm, ProtocolTracePath: tracePath},
	})
}

func writeHumanSuccess(w io.Writer, profileName string, result driver.FanControlResult) error {
	if _, err := fmt.Fprintf(w, "Printer: %s\n", profileName); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "%s fan set to %d%%.\n", fanDisplayName(result.Fan), result.SpeedPercent)
	return err
}
