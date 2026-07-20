// Package cmdrun holds run-flow helpers shared by per-printer control
// command groups (fans, lights, speed).
package cmdrun

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/profile"
	"github.com/polimero-app/cli/internal/tty"
)

// WriteError classifies err and writes the standard error envelope.
func WriteError(out, errOut io.Writer, format output.Format, cmdName string, err error) error {
	detail := output.ErrDetail{Code: cmderr.Code(err, true), Message: cmderr.CommandMessage(err)}
	return cmderr.Write(out, errOut, format, cmdName, detail, err)
}

// CheckStatePrecondition fetches printer status and requires the state to be
// one of requiredStates, writing an invalid_printer_state error otherwise.
func CheckStatePrecondition(ctx context.Context, out, errOut io.Writer, format output.Format, cmdName string, requiredStates []string, rp *profile.Resolved, log *slog.Logger) (*driver.StatusResult, error) {
	status, err := rp.Driver.Status(ctx, rp.Input, rp.Secrets, log)
	if err != nil {
		return nil, WriteError(out, errOut, format, cmdName, err)
	}
	if status == nil {
		return nil, WriteError(out, errOut, format, cmdName, apperr.New(1, "driver returned nil status result"))
	}

	for _, s := range requiredStates {
		if status.State == s {
			return status, nil
		}
	}

	required := strings.Join(requiredStates, " or ")
	return nil, cmderr.WriteDetail(out, errOut, format, cmdName, 2,
		"invalid_printer_state",
		fmt.Sprintf("cannot perform action: printer is %s, expected %s", status.State, required),
		map[string]any{
			"profile":       rp.Name,
			"currentState":  status.State,
			"requiredState": required,
		})
}

// CheckIdlePrecondition fetches printer status and requires the idle state,
// writing an invalid_printer_state error shaped "cannot <action> while
// <state>" otherwise.
func CheckIdlePrecondition(ctx context.Context, out, errOut io.Writer, format output.Format, cmdName, action string, rp *profile.Resolved, log *slog.Logger) (*driver.StatusResult, error) {
	status, err := rp.Driver.Status(ctx, rp.Input, rp.Secrets, log)
	if err != nil {
		return nil, WriteError(out, errOut, format, cmdName, err)
	}
	if status == nil {
		return nil, WriteError(out, errOut, format, cmdName, apperr.New(1, "driver returned nil status result"))
	}
	if status.State != "idle" {
		return nil, cmderr.WriteDetail(out, errOut, format, cmdName, 2,
			"invalid_printer_state",
			fmt.Sprintf("cannot %s while %s", action, status.State),
			map[string]any{
				"profile":       rp.Name,
				"currentState":  status.State,
				"requiredState": "idle",
			})
	}
	return status, nil
}

// CheckConfirmation prompts for an interactive "yes" unless yes is set,
// writing a config_error when confirmation is unavailable or declined.
func CheckConfirmation(out, errOut io.Writer, format output.Format, cmdName string, yes bool, promptMsg string, prompter tty.Prompter) error {
	if yes {
		return nil
	}
	if !prompter.IsTerminal() {
		return cmderr.WriteDetail(out, errOut, format, cmdName, 2,
			"config_error", "non-interactive mode requires --yes", nil)
	}
	answer, readErr := prompter.ReadLine(promptMsg)
	if readErr != nil {
		return WriteError(out, errOut, format, cmdName, apperr.Newf(1, "cannot read confirmation: %s", readErr))
	}
	if answer != "yes" {
		return cmderr.WriteDetail(out, errOut, format, cmdName, 2,
			"config_error", "confirmation declined", nil)
	}
	return nil
}
