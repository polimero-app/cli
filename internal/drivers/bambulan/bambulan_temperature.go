package bambulan

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

// TemperatureSet sends M104/M140/M141 G-code lines to set heater targets and
// waits for the printer to confirm the new targets in a full status report.
func (d *Driver) TemperatureSet(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, targets driver.TemperatureTargets) (driver.TemperatureResult, error) {
	gcode := buildTemperatureGcode(targets)
	if gcode == "" {
		return driver.TemperatureResult{}, apperr.New(2, "no temperature targets specified")
	}

	payload, err := buildGcodeLinePayload(gcode)
	if err != nil {
		return driver.TemperatureResult{}, apperr.Wrap(4, "failed to build temperature command", err)
	}

	data, err := d.mqttCommand(ctx, p, s, payload, isPushallReport)
	if err != nil {
		return driver.TemperatureResult{}, err
	}

	status, err := parseReport(data)
	if err != nil {
		return driver.TemperatureResult{}, err
	}

	warnings := status.Warnings
	if warnings == nil {
		warnings = []driver.StatusWarning{}
	}

	result := driver.TemperatureResult{
		Warnings:     warnings,
		Capabilities: d.Capabilities(),
	}

	if status.Temperatures != nil {
		if status.Temperatures.Nozzle != nil && status.Temperatures.Nozzle.TargetCelsius != nil {
			v := *status.Temperatures.Nozzle.TargetCelsius
			result.Targets.NozzleCelsius = &v
		}
		if status.Temperatures.Bed != nil && status.Temperatures.Bed.TargetCelsius != nil {
			v := *status.Temperatures.Bed.TargetCelsius
			result.Targets.BedCelsius = &v
		}
		if status.Temperatures.Chamber != nil {
			// Chamber has no target in the status report; echo what was requested.
			result.Targets.ChamberCelsius = targets.ChamberCelsius
		}
	}

	return result, nil
}

// buildTemperatureGcode converts TemperatureTargets to G-code lines.
// A nil pointer means "leave that heater unchanged".
// A value of 0 means "turn the heater off".
func buildTemperatureGcode(targets driver.TemperatureTargets) string {
	var sb strings.Builder
	if targets.NozzleCelsius != nil {
		fmt.Fprintf(&sb, "M104 S%.0f\n", *targets.NozzleCelsius)
	}
	if targets.BedCelsius != nil {
		fmt.Fprintf(&sb, "M140 S%.0f\n", *targets.BedCelsius)
	}
	if targets.ChamberCelsius != nil {
		fmt.Fprintf(&sb, "M141 S%.0f\n", *targets.ChamberCelsius)
	}
	return sb.String()
}

// buildGcodeLinePayload constructs the Bambu gcode_line MQTT payload JSON.
func buildGcodeLinePayload(gcode string) (string, error) {
	type gcodeLineCmd struct {
		SequenceID string `json:"sequence_id"`
		Command    string `json:"command"`
		Param      string `json:"param"`
	}
	type payload struct {
		Print gcodeLineCmd `json:"print"`
	}
	p := payload{Print: gcodeLineCmd{
		SequenceID: "1",
		Command:    "gcode_line",
		Param:      gcode,
	}}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
