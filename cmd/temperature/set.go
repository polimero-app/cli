package temperature

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/spf13/cobra"
)

const commandSet = "temperature set"

// temperature safety bounds (command-layer enforcement, independent of firmware).
const (
	nozzleMin  = 0.0
	nozzleMax  = 300.0
	bedMin     = 0.0
	bedMax     = 120.0
	chamberMin = 0.0
	chamberMax = 65.0
)

func setCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		nozzle        float64
		bed           float64
		chamber       float64
		hasNozzle     bool
		hasBed        bool
		hasChamber    bool
		yes           bool
		timeout       string
		insecure      bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "set <printer>",
		Short: "Set heater target temperatures on a printer",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return writeUsageError(cmd, "profile name is required")
			}
			if len(args) > 1 {
				return writeUsageError(cmd, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			flags.hasNozzle = cmd.Flags().Changed("nozzle")
			flags.hasBed = cmd.Flags().Changed("bed")
			flags.hasChamber = cmd.Flags().Changed("chamber")
			return runSet(cmd, args[0], flags.nozzle, flags.bed, flags.chamber,
				flags.hasNozzle, flags.hasBed, flags.hasChamber,
				flags.yes, flags.timeout, flags.insecure, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().Float64Var(&flags.nozzle, "nozzle", 0, "nozzle target temperature in Celsius (0 turns off)")
	cmd.Flags().Float64Var(&flags.bed, "bed", 0, "bed target temperature in Celsius (0 turns off)")
	cmd.Flags().Float64Var(&flags.chamber, "chamber", 0, "chamber target temperature in Celsius (0 turns off)")
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "skip interactive confirmation")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func writeUsageError(cmd *cobra.Command, message string) error {
	return cmderr.WriteUsage(cmd, commandSet, message)
}

func runSet(cmd *cobra.Command, nameArg string,
	nozzle, bed, chamber float64,
	hasNozzle, hasBed, hasChamber bool,
	yes bool, timeoutFlag string, insecureFlag bool, protocolTrace string,
	deps Deps,
) (retErr error) {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, traceErr)
	}
	defer protocoltrace.Finish(traceCleanup, cmd.ErrOrStderr(), &retErr)

	// Step 2: validate that at least one target was given and bounds are safe.
	if !hasNozzle && !hasBed && !hasChamber {
		return writeDetailError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 2,
			"unsafe_value", "at least one of --nozzle, --bed, --chamber is required", nil)
	}
	if hasNozzle {
		if err := checkBound(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "nozzle", nozzle, nozzleMin, nozzleMax); err != nil {
			return err
		}
	}
	if hasBed {
		if err := checkBound(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "bed", bed, bedMin, bedMax); err != nil {
			return err
		}
	}
	if hasChamber {
		if err := checkBound(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "chamber", chamber, chamberMin, chamberMax); err != nil {
			return err
		}
	}

	// Step 1: resolve profile and secrets.
	rp, err := resolveProfile(traceCtx, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, err)
	}

	// Check capability.
	if !rp.driver.Capabilities().TemperatureWrite {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet,
			apperr.Newf(5, "driver %q does not support temperature control", rp.pi.Driver))
	}

	ctx, cancel := context.WithTimeout(traceCtx, rp.timeout)
	defer cancel()

	// Step 3: query current status for precondition check.
	status, err := rp.driver.Status(ctx, rp.pi, rp.secrets, deps.Log)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, err)
	}
	if status == nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet,
			apperr.New(1, "driver returned nil status result"))
	}

	// Step 4: check state precondition.
	if status.State != "idle" {
		return writeDetailError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 2,
			"invalid_printer_state",
			fmt.Sprintf("cannot set temperature while %s", status.State),
			map[string]any{
				"profile":       rp.name,
				"currentState":  status.State,
				"requiredState": "idle",
			})
	}

	// Step 5: prompt for confirmation.
	if !yes {
		if !deps.Prompter.IsTerminal() {
			return writeDetailError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 2,
				"config_error", "non-interactive mode requires --yes", nil)
		}
		prompt := fmt.Sprintf("Set %s on %s? Type 'yes' to continue: ", targetSummary(nozzle, bed, chamber, hasNozzle, hasBed, hasChamber), rp.name)
		answer, readErr := deps.Prompter.ReadLine(prompt)
		if readErr != nil {
			return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet,
				apperr.Newf(1, "cannot read confirmation: %s", readErr))
		}
		if answer != "yes" {
			return writeDetailError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, 2,
				"config_error", "confirmation declined", nil)
		}
	}

	// Build targets.
	targets := driver.TemperatureTargets{}
	if hasNozzle {
		v := nozzle
		targets.NozzleCelsius = &v
	}
	if hasBed {
		v := bed
		targets.BedCelsius = &v
	}
	if hasChamber {
		v := chamber
		targets.ChamberCelsius = &v
	}

	// Step 6: dispatch to driver.
	start := time.Now()
	result, err := rp.tempDrv.TemperatureSet(ctx, rp.pi, rp.secrets, deps.Log, targets)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSet, err)
	}
	durationMs := time.Since(start).Milliseconds()

	return writeSetSuccess(cmd.OutOrStdout(), format, rp.name, rp.pi.Driver, result, durationMs, protocolTrace)
}

func checkBound(out, errOut io.Writer, format output.Format, target string, value, min, max float64) error {
	// NaN compares false against any bound, so reject non-finite values first.
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return writeDetailError(out, errOut, format, commandSet, 2, "unsafe_value",
			fmt.Sprintf("%s target must be a finite number", target),
			map[string]any{
				"target":  target,
				"value":   fmt.Sprintf("%g", value),
				"minimum": min,
				"maximum": max,
			})
	}
	if value < min || value > max {
		return writeDetailError(out, errOut, format, commandSet, 2, "unsafe_value",
			fmt.Sprintf("%s target out of range", target),
			map[string]any{
				"target":  target,
				"value":   value,
				"minimum": min,
				"maximum": max,
			})
	}
	return nil
}

func targetSummary(nozzle, bed, chamber float64, hasNozzle, hasBed, hasChamber bool) string {
	var parts []string
	if hasNozzle {
		parts = append(parts, fmt.Sprintf("nozzle=%.1fC", nozzle))
	}
	if hasBed {
		parts = append(parts, fmt.Sprintf("bed=%.1fC", bed))
	}
	if hasChamber {
		parts = append(parts, fmt.Sprintf("chamber=%.1fC", chamber))
	}
	return strings.Join(parts, ", ")
}

func writeSetSuccess(w io.Writer, format output.Format, name, driverName string, result driver.TemperatureResult, durationMs int64, protocolTrace string) error {
	if format == output.FormatJSON {
		var tracePath *string
		if protocolTrace != "" {
			tracePath = &protocolTrace
		}
		return writeSetJSONSuccess(w, name, driverName, result, durationMs, tracePath)
	}
	return writeSetHumanSuccess(w, name, result)
}

func writeSetJSONSuccess(w io.Writer, name, driverName string, result driver.TemperatureResult, durationMs int64, tracePath *string) error {
	dm := durationMs
	type targetsJSON struct {
		NozzleCelsius  *float64 `json:"nozzleCelsius"`
		BedCelsius     *float64 `json:"bedCelsius"`
		ChamberCelsius *float64 `json:"chamberCelsius"`
	}
	type data struct {
		Profile      string                 `json:"profile"`
		Driver       string                 `json:"driver"`
		Targets      targetsJSON            `json:"targets"`
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
			Targets: targetsJSON{
				NozzleCelsius:  result.Targets.NozzleCelsius,
				BedCelsius:     result.Targets.BedCelsius,
				ChamberCelsius: result.Targets.ChamberCelsius,
			},
			Warnings:     warnings,
			Capabilities: result.Capabilities,
		},
		Error: nil,
		Meta:  output.Meta{Command: commandSet, DurationMs: &dm, ProtocolTracePath: tracePath},
	})
}

func writeSetHumanSuccess(w io.Writer, name string, result driver.TemperatureResult) error {
	lines := []string{fmt.Sprintf("Printer: %s", name)}
	if result.Targets.NozzleCelsius != nil {
		lines = append(lines, fmt.Sprintf("Nozzle target set to %.1f C", *result.Targets.NozzleCelsius))
	}
	if result.Targets.BedCelsius != nil {
		lines = append(lines, fmt.Sprintf("Bed target set to %.1f C", *result.Targets.BedCelsius))
	}
	if result.Targets.ChamberCelsius != nil {
		lines = append(lines, fmt.Sprintf("Chamber target set to %.1f C", *result.Targets.ChamberCelsius))
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}
