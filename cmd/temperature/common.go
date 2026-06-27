package temperature

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
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/profile"
)

const defaultTimeout = "10s"

type resolvedProfile struct {
	name    string
	driver  driver.Driver
	tempDrv driver.TemperatureDriver
	pi      driver.ProfileInput
	secrets driver.SecretsBundle
	timeout time.Duration
}

func resolveProfile(ctx context.Context, nameArg, timeoutFlag string, insecureFlag bool, deps Deps) (*resolvedProfile, error) {
	name := strings.ToLower(nameArg)
	if err := profile.ValidateName(name); err != nil {
		return nil, err
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return nil, apperr.Newf(1, "cannot resolve config directory: %s", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return nil, apperr.Newf(2, "cannot load config: %s", err)
	}
	p, ok := cfg.GetProfile(name)
	if !ok {
		return nil, apperr.Newf(2, "printer profile %q not found", name)
	}

	timeoutStr := p.Timeout
	if timeoutFlag != "" {
		timeoutStr = timeoutFlag
	}
	if timeoutStr == "" {
		timeoutStr = defaultTimeout
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return nil, apperr.Newf(2, "invalid --timeout %q: %s", timeoutStr, err)
	}
	if timeout <= 0 {
		return nil, apperr.New(2, "--timeout must be greater than zero")
	}

	insecure := p.Insecure || insecureFlag

	kcAcct := fmt.Sprintf("%s:%s:access-code", p.Driver, name)
	accessCode, err := deps.KC.Get(ctx, "polimero", kcAcct)
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return nil, apperr.Newf(3, "access code not found in keychain for %q", name)
		}
		return nil, apperr.Wrap(3, "cannot read access code from keychain", err)
	}

	var tlsFingerprint string
	if !insecure {
		kcFpAcct := fmt.Sprintf("%s:%s:tls-fingerprint", p.Driver, name)
		tlsFingerprint, err = deps.KC.Get(ctx, "polimero", kcFpAcct)
		if err != nil {
			if errors.Is(err, keychain.ErrNotFound) {
				return nil, apperr.Newf(3, "TLS fingerprint not found in keychain for %q", name)
			}
			return nil, apperr.Wrap(3, "cannot read TLS fingerprint from keychain", err)
		}
		if !driver.ValidTLSFingerprint(tlsFingerprint) {
			return nil, apperr.Newf(3, "invalid TLS fingerprint in keychain for %q", name)
		}
	}

	drv, ok := deps.GetDriver(p.Driver)
	if !ok {
		return nil, apperr.Newf(2, "unknown driver %q", p.Driver)
	}

	tempDrv, ok := drv.(driver.TemperatureDriver)
	if !ok {
		return nil, apperr.Newf(5, "driver %q does not support temperature control", p.Driver)
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

	return &resolvedProfile{
		name:    name,
		driver:  drv,
		tempDrv: tempDrv,
		pi:      pi,
		secrets: secrets,
		timeout: timeout,
	}, nil
}

func writeError(out, errOut io.Writer, format output.Format, cmdName string, err error) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	errDetail := buildErrorDetail(err)
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &errDetail,
			Meta:  output.Meta{Command: cmdName},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", errDetail.Message)
	}
	return apperr.New(code, "")
}

// writeDetailError writes a structured error with a specific JSON code and details map.
func writeDetailError(out, errOut io.Writer, format output.Format, cmdName string, exitCode int, jsonCode, msg string, details map[string]any) error {
	detail := output.ErrDetail{Code: jsonCode, Message: msg, Details: details}
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &detail,
			Meta:  output.Meta{Command: cmdName},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", msg)
	}
	return apperr.New(exitCode, msg)
}

func buildErrorDetail(err error) output.ErrDetail {
	return output.ErrDetail{Code: errorCode(err), Message: err.Error()}
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
		if strings.Contains(msg, "TLS fingerprint mismatch") {
			return "authentication_failed"
		}
		return "secret_not_found"
	case 4:
		return "timeout"
	case 5:
		return "capability_unsupported"
	default:
		return "error"
	}
}
