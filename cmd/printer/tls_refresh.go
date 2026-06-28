package printer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

// TlsRefreshDeps holds injectable dependencies for the printer tls refresh command.
type TlsRefreshDeps struct {
	KC        keychain.Keychain
	GetDriver func(string) (driver.Driver, bool)
	Prompter  tty.Prompter
}

func tlsRefreshCommand() *cobra.Command {
	return TlsRefreshCommandWithDeps(TlsRefreshDeps{
		KC:        keychain.NewReal(),
		GetDriver: drivers.Get,
		Prompter:  tty.NewReal(),
	})
}

// TlsRefreshCommandWithDeps constructs the "tls refresh" cobra command with injected dependencies.
func TlsRefreshCommandWithDeps(deps TlsRefreshDeps) *cobra.Command {
	var flags struct {
		timeout       string
		insecure      bool
		yes           bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "refresh <name>",
		Short: "Re-pin or disable TLS certificate for a printer profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return writeTlsRefreshUsageError(cmd, "profile name is required")
			}
			if len(args) > 1 {
				return writeTlsRefreshUsageError(cmd, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runTlsRefresh(cmd, args[0], flags.timeout, flags.insecure, flags.yes, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "disable TLS verification for this profile")
	cmd.Flags().BoolVar(&flags.yes, "yes", false, "skip interactive confirmation")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func writeTlsRefreshUsageError(cmd *cobra.Command, message string) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}
	return writeTlsRefreshError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, apperr.New(2, message), "")
}

func runTlsRefresh(cmd *cobra.Command, nameArg, timeoutFlag string, insecureFlag, yes bool, protocolTrace string, deps TlsRefreshDeps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return writeTlsRefreshError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, traceErr, "")
	}
	defer func() { _ = traceCleanup() }()

	verboseFlag, _ := cmd.Root().PersistentFlags().GetBool("verbose")
	verbose := verboseFlag && format == output.FormatHuman

	name := strings.ToLower(nameArg)
	fp, durationMs, errName, err := doTlsRefresh(cmd, traceCtx, name, timeoutFlag, insecureFlag, yes, verbose, deps)
	if err != nil {
		return writeTlsRefreshError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, err, errName)
	}
	return writeTlsRefreshSuccess(cmd.OutOrStdout(), format, name, fp, insecureFlag, durationMs, protocolTrace)
}

// doTlsRefresh executes the core logic. Returns (fingerprint, durationMs, profileName, error).
// profileName is used for error details; it is empty when the error occurs before name is known.
func doTlsRefresh(cmd *cobra.Command, ctx context.Context, name, timeoutFlag string, insecureFlag, yes, verbose bool, deps TlsRefreshDeps) (string, int64, string, error) {
	if err := validateProfileName(name); err != nil {
		return "", 0, "", err
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return "", 0, "", apperr.Newf(1, "cannot resolve config directory: %s", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return "", 0, name, apperr.Newf(2, "cannot load config: %s", err)
	}
	p, ok := cfg.GetProfile(name)
	if !ok {
		return "", 0, name, apperr.Newf(2, "printer profile %q not found", name)
	}

	if !yes {
		if !deps.Prompter.IsTerminal() {
			return "", 0, name, apperr.New(2, "non-interactive mode requires --yes")
		}
		answer, promptErr := deps.Prompter.ReadLine(
			fmt.Sprintf("Re-pin TLS certificate for %s? Type 'yes' to continue: ", name),
		)
		if promptErr != nil {
			return "", 0, name, apperr.Newf(1, "cannot read confirmation: %s", promptErr)
		}
		if answer != "yes" {
			return "", 0, name, apperr.New(2, "confirmation declined; TLS certificate not updated")
		}
	}

	drv, ok := deps.GetDriver(p.Driver)
	if !ok {
		return "", 0, name, apperr.Newf(2, "unknown driver %q", p.Driver)
	}
	if !drv.Capabilities().TLSRefresh {
		return "", 0, name, apperr.Newf(5, "driver %q does not support the tls refresh command", p.Driver)
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
		return "", 0, name, apperr.Newf(2, "invalid --timeout %q: %s", timeoutStr, err)
	}
	if timeout <= 0 {
		return "", 0, name, apperr.Newf(2, "--timeout must be greater than zero")
	}

	kcFpAcct := fmt.Sprintf("%s:%s:tls-fingerprint", p.Driver, name)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if insecureFlag {
		p.Insecure = true
		p.Updated = time.Now().UTC()
		if setErr := cfg.SetProfile(name, p); setErr != nil {
			return "", 0, name, apperr.Newf(1, "cannot update profile: %s", setErr)
		}
		if saveErr := config.Save(dir, cfg); saveErr != nil {
			return "", 0, name, apperr.Newf(1, "cannot save config: %s", saveErr)
		}
		// Config saved successfully; now clean up keychain (best-effort).
		if delErr := deps.KC.Delete(ctx, "polimero", kcFpAcct); delErr != nil && !errors.Is(delErr, keychain.ErrNotFound) {
			return "", 0, name, apperr.Wrap(3, "cannot delete TLS fingerprint from keychain", delErr)
		}
		return "", 0, "", nil
	}

	output.Verbose(cmd.OutOrStdout(), verbose, fmt.Sprintf("Connecting to %s...", p.Host))
	captureInput := driver.ProfileInput{
		Name:   name,
		Driver: p.Driver,
		Host:   p.Host,
		Serial: p.Serial,
	}
	start := time.Now()
	fp, err := drv.CaptureFingerprint(ctx, captureInput)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		return "", 0, name, err
	}
	if !driver.ValidTLSFingerprint(fp) {
		return "", 0, name, apperr.New(4, "driver returned invalid TLS fingerprint")
	}
	output.Verbose(cmd.OutOrStdout(), verbose, fmt.Sprintf("TLS certificate captured. New fingerprint: %s", fp))

	output.Verbose(cmd.OutOrStdout(), verbose, "Updating TLS fingerprint in keychain...")
	if setErr := deps.KC.Set(ctx, "polimero", kcFpAcct, fp); setErr != nil {
		return "", 0, name, apperr.Wrap(3, "cannot store TLS fingerprint in keychain", setErr)
	}

	p.Insecure = false
	p.Updated = time.Now().UTC()
	if setErr := cfg.SetProfile(name, p); setErr != nil {
		return "", 0, name, apperr.Newf(1, "cannot update profile: %s", setErr)
	}
	if saveErr := config.Save(dir, cfg); saveErr != nil {
		return "", 0, name, apperr.Newf(1, "cannot save config: %s", saveErr)
	}
	return fp, durationMs, "", nil
}

func writeTlsRefreshSuccess(w io.Writer, format output.Format, name, fp string, insecure bool, durationMs int64, tracePath string) error {
	if format == output.FormatJSON {
		var fpVal any
		if fp != "" {
			fpVal = fp
		}
		meta := output.Meta{Command: "printer tls refresh"}
		if tracePath != "" {
			meta.ProtocolTracePath = &tracePath
		}
		env := output.Envelope{
			OK: true,
			Data: map[string]any{
				"profile":     name,
				"fingerprint": fpVal,
				"insecure":    insecure,
			},
			Error: nil,
			Meta:  meta,
		}
		if !insecure {
			dm := durationMs
			env.Meta.DurationMs = &dm
		}
		return output.WriteEnvelope(w, env)
	}

	if insecure {
		_, err := fmt.Fprintf(w, "TLS certificate verification disabled: %s\nWarning: TLS verification is disabled for this profile.\n", name)
		return err
	}
	_, err := fmt.Fprintf(w, "TLS certificate re-pinned: %s\nFingerprint: %s\n", name, fp)
	return err
}

func writeTlsRefreshError(out, errOut io.Writer, format output.Format, err error, profileName string) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	if format == output.FormatJSON {
		errDetail := output.ErrDetail{
			Code:    tlsRefreshErrorCode(err),
			Message: tlsRefreshErrorMessage(err),
		}
		if profileName != "" {
			errDetail.Details = map[string]any{"profile": profileName}
		}
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &errDetail,
			Meta:  output.Meta{Command: "printer tls refresh"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", tlsRefreshErrorMessage(err))
	}
	return apperr.New(code, "")
}

func tlsRefreshErrorMessage(err error) string {
	msg := err.Error()
	switch tlsRefreshErrorCode(err) {
	case "secret_not_found":
		return "keychain operation failed"
	case "connection_failed":
		if strings.Contains(msg, "TLS connect failed") {
			return "TLS connect failed"
		}
		if strings.Contains(msg, "driver returned invalid TLS fingerprint") {
			return msg
		}
		return "TLS refresh failed"
	default:
		return msg
	}
}

func tlsRefreshErrorCode(err error) string {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		return "error"
	}
	switch exitErr.Code {
	case 2:
		return "config_error"
	case 3:
		return "secret_not_found"
	case 4:
		return "connection_failed"
	case 5:
		return "capability_unsupported"
	default:
		return "error"
	}
}
