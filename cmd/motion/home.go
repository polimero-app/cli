package motion

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/spf13/cobra"
)

const commandHome = "motion home"

var validAxes = map[string]driver.Axis{
	"x": driver.AxisX,
	"y": driver.AxisY,
	"z": driver.AxisZ,
}

func homeCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		axis          string
		yes           bool
		timeout       string
		insecure      bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "home <printer>",
		Short: "Home printer axes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if len(args) > 1 {
				return writeUsageError(cmd, commandHome, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runHome(cmd, args[0], flags.axis, flags.yes, flags.timeout, flags.insecure, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().StringVar(&flags.axis, "axis", "", "comma-separated axes to home: x,y,z (default: all)")
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "skip interactive confirmation")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func writeUsageError(cmd *cobra.Command, cmdName, message string) error {
	return cmderr.WriteUsage(cmd, cmdName, message)
}

func runHome(cmd *cobra.Command, nameArg, axisFlag string, yes bool, timeoutFlag string, insecureFlag bool, protocolTrace string, deps Deps) (retErr error) {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandHome, traceErr)
	}
	defer protocoltrace.Finish(traceCleanup, cmd.ErrOrStderr(), &retErr)

	// Parse and validate axis list.
	axes, axisNames, err := parseAxes(axisFlag)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandHome, err)
	}

	rp, err := resolveProfile(traceCtx, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandHome, err)
	}

	if !rp.driver.Capabilities().MotionControl {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandHome,
			apperr.Newf(5, "driver %q does not support motion control", rp.pi.Driver))
	}

	ctx, cancel := context.WithTimeout(traceCtx, rp.timeout)
	defer cancel()

	if _, err := checkIdlePrecondition(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandHome, rp.name, "home", rp, deps, ctx); err != nil {
		return err
	}

	if !yes {
		if !deps.Prompter.IsTerminal() {
			return writeDetailError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandHome, 2,
				"config_error", "non-interactive mode requires --yes", nil)
		}
		prompt := fmt.Sprintf("Home %s on %s? Type 'yes' to continue: ", strings.Join(axisNames, ", "), rp.name)
		answer, readErr := deps.Prompter.ReadLine(prompt)
		if readErr != nil {
			return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandHome,
				apperr.Newf(1, "cannot read confirmation: %s", readErr))
		}
		if answer != "yes" {
			return writeDetailError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandHome, 2,
				"config_error", "confirmation declined", nil)
		}
	}

	start := time.Now()
	result, err := rp.motionDrv.MotionHome(ctx, rp.pi, rp.secrets, deps.Log, axes)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandHome, err)
	}
	durationMs := time.Since(start).Milliseconds()

	var tracePath *string
	if protocolTrace != "" {
		tracePath = &protocolTrace
	}
	return writeHomeSuccess(cmd.OutOrStdout(), format, rp.name, rp.pi.Driver, axisNames, result, durationMs, tracePath)
}

func parseAxes(axisFlag string) ([]driver.Axis, []string, error) {
	if axisFlag == "" {
		return []driver.Axis{driver.AxisX, driver.AxisY, driver.AxisZ}, []string{"x", "y", "z"}, nil
	}
	parts := strings.Split(axisFlag, ",")
	axes := make([]driver.Axis, 0, len(parts))
	names := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p == "" {
			continue
		}
		if seen[p] {
			continue
		}
		a, ok := validAxes[p]
		if !ok {
			return nil, nil, apperr.Newf(2, "invalid axis %q: must be one of x, y, z", p)
		}
		seen[p] = true
		axes = append(axes, a)
		names = append(names, p)
	}
	if len(axes) == 0 {
		return nil, nil, apperr.New(2, "at least one axis is required")
	}
	return axes, names, nil
}

func writeHomeSuccess(w io.Writer, format output.Format, name, driverName string, axisNames []string, result driver.MotionResult, durationMs int64, tracePath *string) error {
	if format == output.FormatJSON {
		return writeHomeJSONSuccess(w, name, driverName, axisNames, result, durationMs, tracePath)
	}
	return writeHomeHumanSuccess(w, name, axisNames)
}

func writeHomeJSONSuccess(w io.Writer, name, driverName string, axisNames []string, result driver.MotionResult, durationMs int64, tracePath *string) error {
	dm := durationMs
	axes := make([]any, len(axisNames))
	for i, a := range axisNames {
		axes[i] = a
	}
	type data struct {
		Profile      string                 `json:"profile"`
		Driver       string                 `json:"driver"`
		Action       string                 `json:"action"`
		State        string                 `json:"state"`
		Axes         []any                  `json:"axes"`
		Warnings     []driver.StatusWarning `json:"warnings"`
		Capabilities driver.Capabilities    `json:"capabilities"`
	}
	warnings := result.Warnings
	if warnings == nil {
		warnings = []driver.StatusWarning{}
	}
	return output.WriteEnvelope(w, output.Envelope{
		OK: true,
		Data: data{
			Profile:      name,
			Driver:       driverName,
			Action:       "home",
			State:        motionResultState(result),
			Axes:         axes,
			Warnings:     warnings,
			Capabilities: result.Capabilities,
		},
		Error: nil,
		Meta:  output.Meta{Command: commandHome, DurationMs: &dm, ProtocolTracePath: tracePath},
	})
}

func writeHomeHumanSuccess(w io.Writer, name string, axisNames []string) error {
	lines := []string{
		fmt.Sprintf("Printer: %s", name),
		fmt.Sprintf("Homing %s...", strings.Join(axisNames, ", ")),
		"Homing command accepted.",
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}

func motionResultState(result driver.MotionResult) string {
	if result.State == "" {
		return string(driver.MotionStateAccepted)
	}
	return string(result.State)
}
