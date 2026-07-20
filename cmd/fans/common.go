package fans

import (
	"context"
	"fmt"
	"io"
	"strings"
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
	fanDrv  driver.FanDriver
	pi      driver.ProfileInput
	secrets driver.SecretsBundle
	timeout time.Duration
}

func resolveProfile(ctx context.Context, nameArg, timeoutFlag string, insecureFlag bool, deps Deps) (*resolvedProfile, error) {
	r, err := profile.Resolve(ctx, nameArg, timeoutFlag, insecureFlag, deps.KC, deps.GetDriver)
	if err != nil {
		return nil, err
	}
	fanDrv, ok := r.Driver.(driver.FanDriver)
	if !ok {
		return nil, apperr.Newf(5, "driver %q does not support fan control", r.Input.Driver)
	}
	return &resolvedProfile{
		name:    r.Name,
		driver:  r.Driver,
		fanDrv:  fanDrv,
		pi:      r.Input,
		secrets: r.Secrets,
		timeout: r.Timeout,
	}, nil
}

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

func writeError(out, errOut io.Writer, format output.Format, cmdName string, err error) error {
	detail := output.ErrDetail{Code: cmderr.Code(err, true), Message: cmderr.CommandMessage(err)}
	return cmderr.Write(out, errOut, format, cmdName, detail, err)
}

func writeDetailError(out, errOut io.Writer, format output.Format, cmdName string, exitCode int, jsonCode, msg string, details map[string]any) error {
	return cmderr.WriteDetail(out, errOut, format, cmdName, exitCode, jsonCode, msg, details)
}
