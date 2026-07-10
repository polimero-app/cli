package temperature

import (
	"context"
	"io"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/profile"
)

type resolvedProfile struct {
	name    string
	driver  driver.Driver
	tempDrv driver.TemperatureDriver
	pi      driver.ProfileInput
	secrets driver.SecretsBundle
	timeout time.Duration
}

func resolveProfile(ctx context.Context, nameArg, timeoutFlag string, insecureFlag bool, deps Deps) (*resolvedProfile, error) {
	r, err := profile.Resolve(ctx, nameArg, timeoutFlag, insecureFlag, deps.KC, deps.GetDriver)
	if err != nil {
		return nil, err
	}
	tempDrv, ok := r.Driver.(driver.TemperatureDriver)
	if !ok {
		return nil, apperr.Newf(5, "driver %q does not support temperature control", r.Input.Driver)
	}
	return &resolvedProfile{
		name:    r.Name,
		driver:  r.Driver,
		tempDrv: tempDrv,
		pi:      r.Input,
		secrets: r.Secrets,
		timeout: r.Timeout,
	}, nil
}

func writeError(out, errOut io.Writer, format output.Format, cmdName string, err error) error {
	detail := output.ErrDetail{Code: cmderr.Code(err, true), Message: cmderr.CommandMessage(err)}
	return cmderr.Write(out, errOut, format, cmdName, detail, err)
}

// writeDetailError writes a structured error with a specific JSON code and details map.
func writeDetailError(out, errOut io.Writer, format output.Format, cmdName string, exitCode int, jsonCode, msg string, details map[string]any) error {
	return cmderr.WriteDetail(out, errOut, format, cmdName, exitCode, jsonCode, msg, details)
}
