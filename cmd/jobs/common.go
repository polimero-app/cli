package jobs

import (
	"context"
	"fmt"
	"io"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/profile"
)

func resolveProfile(ctx context.Context, nameArg, timeoutFlag string, insecureFlag bool, deps Deps) (*profile.Resolved, driver.JobDriver, error) {
	rp, err := profile.Resolve(ctx, nameArg, timeoutFlag, insecureFlag, deps.KC, deps.GetDriver)
	if err != nil {
		return nil, nil, err
	}
	jobDrv, ok := rp.Driver.(driver.JobDriver)
	if !ok {
		return nil, nil, apperr.Newf(5, "driver %q does not support job control", rp.Input.Driver)
	}
	return rp, jobDrv, nil
}

// checkExpectedState verifies the driver returned the expected resulting state.
func checkExpectedState(out, errOut io.Writer, format output.Format, cmdName, profileName, action, expectedState string, result driver.JobActionResult) error {
	if result.State == expectedState {
		return nil
	}
	return cmderr.WriteDetail(out, errOut, format, cmdName, 1,
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
