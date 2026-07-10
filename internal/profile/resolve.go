package profile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
)

const defaultTimeout = "10s"

// Resolved holds everything a command needs after profile/secret resolution.
type Resolved struct {
	Name    string
	Driver  driver.Driver
	Input   driver.ProfileInput
	Secrets driver.SecretsBundle
	Timeout time.Duration
}

// Resolve loads and validates the named printer profile, resolves the
// effective timeout, retrieves secrets from the keychain (reads bounded by
// the resolved timeout), and locates the driver. It is the shared resolution
// path for all per-printer commands.
func Resolve(
	ctx context.Context,
	nameArg, timeoutFlag string,
	insecureFlag bool,
	kc keychain.Keychain,
	getDriver func(string) (driver.Driver, bool),
) (*Resolved, error) {
	name := strings.ToLower(nameArg)
	if err := ValidateName(name); err != nil {
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

	// Bound keychain reads; the caller owns the deadline for driver calls.
	kcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	insecure := p.Insecure || insecureFlag
	kcAcct := fmt.Sprintf("%s:%s:access-code", p.Driver, name)
	accessCode, err := kc.Get(kcCtx, "polimero", kcAcct)
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return nil, apperr.Newf(3, "access code not found in keychain for %q", name)
		}
		return nil, apperr.Wrap(3, "cannot read access code from keychain", err)
	}

	var tlsFingerprint string
	if !insecure {
		kcFpAcct := fmt.Sprintf("%s:%s:tls-fingerprint", p.Driver, name)
		tlsFingerprint, err = kc.Get(kcCtx, "polimero", kcFpAcct)
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

	drv, ok := getDriver(p.Driver)
	if !ok {
		return nil, apperr.Newf(2, "unknown driver %q", p.Driver)
	}

	return &Resolved{
		Name:   name,
		Driver: drv,
		Input: driver.ProfileInput{
			Name:     name,
			Driver:   p.Driver,
			Host:     p.Host,
			Serial:   p.Serial,
			Timeout:  timeout,
			Insecure: insecure,
		},
		Secrets: driver.SecretsBundle{
			AccessCode:     accessCode,
			TLSFingerprint: tlsFingerprint,
		},
		Timeout: timeout,
	}, nil
}
