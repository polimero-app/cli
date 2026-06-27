package motion

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/spf13/cobra"
)

const commandJog = "motion jog"

const (
	jogMin = -10.0
	jogMax = 10.0
)

func jogCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		x, y, z  float64
		feedrate  int
		yes       bool
		timeout   string
		insecure  bool
	}

	cmd := &cobra.Command{
		Use:   "jog <printer>",
		Short: "Jog printer axes by a relative distance",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if len(args) > 1 {
				return writeUsageError(cmd, commandJog, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			hasX := cmd.Flags().Changed("x")
			hasY := cmd.Flags().Changed("y")
			hasZ := cmd.Flags().Changed("z")
			return runJog(cmd, args[0], flags.x, flags.y, flags.z, hasX, hasY, hasZ,
				flags.feedrate, flags.yes, flags.timeout, flags.insecure, deps)
		},
	}
	cmd.Flags().Float64Var(&flags.x, "x", 0, "relative X-axis move in mm (range: -10 to 10)")
	cmd.Flags().Float64Var(&flags.y, "y", 0, "relative Y-axis move in mm (range: -10 to 10)")
	cmd.Flags().Float64Var(&flags.z, "z", 0, "relative Z-axis move in mm (range: -10 to 10)")
	cmd.Flags().IntVar(&flags.feedrate, "feedrate", 1500, "move speed in mm/min")
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "skip interactive confirmation")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	return cmd
}

func runJog(cmd *cobra.Command, nameArg string,
	x, y, z float64, hasX, hasY, hasZ bool,
	feedrate int, yes bool, timeoutFlag string, insecureFlag bool,
	deps Deps,
) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	// Validate at least one axis given.
	if !hasX && !hasY && !hasZ {
		return writeDetailError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandJog, 2,
			"unsafe_value", "at least one of --x, --y, --z is required", nil)
	}

	// Validate bounds.
	if hasX {
		if err := checkJogBound(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "x", x); err != nil {
			return err
		}
	}
	if hasY {
		if err := checkJogBound(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "y", y); err != nil {
			return err
		}
	}
	if hasZ {
		if err := checkJogBound(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "z", z); err != nil {
			return err
		}
	}

	rp, err := resolveProfile(cmd.Context(), nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandJog, err)
	}

	if !rp.driver.Capabilities().MotionControl {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandJog,
			apperr.Newf(5, "driver %q does not support motion control", rp.pi.Driver))
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), rp.timeout)
	defer cancel()

	if _, err := checkIdlePrecondition(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandJog, rp.name, "move", rp, deps, ctx); err != nil {
		return err
	}

	if !yes {
		if !deps.Prompter.IsTerminal() {
			return writeDetailError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandJog, 2,
				"config_error", "non-interactive mode requires --yes", nil)
		}
		prompt := fmt.Sprintf("Jog %s on %s? Type 'yes' to continue: ", jogSummary(x, y, z, hasX, hasY, hasZ, feedrate), rp.name)
		answer, readErr := deps.Prompter.ReadLine(prompt)
		if readErr != nil {
			return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandJog,
				apperr.Newf(1, "cannot read confirmation: %s", readErr))
		}
		if answer != "yes" {
			return writeDetailError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandJog, 2,
				"config_error", "confirmation declined", nil)
		}
	}

	delta := driver.JogDelta{FeedrateMmPerMin: feedrate}
	if hasX {
		v := x
		delta.XMillimeters = &v
	}
	if hasY {
		v := y
		delta.YMillimeters = &v
	}
	if hasZ {
		v := z
		delta.ZMillimeters = &v
	}

	start := time.Now()
	result, err := rp.motionDrv.MotionJog(ctx, rp.pi, rp.secrets, deps.Log, delta)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandJog, err)
	}
	durationMs := time.Since(start).Milliseconds()

	return writeJogSuccess(cmd.OutOrStdout(), format, rp.name, rp.pi.Driver, delta, result, durationMs)
}

func checkJogBound(out, errOut io.Writer, format output.Format, axis string, value float64) error {
	if value < jogMin || value > jogMax {
		return writeDetailError(out, errOut, format, commandJog, 2, "unsafe_value",
			"jog distance out of range",
			map[string]any{
				"axis":    axis,
				"value":   value,
				"minimum": jogMin,
				"maximum": jogMax,
			})
	}
	return nil
}

func jogSummary(x, y, z float64, hasX, hasY, hasZ bool, feedrate int) string {
	var parts []string
	if hasX {
		parts = append(parts, fmt.Sprintf("x%+.1fmm", x))
	}
	if hasY {
		parts = append(parts, fmt.Sprintf("y%+.1fmm", y))
	}
	if hasZ {
		parts = append(parts, fmt.Sprintf("z%+.1fmm", z))
	}
	return fmt.Sprintf("%s at %dmm/min", strings.Join(parts, " "), feedrate)
}

func writeJogSuccess(w io.Writer, format output.Format, name, driverName string, delta driver.JogDelta, result driver.MotionResult, durationMs int64) error {
	if format == output.FormatJSON {
		return writeJogJSONSuccess(w, name, driverName, delta, result, durationMs)
	}
	return writeJogHumanSuccess(w, name, delta)
}

func writeJogJSONSuccess(w io.Writer, name, driverName string, delta driver.JogDelta, result driver.MotionResult, durationMs int64) error {
	dm := durationMs
	type deltaJSON struct {
		XMillimeters     *float64 `json:"xMillimeters"`
		YMillimeters     *float64 `json:"yMillimeters"`
		ZMillimeters     *float64 `json:"zMillimeters"`
		FeedrateMmPerMin int      `json:"feedrateMmPerMin"`
	}
	type data struct {
		Profile      string                 `json:"profile"`
		Driver       string                 `json:"driver"`
		Action       string                 `json:"action"`
		Delta        deltaJSON              `json:"delta"`
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
			Profile: name,
			Driver:  driverName,
			Action:  "jog",
			Delta: deltaJSON{
				XMillimeters:     delta.XMillimeters,
				YMillimeters:     delta.YMillimeters,
				ZMillimeters:     delta.ZMillimeters,
				FeedrateMmPerMin: delta.FeedrateMmPerMin,
			},
			Warnings:     warnings,
			Capabilities: result.Capabilities,
		},
		Error: nil,
		Meta:  output.Meta{Command: commandJog, DurationMs: &dm},
	})
}

func writeJogHumanSuccess(w io.Writer, name string, delta driver.JogDelta) error {
	var parts []string
	if delta.XMillimeters != nil {
		parts = append(parts, fmt.Sprintf("x%+.1fmm", *delta.XMillimeters))
	}
	if delta.YMillimeters != nil {
		parts = append(parts, fmt.Sprintf("y%+.1fmm", *delta.YMillimeters))
	}
	if delta.ZMillimeters != nil {
		parts = append(parts, fmt.Sprintf("z%+.1fmm", *delta.ZMillimeters))
	}
	lines := []string{
		fmt.Sprintf("Printer: %s", name),
		fmt.Sprintf("Jogging %s at %dmm/min...", strings.Join(parts, " "), delta.FeedrateMmPerMin),
		"Jog complete.",
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}
