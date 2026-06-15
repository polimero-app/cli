package printer

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
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/spf13/cobra"
)

// StatusDeps holds injectable dependencies for the printer status command.
type StatusDeps struct {
	KC        keychain.Keychain
	GetDriver func(string) (driver.Driver, bool)
	Log       *slog.Logger
}

func statusCommand() *cobra.Command {
	return StatusCommandWithDeps(StatusDeps{
		KC:        keychain.NewReal(),
		GetDriver: drivers.Get,
		Log:       slog.Default(),
	})
}

// StatusCommandWithDeps constructs the "status" cobra command with injected dependencies.
func StatusCommandWithDeps(deps StatusDeps) *cobra.Command {
	var flags struct {
		timeout  string
		insecure bool
	}

	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show the current status of a printer",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if len(args) > 1 {
				return writeStatusUsageError(cmd, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runStatus(cmd, args[0], flags.timeout, flags.insecure, deps)
		},
	}
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	return cmd
}

func writeStatusUsageError(cmd *cobra.Command, message string) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}
	return writeStatusError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, apperr.New(2, message), statusErrorContext{})
}

func runStatus(cmd *cobra.Command, nameArg, timeoutFlag string, insecureFlag bool, deps StatusDeps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}

	name := strings.ToLower(nameArg)
	result, durationMs, driverName, errCtx, err := doStatus(cmd, name, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeStatusError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, err, errCtx)
	}
	return writeStatusSuccess(cmd.OutOrStdout(), format, name, driverName, result, durationMs)
}

type statusErrorContext struct {
	profile string
	timeout string
}

func doStatus(cmd *cobra.Command, name, timeoutFlag string, insecureFlag bool, deps StatusDeps) (*driver.StatusResult, int64, string, statusErrorContext, error) {
	if err := validateProfileName(name); err != nil {
		return nil, 0, "", statusErrorContext{}, err
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return nil, 0, "", statusErrorContext{}, apperr.Newf(1, "cannot resolve config directory: %s", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return nil, 0, "", statusErrorContext{}, apperr.Newf(2, "cannot load config: %s", err)
	}
	p, ok := cfg.GetProfile(name)
	if !ok {
		return nil, 0, "", statusErrorContext{}, apperr.Newf(2, "printer profile %q not found", name)
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
		return nil, 0, "", statusErrorContext{profile: name, timeout: timeoutStr}, apperr.Newf(2, "invalid --timeout %q: %s", timeoutStr, err)
	}
	if timeout <= 0 {
		return nil, 0, "", statusErrorContext{profile: name, timeout: timeoutStr}, apperr.Newf(2, "--timeout must be greater than zero")
	}
	errCtx := statusErrorContext{profile: name, timeout: timeout.String()}
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
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

	start := time.Now()
	result, err := drv.Status(ctx, pi, secrets, deps.Log)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		return nil, 0, "", errCtx, err
	}
	return result, durationMs, p.Driver, statusErrorContext{}, nil
}

func writeStatusSuccess(w io.Writer, format output.Format, name, driverName string, result *driver.StatusResult, durationMs int64) error {
	if format == output.FormatJSON {
		dm := durationMs
		type statusData struct {
			Profile string `json:"profile"`
			Driver  string `json:"driver"`
			*driver.StatusResult
		}
		data := statusData{
			Profile:      name,
			Driver:       driverName,
			StatusResult: result,
		}
		return output.WriteEnvelope(w, output.Envelope{
			OK:    true,
			Data:  data,
			Error: nil,
			Meta:  output.Meta{Command: "printer status", DurationMs: &dm},
		})
	}
	lines := []string{
		fmt.Sprintf("Printer: %s", name),
		fmt.Sprintf("State: %s", result.State),
	}
	if result.Progress != nil {
		lines = append(lines, fmt.Sprintf("Progress: %d%%", result.Progress.Percent))
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
	if result.Job != nil {
		lines = append(lines, fmt.Sprintf("Job: %s", result.Job.Name))
	}
	if len(result.Errors) > 0 {
		lines = append(lines, "Errors:")
		for _, statusErr := range result.Errors {
			if statusErr.Code != "" {
				lines = append(lines, fmt.Sprintf("- %s %s", statusErr.Code, statusErr.Message))
			} else {
				lines = append(lines, fmt.Sprintf("- %s", statusErr.Message))
			}
		}
	}
	if len(result.Warnings) > 0 {
		lines = append(lines, "Warnings:")
		for _, warn := range result.Warnings {
			lines = append(lines, fmt.Sprintf("- %s", warn.Message))
		}
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}

func writeStatusError(out, errOut io.Writer, format output.Format, err error, errCtx statusErrorContext) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	errDetail := statusErrorDetail(err, errCtx)
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &errDetail,
			Meta:  output.Meta{Command: "printer status"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", errDetail.Message)
	}
	return apperr.New(code, "")
}

func statusErrorDetail(err error, errCtx statusErrorContext) output.ErrDetail {
	detail := output.ErrDetail{Code: statusErrorCode(err), Message: statusErrorMessage(err)}
	if isStatusTimeout(err) {
		detail.Code = "timeout"
		detail.Message = "printer status request timed out"
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

func statusErrorMessage(err error) string {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch statusErrorCode(err) {
	case "auth_error":
		switch {
		case strings.Contains(msg, "MQTT authentication rejected"):
			return "MQTT authentication rejected"
		case strings.Contains(msg, "TLS fingerprint mismatch"):
			return "TLS fingerprint mismatch"
		case strings.Contains(lower, "keychain"):
			return msg
		default:
			return "authentication or secret error"
		}
	case "network_error":
		switch {
		case strings.Contains(lower, "cancelled"):
			return "printer status request cancelled"
		case strings.Contains(msg, "invalid status report"):
			return "invalid status report"
		case strings.Contains(msg, "status subscription failed"):
			return "status subscription failed"
		case strings.Contains(msg, "status request failed"):
			return "status request failed"
		case strings.Contains(msg, "connection failed"):
			return "connection failed"
		default:
			return "printer status request failed"
		}
	default:
		return msg
	}
}

func statusErrorCode(err error) string {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		return "error"
	}
	switch exitErr.Code {
	case 2:
		return "config_error"
	case 3:
		return "auth_error"
	case 4:
		return "network_error"
	case 5:
		return "capability_unsupported"
	default:
		return "error"
	}
}

func isStatusTimeout(err error) bool {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timed out") || strings.Contains(msg, "timeout")
}
