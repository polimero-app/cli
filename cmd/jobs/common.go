package jobs

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
	jobDrv  driver.JobDriver
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

	jobDrv, ok := drv.(driver.JobDriver)
	if !ok {
		return nil, apperr.Newf(5, "driver %q does not support job control", p.Driver)
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
		jobDrv:  jobDrv,
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

// checkStatePrecondition fetches status and verifies it matches one of the required states.
func checkStatePrecondition(out, errOut io.Writer, format output.Format, cmdName, profileName string, requiredStates []string, rp *resolvedProfile, deps Deps, ctx context.Context) (*driver.StatusResult, error) {
	status, err := rp.driver.Status(ctx, rp.pi, rp.secrets, deps.Log)
	if err != nil {
		return nil, writeError(out, errOut, format, cmdName, err)
	}
	if status == nil {
		return nil, writeError(out, errOut, format, cmdName, apperr.New(1, "driver returned nil status result"))
	}

	for _, s := range requiredStates {
		if status.State == s {
			return status, nil
		}
	}

	required := strings.Join(requiredStates, " or ")
	return nil, writeDetailError(out, errOut, format, cmdName, 2,
		"invalid_printer_state",
		fmt.Sprintf("cannot perform action: printer is %s, expected %s", status.State, required),
		map[string]any{
			"profile":       profileName,
			"currentState":  status.State,
			"requiredState": required,
		})
}

// checkConfirmation handles interactive/non-interactive confirmation.
func checkConfirmation(out, errOut io.Writer, format output.Format, cmdName string, yes bool, promptMsg string, deps Deps) error {
	if yes {
		return nil
	}
	if !deps.Prompter.IsTerminal() {
		return writeDetailError(out, errOut, format, cmdName, 2,
			"config_error", "non-interactive mode requires --yes", nil)
	}
	answer, readErr := deps.Prompter.ReadLine(promptMsg)
	if readErr != nil {
		return writeError(out, errOut, format, cmdName, apperr.Newf(1, "cannot read confirmation: %s", readErr))
	}
	if answer != "yes" {
		return writeDetailError(out, errOut, format, cmdName, 2,
			"config_error", "confirmation declined", nil)
	}
	return nil
}

// checkExpectedState verifies the driver returned the expected resulting state.
func checkExpectedState(out, errOut io.Writer, format output.Format, cmdName, profileName, action, expectedState string, result driver.JobActionResult) error {
	if result.State == expectedState {
		return nil
	}
	return writeDetailError(out, errOut, format, cmdName, 1,
		"job_action_failed",
		fmt.Sprintf("%s did not result in the expected state", action),
		map[string]any{
			"profile":       profileName,
			"action":        action,
			"expectedState": expectedState,
			"observedState": result.State,
		})
}

// writeActionSuccess writes a successful job action result in the appropriate format.
func writeActionSuccess(w io.Writer, format output.Format, cmdName, profileName, driverName, action, devicePath string, plate *int, result driver.JobActionResult, durationMs int64) error {
	if format == output.FormatJSON {
		return writeActionJSONSuccess(w, cmdName, profileName, driverName, action, devicePath, plate, result, durationMs)
	}
	return writeActionHumanSuccess(w, action)
}

func writeActionJSONSuccess(w io.Writer, cmdName, profileName, driverName, action, devicePath string, plate *int, result driver.JobActionResult, durationMs int64) error {
	dm := durationMs
	type data struct {
		Profile      string                 `json:"profile"`
		Driver       string                 `json:"driver"`
		Action       string                 `json:"action"`
		DevicePath   *string                `json:"devicePath,omitempty"`
		Plate        *int                   `json:"plate,omitempty"`
		State        string                 `json:"state"`
		Warnings     []driver.StatusWarning `json:"warnings"`
		Capabilities driver.Capabilities    `json:"capabilities"`
	}
	warnings := result.Warnings
	if warnings == nil {
		warnings = []driver.StatusWarning{}
	}
	d := data{
		Profile:      profileName,
		Driver:       driverName,
		Action:       action,
		State:        result.State,
		Warnings:     warnings,
		Capabilities: result.Capabilities,
	}
	if devicePath != "" {
		d.DevicePath = &devicePath
	}
	if plate != nil {
		d.Plate = plate
	}
	return output.WriteEnvelope(w, output.Envelope{
		OK:    true,
		Data:  d,
		Error: nil,
		Meta:  output.Meta{Command: cmdName, DurationMs: &dm},
	})
}

func writeActionHumanSuccess(w io.Writer, action string) error {
	var msg string
	switch action {
	case "start":
		msg = "Job started."
	case "pause":
		msg = "Job paused."
	case "resume":
		msg = "Job resumed."
	case "cancel":
		msg = "Job canceled."
	default:
		msg = fmt.Sprintf("Job %s.", action)
	}
	_, err := fmt.Fprintln(w, msg)
	return err
}

// validateDevicePath ensures a device path has a named-root format ("root:/path").
func validateDevicePath(path string) error {
	idx := strings.Index(path, ":/")
	if idx <= 0 {
		return apperr.Newf(2, "invalid device path %q: must use format root:/path (e.g. sdcard:/models/cube.3mf)", path)
	}
	return nil
}
