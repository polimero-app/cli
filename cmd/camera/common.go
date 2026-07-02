package camera

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/profile"
	"github.com/spf13/cobra"
)

const defaultCameraTimeout = "10s"

type resolvedProfile struct {
	name    string
	driver  driver.Driver
	input   driver.ProfileInput
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
		timeoutStr = defaultCameraTimeout
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return nil, apperr.Newf(2, "invalid --timeout %q: %s", timeoutStr, err)
	}
	if timeout <= 0 {
		return nil, apperr.New(2, "--timeout must be greater than zero")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

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

	return &resolvedProfile{
		name:   name,
		driver: drv,
		input: driver.ProfileInput{
			Name:     name,
			Driver:   p.Driver,
			Host:     p.Host,
			Serial:   p.Serial,
			Timeout:  timeout,
			Insecure: insecure,
		},
		secrets: driver.SecretsBundle{
			AccessCode:     accessCode,
			TLSFingerprint: tlsFingerprint,
		},
		timeout: timeout,
	}, nil
}

func writeUsageError(cmd *cobra.Command, cmdName, message string) error {
	return cmderr.WriteUsage(cmd, cmdName, message)
}

func writeError(out, errOut io.Writer, format output.Format, cmdName string, err error) error {
	detail := output.ErrDetail{Code: cmderr.Code(err, false), Message: err.Error()}
	return cmderr.Write(out, errOut, format, cmdName, detail, err)
}
