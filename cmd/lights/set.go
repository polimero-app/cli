package lights

import (
	"context"
	"fmt"
	"io"
	"regexp"
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

const commandSet = "lights set"

var lightTokenRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func setCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		yes           bool
		timeout       string
		insecure      bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "set <printer> <light> <state>",
		Short: "Set light state (on/off) on a printer",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 3 {
				msg := "profile name, light name, and state are required"
				if len(args) == 1 {
					msg = "light name and state are required"
				} else if len(args) == 2 {
					msg = "state is required"
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

func runSet(cmd *cobra.Command, nameArg, lightArg, stateArg string, yes bool, timeoutFlag string, insecureFlag bool, protocolTrace string, deps Deps) (retErr error) {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	// 1. Validate light name syntax
	if !lightTokenRE.MatchString(lightArg) {
		return cmderr.WriteDetail(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 2,
			"invalid_argument", "invalid light name syntax", nil)
	}

	// 2. Validate state
	state := driver.LightState(stateArg)
	if state != driver.LightStateOn && state != driver.LightStateOff {
		return cmderr.WriteDetail(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 2,
			"invalid_argument", "invalid light state: must be on or off", nil)
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
	lightDrv, ok := rp.Driver.(driver.LightDriver)
	if !ok || !rp.Driver.Capabilities().LightControl {
		return cmdrun.WriteError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet,
			apperr.Newf(5, "driver %q does not support light control", rp.Input.Driver))
	}

	precheckCtx, cancelPrecheck := context.WithTimeout(traceCtx, rp.Timeout)
	// Allowed states: idle, printing, paused, error
	if _, err := cmdrun.CheckStatePrecondition(precheckCtx, cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet,
		[]string{"idle", "printing", "paused", "error"}, rp, deps.Log); err != nil {
		cancelPrecheck()
		return err
	}
	cancelPrecheck()

	canonicalLight := normalizeLightKey(lightArg)
	prompt := fmt.Sprintf("Set %s light %s on %s? Type 'yes' to continue: ", lightArg, stateArg, rp.Name)
	if err := cmdrun.CheckConfirmation(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, yes, prompt, deps.Prompter); err != nil {
		return err
	}

	actionCtx, cancelAction := context.WithTimeout(traceCtx, rp.Timeout)
	defer cancelAction()

	target := driver.LightTarget{
		Light: canonicalLight,
		State: state,
	}

	start := time.Now()
	result, err := lightDrv.LightSet(actionCtx, rp.Input, rp.Secrets, deps.Log, target)
	if err != nil {
		return cmdrun.WriteError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, err)
	}
	durationMs := time.Since(start).Milliseconds()

	// Verify driver-confirmed state matches target
	if result.State != state || result.Light != canonicalLight {
		return cmderr.WriteDetail(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 1,
			"light_action_failed", "light operation did not result in expected state", map[string]any{
				"profile":       rp.Name,
				"expectedLight": canonicalLight,
				"expectedState": state,
				"observedLight": result.Light,
				"observedState": result.State,
			})
	}

	var tracePath *string
	if protocolTrace != "" {
		tracePath = &protocolTrace
	}

	return writeSuccess(cmd.OutOrStdout(), format, rp.Name, rp.Input.Driver, result, durationMs, tracePath)
}

func normalizeLightKey(light string) string {
	switch strings.ToLower(light) {
	case "chamber", "chamber-light", "chamber_light":
		return "chamber"
	default:
		return light
	}
}

func lightDisplayName(key string) string {
	switch key {
	case "chamber":
		return "Chamber"
	default:
		if len(key) > 0 {
			return strings.ToUpper(key[:1]) + key[1:]
		}
		return key
	}
}

func writeSuccess(w io.Writer, format output.Format, profileName, driverName string, result driver.LightControlResult, durationMs int64, tracePath *string) error {
	if format == output.FormatJSON {
		return writeJSONSuccess(w, profileName, driverName, result, durationMs, tracePath)
	}
	return writeHumanSuccess(w, profileName, result)
}

func writeJSONSuccess(w io.Writer, profileName, driverName string, result driver.LightControlResult, durationMs int64, tracePath *string) error {
	dm := durationMs
	type data struct {
		Profile      string                 `json:"profile"`
		Driver       string                 `json:"driver"`
		Light        string                 `json:"light"`
		State        driver.LightState      `json:"state"`
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
		Light:        result.Light,
		State:        result.State,
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

func writeHumanSuccess(w io.Writer, profileName string, result driver.LightControlResult) error {
	if _, err := fmt.Fprintf(w, "Printer: %s\n", profileName); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "%s light set to %s.\n", lightDisplayName(result.Light), result.State)
	return err
}
