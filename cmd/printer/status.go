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
	return writeStatusError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, apperr.New(2, message))
}

func runStatus(cmd *cobra.Command, nameArg, timeoutFlag string, insecureFlag bool, deps StatusDeps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}

	name := strings.ToLower(nameArg)
	result, durationMs, err := doStatus(cmd, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeStatusError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, err)
	}
	return writeStatusSuccess(cmd.OutOrStdout(), format, name, result, durationMs)
}

func doStatus(cmd *cobra.Command, nameArg, timeoutFlag string, insecureFlag bool, deps StatusDeps) (*driver.StatusResult, int64, error) {
	name := strings.ToLower(nameArg)
	if err := validateProfileName(name); err != nil {
		return nil, 0, err
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return nil, 0, apperr.Newf(1, "cannot resolve config directory: %s", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return nil, 0, apperr.Newf(2, "cannot load config: %s", err)
	}
	p, ok := cfg.GetProfile(name)
	if !ok {
		return nil, 0, apperr.Newf(2, "printer profile %q not found", name)
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
		return nil, 0, apperr.Newf(2, "invalid --timeout %q: %s", timeoutStr, err)
	}
	if timeout <= 0 {
		return nil, 0, apperr.Newf(2, "--timeout must be greater than zero")
	}

	insecure := p.Insecure || insecureFlag

	kcAcct := fmt.Sprintf("%s:%s:access-code", p.Driver, name)
	accessCode, err := deps.KC.Get("polimero", kcAcct)
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return nil, 0, apperr.Newf(3, "access code not found in keychain for %q", name)
		}
		return nil, 0, apperr.Newf(3, "cannot read access code from keychain: %s", err)
	}

	var tlsFingerprint string
	if !insecure {
		kcFpAcct := fmt.Sprintf("%s:%s:tls-fingerprint", p.Driver, name)
		tlsFingerprint, err = deps.KC.Get("polimero", kcFpAcct)
		if err != nil {
			if errors.Is(err, keychain.ErrNotFound) {
				return nil, 0, apperr.Newf(3, "TLS fingerprint not found in keychain for %q", name)
			}
			return nil, 0, apperr.Newf(3, "cannot read TLS fingerprint from keychain: %s", err)
		}
	}

	drv, ok := deps.GetDriver(p.Driver)
	if !ok {
		return nil, 0, apperr.Newf(2, "unknown driver %q", p.Driver)
	}
	if !drv.Capabilities().Status {
		return nil, 0, apperr.Newf(5, "driver %q does not support the status command", p.Driver)
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

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	start := time.Now()
	result, err := drv.Status(ctx, pi, secrets, deps.Log)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		return nil, 0, err
	}
	return result, durationMs, nil
}

func writeStatusSuccess(w io.Writer, format output.Format, name string, result *driver.StatusResult, durationMs int64) error {
	if format == output.FormatJSON {
		dm := durationMs
		return output.WriteEnvelope(w, output.Envelope{
			OK:    true,
			Data:  result,
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
				lines = append(lines, fmt.Sprintf("Nozzle: %.1f °C / %.1f °C", n.CurrentCelsius, *n.TargetCelsius))
			} else {
				lines = append(lines, fmt.Sprintf("Nozzle: %.1f °C", n.CurrentCelsius))
			}
		}
		if b := result.Temperatures.Bed; b != nil {
			if b.TargetCelsius != nil {
				lines = append(lines, fmt.Sprintf("Bed: %.1f °C / %.1f °C", b.CurrentCelsius, *b.TargetCelsius))
			} else {
				lines = append(lines, fmt.Sprintf("Bed: %.1f °C", b.CurrentCelsius))
			}
		}
		if c := result.Temperatures.Chamber; c != nil {
			lines = append(lines, fmt.Sprintf("Chamber: %.1f °C", c.CurrentCelsius))
		}
	}
	if result.Job != nil {
		lines = append(lines, fmt.Sprintf("Job: %s", result.Job.Name))
	}
	for _, warn := range result.Warnings {
		lines = append(lines, fmt.Sprintf("Warning: %s", warn.Message))
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}

func writeStatusError(out, errOut io.Writer, format output.Format, err error) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &output.ErrDetail{Code: statusErrorCode(err), Message: err.Error()},
			Meta:  output.Meta{Command: "printer status"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", err)
	}
	return apperr.New(code, "")
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
