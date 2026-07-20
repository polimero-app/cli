package speed

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/spf13/cobra"
)

const commandSet = "speed set"

var speedTokenRE = regexp.MustCompile(`^[a-z0-9_.-]+$`)

func setCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		yes           bool
		timeout       string
		insecure      bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "set <printer> <profile>",
		Short: "Set active print speed profile on a printer",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				msg := "profile name and speed profile are required"
				if len(args) == 1 {
					msg = "speed profile is required"
				}
				return cmderr.WriteUsage(cmd, commandSet, msg)
			}
			if len(args) > 2 {
				return cmderr.WriteUsage(cmd, commandSet, fmt.Sprintf("expected exactly two arguments, got %d", len(args)))
			}
			return runSet(cmd, args[0], args[1], flags.yes, flags.timeout, flags.insecure, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "skip interactive confirmation")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func runSet(cmd *cobra.Command, nameArg, profileArg string, yes bool, timeoutFlag string, insecureFlag bool, protocolTrace string, deps Deps) (retErr error) {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	if !speedTokenRE.MatchString(profileArg) {
		return writeDetailError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 2,
			"invalid_argument", "invalid speed profile syntax", nil)
	}

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, traceErr)
	}
	defer protocoltrace.Finish(traceCleanup, cmd.ErrOrStderr(), &retErr)

	rp, err := resolveProfile(traceCtx, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, err)
	}

	if !rp.driver.Capabilities().SpeedControl {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet,
			apperr.Newf(5, "driver %q does not support speed control", rp.pi.Driver))
	}

	precheckCtx, cancelPrecheck := context.WithTimeout(traceCtx, rp.timeout)
	if _, err := checkStatePrecondition(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet,
		rp.name, []string{"printing", "paused"}, rp, deps, precheckCtx); err != nil {
		cancelPrecheck()
		return err
	}
	cancelPrecheck()

	prompt := fmt.Sprintf("Set speed profile %s on %s? Type 'yes' to continue: ", profileArg, rp.name)
	if err := checkConfirmation(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, yes, prompt, deps); err != nil {
		return err
	}

	actionCtx, cancelAction := context.WithTimeout(traceCtx, rp.timeout)
	defer cancelAction()

	start := time.Now()
	result, err := rp.speedDrv.SpeedSet(actionCtx, rp.pi, rp.secrets, deps.Log, profileArg)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, err)
	}
	durationMs := time.Since(start).Milliseconds()

	if result.SpeedProfile != profileArg {
		return writeDetailError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 1,
			"speed_action_failed", "speed operation did not result in expected profile", map[string]any{
				"profile":         rp.name,
				"expectedProfile": profileArg,
				"observedProfile": result.SpeedProfile,
			})
	}

	var tracePath *string
	if protocolTrace != "" {
		tracePath = &protocolTrace
	}

	return writeSuccess(cmd.OutOrStdout(), format, rp.name, rp.pi.Driver, result, durationMs, tracePath)
}

func writeSuccess(w io.Writer, format output.Format, profileName, driverName string, result driver.SpeedControlResult, durationMs int64, tracePath *string) error {
	if format == output.FormatJSON {
		return writeJSONSuccess(w, profileName, driverName, result, durationMs, tracePath)
	}
	return writeHumanSuccess(w, profileName, result)
}

func writeJSONSuccess(w io.Writer, profileName, driverName string, result driver.SpeedControlResult, durationMs int64, tracePath *string) error {
	dm := durationMs
	type data struct {
		Profile      string                 `json:"profile"`
		Driver       string                 `json:"driver"`
		SpeedProfile string                 `json:"speedProfile"`
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
		SpeedProfile: result.SpeedProfile,
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

func writeHumanSuccess(w io.Writer, profileName string, result driver.SpeedControlResult) error {
	if _, err := fmt.Fprintf(w, "Printer: %s\n", profileName); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "Speed profile set to %s.\n", result.SpeedProfile)
	return err
}
