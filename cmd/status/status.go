package status

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/devicepath"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/profile"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/spf13/cobra"
)

// Deps holds injectable dependencies for the status command.
type Deps struct {
	KC        keychain.Keychain
	GetDriver func(string) (driver.Driver, bool)
	Log       *slog.Logger
}

// Command returns the top-level "status" cobra command.
func Command() *cobra.Command {
	return CommandWithDeps(Deps{
		KC:        keychain.NewReal(),
		GetDriver: drivers.Get,
		Log:       slog.Default(),
	})
}

// CommandWithDeps constructs the "status" cobra command with injected dependencies.
func CommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		timeout       string
		insecure      bool
		detailed      bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show the current status of a printer",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if len(args) > 1 {
				return writeUsageError(cmd, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runStatus(cmd, args[0], flags.timeout, flags.insecure, flags.detailed, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	cmd.Flags().BoolVar(&flags.detailed, "detailed", false, "include extended telemetry (fans, time, speed, AMS, etc.)")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func writeUsageError(cmd *cobra.Command, message string) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}
	return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, apperr.New(2, message), errorContext{})
}

func runStatus(cmd *cobra.Command, nameArg, timeoutFlag string, insecureFlag, detailed bool, protocolTrace string, deps Deps) (retErr error) {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	ctx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, traceErr, errorContext{})
	}
	// Trace close failure after protocol work: exit 1 unless earlier error.
	defer protocoltrace.Finish(traceCleanup, cmd.ErrOrStderr(), &retErr)

	name := strings.ToLower(nameArg)
	verboseFlag, _ := cmd.Root().PersistentFlags().GetBool("verbose")
	verbose := verboseFlag && format == output.FormatHuman
	result, durationMs, driverName, errCtx, err := doStatus(cmd, ctx, name, timeoutFlag, insecureFlag, verbose, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, err, errCtx)
	}

	var tracePath *string
	if protocolTrace != "" {
		tracePath = &protocolTrace
	}
	return writeSuccess(cmd.OutOrStdout(), format, name, driverName, result, durationMs, detailed, tracePath)
}

type errorContext struct {
	profile string
	timeout string
}

func doStatus(cmd *cobra.Command, ctx context.Context, name, timeoutFlag string, insecureFlag, verbose bool, deps Deps) (*driver.StatusResult, int64, string, errorContext, error) {
	if err := profile.ValidateName(name); err != nil {
		return nil, 0, "", errorContext{}, err
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return nil, 0, "", errorContext{}, apperr.Newf(1, "cannot resolve config directory: %s", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return nil, 0, "", errorContext{}, apperr.Newf(2, "cannot load config: %s", err)
	}
	p, ok := cfg.GetProfile(name)
	if !ok {
		return nil, 0, "", errorContext{}, apperr.Newf(2, "printer profile %q not found", name)
	}

	timeoutStr := p.Timeout
	if timeoutFlag != "" {
		timeoutStr = timeoutFlag
	}
	if timeoutStr == "" {
		timeoutStr = "10s"
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return nil, 0, "", errorContext{profile: name, timeout: timeoutStr}, apperr.Newf(2, "invalid --timeout %q: %s", timeoutStr, err)
	}
	if timeout <= 0 {
		return nil, 0, "", errorContext{profile: name, timeout: timeoutStr}, apperr.Newf(2, "--timeout must be greater than zero")
	}
	errCtx := errorContext{profile: name, timeout: timeout.String()}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	insecure := p.Insecure || insecureFlag

	kcAcct := fmt.Sprintf("%s:%s:access-code", p.Driver, name)
	accessCode, err := deps.KC.Get(ctx, "polimero", kcAcct)
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return nil, 0, "", errCtx, apperr.Newf(3, "access code not found in keychain for %q", name)
		}
		return nil, 0, "", errCtx, apperr.Wrap(3, "cannot read access code from keychain", err)
	}

	var tlsFingerprint string
	if !insecure {
		kcFpAcct := fmt.Sprintf("%s:%s:tls-fingerprint", p.Driver, name)
		tlsFingerprint, err = deps.KC.Get(ctx, "polimero", kcFpAcct)
		if err != nil {
			if errors.Is(err, keychain.ErrNotFound) {
				return nil, 0, "", errCtx, apperr.Newf(3, "TLS fingerprint not found in keychain for %q", name)
			}
			return nil, 0, "", errCtx, apperr.Wrap(3, "cannot read TLS fingerprint from keychain", err)
		}
		if !driver.ValidTLSFingerprint(tlsFingerprint) {
			return nil, 0, "", errCtx, apperr.Newf(3, "invalid TLS fingerprint in keychain for %q", name)
		}
	}

	drv, ok := deps.GetDriver(p.Driver)
	if !ok {
		return nil, 0, "", errCtx, apperr.Newf(2, "unknown driver %q", p.Driver)
	}
	if !drv.Capabilities().Status {
		return nil, 0, "", errCtx, apperr.Newf(5, "driver %q does not support the status command", p.Driver)
	}

	pi := driver.ProfileInput{
		Name:     name,
		Driver:   p.Driver,
		Host:     p.Host,
		Serial:   p.Serial,
		Timeout:  timeout,
		Insecure: insecure,
	}
	secrets := driver.SecretsBundle{
		AccessCode:     accessCode,
		TLSFingerprint: tlsFingerprint,
	}

	output.Verbose(cmd.OutOrStdout(), verbose, fmt.Sprintf("Connecting to %s...", p.Host))
	start := time.Now()
	result, err := drv.Status(ctx, pi, secrets, deps.Log)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		return nil, 0, "", errCtx, err
	}
	output.Verbose(cmd.OutOrStdout(), verbose, fmt.Sprintf("Response received (%dms).", durationMs))
	return result, durationMs, p.Driver, errorContext{}, nil
}

func writeSuccess(w io.Writer, format output.Format, name, driverName string, result *driver.StatusResult, durationMs int64, detailed bool, tracePath *string) error {
	if result == nil {
		return fmt.Errorf("driver returned nil status result")
	}
	if format == output.FormatJSON {
		return writeJSONSuccess(w, name, driverName, result, durationMs, detailed, tracePath)
	}
	return writeHumanSuccess(w, name, result, detailed)
}

func writeJSONSuccess(w io.Writer, name, driverName string, result *driver.StatusResult, durationMs int64, detailed bool, tracePath *string) error {
	dm := durationMs
	type statusData struct {
		Profile       string                 `json:"profile"`
		Driver        string                 `json:"driver"`
		State         string                 `json:"state"`
		Temperatures  *driver.Temperatures   `json:"temperatures"`
		Job           *driver.Job            `json:"job"`
		Progress      *driver.Progress       `json:"progress"`
		Errors        []driver.StatusError   `json:"errors"`
		Warnings      []driver.StatusWarning `json:"warnings"`
		Capabilities  driver.Capabilities    `json:"capabilities"`
		Fans          driver.Fans            `json:"fans,omitempty"`
		TimeEstimates *driver.TimeEstimates  `json:"timeEstimates,omitempty"`
		SpeedLevel    *string                `json:"speedLevel,omitempty"`
		Wifi          *driver.Wifi           `json:"wifi,omitempty"`
		Lights        driver.Lights          `json:"lights,omitempty"`
		PrintMeta     *driver.PrintMeta      `json:"printMeta,omitempty"`
		Stage         *string                `json:"stage,omitempty"`
		Timelapse     *driver.Timelapse      `json:"timelapse,omitempty"`
		GcodePosition *driver.GcodePosition  `json:"gcodePosition,omitempty"`
		Extensions    map[string]any         `json:"extensions,omitempty"`
	}
	data := statusData{
		Profile:      name,
		Driver:       driverName,
		State:        result.State,
		Temperatures: result.Temperatures,
		Job:          result.Job,
		Progress:     result.Progress,
		Errors:       result.Errors,
		Warnings:     result.Warnings,
		Capabilities: result.Capabilities,
	}
	if detailed {
		data.Fans = result.Fans
		data.TimeEstimates = result.TimeEstimates
		data.SpeedLevel = result.SpeedLevel
		data.Wifi = result.Wifi
		data.Lights = result.Lights
		data.PrintMeta = result.PrintMeta
		data.Stage = result.Stage
		data.Timelapse = result.Timelapse
		data.GcodePosition = result.GcodePosition
		data.Extensions = result.Extensions
	}
	return output.WriteEnvelope(w, output.Envelope{
		OK:    true,
		Data:  data,
		Error: nil,
		Meta:  output.Meta{Command: "status", DurationMs: &dm, ProtocolTracePath: tracePath},
	})
}

// sanitize replaces terminal control characters in printer-supplied strings
// for safe human-readable output.
func sanitize(s string) string {
	return devicepath.SanitizeForDisplay(s)
}

func writeHumanSuccess(w io.Writer, name string, result *driver.StatusResult, detailed bool) error {
	lines := []string{
		fmt.Sprintf("Printer: %s", name),
		fmt.Sprintf("State: %s", result.State),
	}
	if detailed && result.Stage != nil && result.State != "idle" {
		lines = append(lines, fmt.Sprintf("Stage: %s", *result.Stage))
	}
	if result.Progress != nil {
		pLine := fmt.Sprintf("Progress: %d%%", result.Progress.Percent)
		if detailed && result.Progress.CurrentLayer != nil && result.Progress.TotalLayers != nil {
			pLine = fmt.Sprintf("Progress: %d%% (layer %d / %d)", result.Progress.Percent, *result.Progress.CurrentLayer, *result.Progress.TotalLayers)
		}
		lines = append(lines, pLine)
	}
	if detailed && result.SpeedLevel != nil {
		lines = append(lines, fmt.Sprintf("Speed: %s", *result.SpeedLevel))
	}
	if detailed && result.TimeEstimates != nil {
		lines = append(lines, formatTimeEstimates(result.TimeEstimates))
	}
	if result.Temperatures != nil {
		if n := result.Temperatures.Nozzle; n != nil {
			if n.TargetCelsius != nil {
				lines = append(lines, fmt.Sprintf("Nozzle: %.1f C / %.1f C", n.CurrentCelsius, *n.TargetCelsius))
			} else {
				lines = append(lines, fmt.Sprintf("Nozzle: %.1f C", n.CurrentCelsius))
			}
		}
		if b := result.Temperatures.Bed; b != nil {
			if b.TargetCelsius != nil {
				lines = append(lines, fmt.Sprintf("Bed: %.1f C / %.1f C", b.CurrentCelsius, *b.TargetCelsius))
			} else {
				lines = append(lines, fmt.Sprintf("Bed: %.1f C", b.CurrentCelsius))
			}
		}
		if c := result.Temperatures.Chamber; c != nil {
			lines = append(lines, fmt.Sprintf("Chamber: %.1f C", c.CurrentCelsius))
		}
	}
	if detailed && result.Fans != nil {
		lines = append(lines, "Fans:")
		for _, fanName := range sortedKeys(result.Fans) {
			lines = append(lines, fmt.Sprintf("  %s: %d%%", fanDisplayName(fanName), result.Fans[fanName]))
		}
	}
	if detailed && result.Wifi != nil {
		lines = append(lines, fmt.Sprintf("Wi-Fi: %d dBm", result.Wifi.SignalDbm))
	}
	if detailed && result.Lights != nil {
		lines = append(lines, "Lights:")
		for _, lightName := range sortedLightKeys(result.Lights) {
			lines = append(lines, fmt.Sprintf("  %s: %s", sanitize(lightName), sanitize(result.Lights[lightName])))
		}
	}
	if result.Job != nil {
		jobLine := fmt.Sprintf("Job: %s", sanitize(result.Job.Name))
		if detailed && result.PrintMeta != nil {
			var parts []string
			if result.PrintMeta.FileSize != nil {
				parts = append(parts, formatFileSize(*result.PrintMeta.FileSize))
			}
			if result.PrintMeta.NozzleDiameter != nil {
				parts = append(parts, fmt.Sprintf("%.1fmm nozzle", *result.PrintMeta.NozzleDiameter))
			}
			if result.PrintMeta.BedType != nil {
				parts = append(parts, sanitize(*result.PrintMeta.BedType))
			}
			if len(parts) > 0 {
				jobLine += " (" + strings.Join(parts, ", ") + ")"
			}
		}
		lines = append(lines, jobLine)
	}
	if detailed && result.GcodePosition != nil {
		gp := result.GcodePosition
		if gp.ZMm > 0 {
			lines = append(lines, fmt.Sprintf("G-code: Z %.2f mm, line %d / %d", gp.ZMm, gp.CurrentLine, gp.TotalLines))
		} else {
			lines = append(lines, fmt.Sprintf("G-code: line %d / %d", gp.CurrentLine, gp.TotalLines))
		}
	}
	if detailed && result.Timelapse != nil {
		tl := result.Timelapse
		if tl.Recording {
			if tl.Progress != nil {
				lines = append(lines, fmt.Sprintf("Timelapse: recording (%d%%)", *tl.Progress))
			} else {
				lines = append(lines, "Timelapse: recording")
			}
		} else {
			lines = append(lines, "Timelapse: off")
		}
	}
	if detailed && result.Extensions != nil {
		if ext, ok := result.Extensions["bambu-lan"]; ok {
			if bambu, ok := ext.(*driver.BambuExtension); ok && bambu.AMS != nil {
				lines = append(lines, formatAMS(bambu.AMS))
			}
		}
	}
	if len(result.Errors) > 0 {
		lines = append(lines, "Errors:")
		for _, statusErr := range result.Errors {
			if statusErr.Code != "" {
				lines = append(lines, fmt.Sprintf("- %s %s", sanitize(statusErr.Code), sanitize(statusErr.Message)))
			} else {
				lines = append(lines, fmt.Sprintf("- %s", sanitize(statusErr.Message)))
			}
		}
	}
	if len(result.Warnings) > 0 {
		lines = append(lines, "Warnings:")
		for _, warn := range result.Warnings {
			lines = append(lines, fmt.Sprintf("- %s", sanitize(warn.Message)))
		}
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}

func formatTimeEstimates(te *driver.TimeEstimates) string {
	var parts []string
	if te.ElapsedSeconds > 0 {
		parts = append(parts, formatDuration(te.ElapsedSeconds)+" elapsed")
	}
	if te.RemainingSeconds != nil && *te.RemainingSeconds > 0 {
		parts = append(parts, formatDuration(*te.RemainingSeconds)+" remaining")
	}
	if len(parts) == 0 {
		return "Time: unknown"
	}
	return "Time: " + strings.Join(parts, ", ")
}

func formatDuration(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	h := seconds / 3600
	m := (seconds % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func fanDisplayName(key string) string {
	switch key {
	case "partCooling":
		return "Part cooling"
	case "heatbreak":
		return "Heatbreak"
	case "auxiliary":
		return "Auxiliary"
	case "chamber":
		return "Chamber"
	default:
		return key
	}
}

func sortedKeys(m driver.Fans) []string {
	order := []string{"partCooling", "heatbreak", "auxiliary", "chamber"}
	var result []string
	for _, k := range order {
		if _, ok := m[k]; ok {
			result = append(result, k)
		}
	}
	return result
}

func sortedLightKeys(m driver.Lights) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func formatFileSize(bytes int) string {
	switch {
	case bytes >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1024*1024*1024))
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func formatAMS(ams *driver.AMSData) string {
	var lines []string
	lines = append(lines, "AMS:")
	for _, unit := range ams.Units {
		unitLine := fmt.Sprintf("  Unit %d", unit.ID)
		var unitParts []string
		if unit.HumidityRange != nil {
			unitParts = append(unitParts, fmt.Sprintf("humidity: %s [%s]", *unit.HumidityRange, *unit.HumidityLevel))
		}
		if unit.Temperature != nil {
			unitParts = append(unitParts, fmt.Sprintf("temp: %.1f C", *unit.Temperature))
		}
		if len(unitParts) > 0 {
			unitLine += " (" + strings.Join(unitParts, ", ") + ")"
		}
		unitLine += ":"
		lines = append(lines, unitLine)
		for _, tray := range unit.Trays {
			trayLine := fmt.Sprintf("    Slot %d: ", tray.Slot)
			if tray.FilamentType != nil {
				trayLine += sanitize(*tray.FilamentType)
				if tray.Color != nil {
					trayLine += " " + sanitize(*tray.Color)
				}
				if tray.RemainingPercent != nil {
					trayLine += fmt.Sprintf(" (%d%%)", *tray.RemainingPercent)
				}
			} else {
				trayLine += "(empty)"
			}
			lines = append(lines, trayLine)
		}
	}
	return strings.Join(lines, "\n")
}

func writeError(out, errOut io.Writer, format output.Format, err error, errCtx errorContext) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	errDetail := buildErrorDetail(err, errCtx)
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &errDetail,
			Meta:  output.Meta{Command: "status"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", errDetail.Message)
	}
	return apperr.New(code, "")
}

func buildErrorDetail(err error, errCtx errorContext) output.ErrDetail {
	detail := output.ErrDetail{Code: errorCode(err), Message: errorMessage(err)}
	if isTimeout(err) {
		detail.Code = "timeout"
		detail.Message = "status request timed out"
		if errCtx.profile != "" || errCtx.timeout != "" {
			detail.Details = map[string]any{}
			if errCtx.profile != "" {
				detail.Details["profile"] = errCtx.profile
			}
			if errCtx.timeout != "" {
				detail.Details["timeout"] = errCtx.timeout
			}
		}
	}
	return detail
}

func errorMessage(err error) string {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch errorCode(err) {
	case "authentication_failed":
		switch {
		case strings.Contains(msg, "MQTT authentication rejected"):
			return "MQTT authentication rejected"
		case strings.Contains(msg, "TLS fingerprint mismatch"):
			return "TLS fingerprint mismatch"
		default:
			return "authentication or secret error"
		}
	case "secret_not_found":
		return "secret not found"
	case "connection_failed":
		switch {
		case strings.Contains(lower, "cancelled"):
			return "status request cancelled"
		case strings.Contains(msg, "invalid status report"):
			return "invalid status report"
		case strings.Contains(msg, "status subscription failed"):
			return "status subscription failed"
		case strings.Contains(msg, "status request failed"):
			return "status request failed"
		case strings.Contains(msg, "connection failed"):
			return "connection failed"
		default:
			return "status request failed"
		}
	default:
		return msg
	}
}

func errorCode(err error) string {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		return "error"
	}
	switch exitErr.Code {
	case 2:
		return "config_error"
	case 3:
		msg := err.Error()
		if strings.Contains(msg, "MQTT authentication") || strings.Contains(msg, "TLS fingerprint mismatch") {
			return "authentication_failed"
		}
		return "secret_not_found"
	case 4:
		return "connection_failed"
	case 5:
		return "capability_unsupported"
	default:
		return "error"
	}
}

func isTimeout(err error) bool {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timed out") || strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded")
}
