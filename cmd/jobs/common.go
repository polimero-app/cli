package jobs

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
	jobDrv  driver.JobDriver
	pi      driver.ProfileInput
	secrets driver.SecretsBundle
	timeout time.Duration
}

func resolveProfile(ctx context.Context, nameArg, timeoutFlag string, insecureFlag bool, deps Deps) (*resolvedProfile, error) {
	r, err := profile.Resolve(ctx, nameArg, timeoutFlag, insecureFlag, deps.KC, deps.GetDriver)
	if err != nil {
		return nil, err
	}
	jobDrv, ok := r.Driver.(driver.JobDriver)
	if !ok {
		return nil, apperr.Newf(5, "driver %q does not support job control", r.Input.Driver)
	}
	return &resolvedProfile{
		name:    r.Name,
		driver:  r.Driver,
		jobDrv:  jobDrv,
		pi:      r.Input,
		secrets: r.Secrets,
		timeout: r.Timeout,
	}, nil
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
func writeActionSuccess(w io.Writer, format output.Format, cmdName, profileName, driverName, action, devicePath string, plate *int, result driver.JobActionResult, durationMs int64, tracePath *string) error {
	if format == output.FormatJSON {
		return writeActionJSONSuccess(w, cmdName, profileName, driverName, action, devicePath, plate, result, durationMs, tracePath)
	}
	return writeActionHumanSuccess(w, profileName, action)
}

func writeActionJSONSuccess(w io.Writer, cmdName, profileName, driverName, action, devicePath string, plate *int, result driver.JobActionResult, durationMs int64, tracePath *string) error {
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
		Meta:  output.Meta{Command: cmdName, DurationMs: &dm, ProtocolTracePath: tracePath},
	})
}

func writeActionHumanSuccess(w io.Writer, profileName, action string) error {
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
	if _, err := fmt.Fprintf(w, "Printer: %s\n", profileName); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, msg)
	return err
}

func writeError(out, errOut io.Writer, format output.Format, cmdName string, err error) error {
	detail := output.ErrDetail{Code: cmderr.Code(err, true), Message: cmderr.CommandMessage(err)}
	return cmderr.Write(out, errOut, format, cmdName, detail, err)
}

// writeDetailError writes a structured error with a specific JSON code and details map.
func writeDetailError(out, errOut io.Writer, format output.Format, cmdName string, exitCode int, jsonCode, msg string, details map[string]any) error {
	return cmderr.WriteDetail(out, errOut, format, cmdName, exitCode, jsonCode, msg, details)
}
