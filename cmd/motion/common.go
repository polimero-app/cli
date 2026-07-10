package motion

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/profile"
)

type resolvedProfile struct {
	name      string
	driver    driver.Driver
	motionDrv driver.MotionDriver
	pi        driver.ProfileInput
	secrets   driver.SecretsBundle
	timeout   time.Duration
}

func resolveProfile(ctx context.Context, nameArg, timeoutFlag string, insecureFlag bool, deps Deps) (*resolvedProfile, error) {
	r, err := profile.Resolve(ctx, nameArg, timeoutFlag, insecureFlag, deps.KC, deps.GetDriver)
	if err != nil {
		return nil, err
	}
	motionDrv, ok := r.Driver.(driver.MotionDriver)
	if !ok {
		return nil, apperr.Newf(5, "driver %q does not support motion control", r.Input.Driver)
	}
	return &resolvedProfile{
		name:      r.Name,
		driver:    r.Driver,
		motionDrv: motionDrv,
		pi:        r.Input,
		secrets:   r.Secrets,
		timeout:   r.Timeout,
	}, nil
}

// checkIdlePrecondition verifies the printer is idle and returns a detail error if not.
func checkIdlePrecondition(out, errOut io.Writer, format output.Format, cmdName, profileName, action string, rp *resolvedProfile, deps Deps, ctx context.Context) (*driver.StatusResult, error) {
	status, err := rp.driver.Status(ctx, rp.pi, rp.secrets, deps.Log)
	if err != nil {
		return nil, writeError(out, errOut, format, cmdName, err)
	}
	if status == nil {
		return nil, writeError(out, errOut, format, cmdName, apperr.New(1, "driver returned nil status result"))
	}
	if status.State != "idle" {
		return nil, writeDetailError(out, errOut, format, cmdName, 2,
			"invalid_printer_state",
			fmt.Sprintf("cannot %s while %s", action, status.State),
			map[string]any{
				"profile":       profileName,
				"currentState":  status.State,
				"requiredState": "idle",
			})
	}
	return status, nil
}

func writeError(out, errOut io.Writer, format output.Format, cmdName string, err error) error {
	detail := output.ErrDetail{Code: cmderr.Code(err, true), Message: cmderr.CommandMessage(err)}
	return cmderr.Write(out, errOut, format, cmdName, detail, err)
}

// writeDetailError writes a structured error with a specific JSON code and details map.
func writeDetailError(out, errOut io.Writer, format output.Format, cmdName string, exitCode int, jsonCode, msg string, details map[string]any) error {
	return cmderr.WriteDetail(out, errOut, format, cmdName, exitCode, jsonCode, msg, details)
}
