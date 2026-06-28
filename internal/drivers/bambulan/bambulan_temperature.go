package bambulan

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

// TemperatureSet sends M104/M140/M141 G-code lines to set heater targets and
// waits for the printer to confirm the new targets in a full status report.
func (d *Driver) TemperatureSet(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, targets driver.TemperatureTargets) (driver.TemperatureResult, error) {
	commandTargets := normalizeTemperatureTargets(targets)
	gcode := buildTemperatureGcode(commandTargets)
	if gcode == "" {
		return driver.TemperatureResult{}, apperr.New(2, "no temperature targets specified")
	}

	payload, err := buildGcodeLinePayload(gcode)
	if err != nil {
		return driver.TemperatureResult{}, apperr.Wrap(4, "failed to build temperature command", err)
	}

	data, err := d.mqttCommand(ctx, p, s, payload, isTemperatureTargetReport(commandTargets))
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

	if status.Temperatures != nil && commandTargets.NozzleCelsius != nil &&
		status.Temperatures.Nozzle != nil && status.Temperatures.Nozzle.TargetCelsius != nil {
		v := *status.Temperatures.Nozzle.TargetCelsius
		result.Targets.NozzleCelsius = &v
	}
	if status.Temperatures != nil && commandTargets.BedCelsius != nil &&
		status.Temperatures.Bed != nil && status.Temperatures.Bed.TargetCelsius != nil {
		v := *status.Temperatures.Bed.TargetCelsius
		result.Targets.BedCelsius = &v
	}
	if commandTargets.ChamberCelsius != nil {
		// Chamber target read-back is not exposed in known status reports.
		v := *commandTargets.ChamberCelsius
		result.Targets.ChamberCelsius = &v
	}

	return result, nil
}

func normalizeTemperatureTargets(targets driver.TemperatureTargets) driver.TemperatureTargets {
	var out driver.TemperatureTargets
	if targets.NozzleCelsius != nil {
		v := math.Round(*targets.NozzleCelsius)
		out.NozzleCelsius = &v
	}
	if targets.BedCelsius != nil {
		v := math.Round(*targets.BedCelsius)
		out.BedCelsius = &v
	}
	if targets.ChamberCelsius != nil {
		v := math.Round(*targets.ChamberCelsius)
		out.ChamberCelsius = &v
	}
	return out
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
		SequenceID: nextSequenceID(),
		Command:    "gcode_line",
		Param:      gcode,
	}}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func isTemperatureTargetReport(targets driver.TemperatureTargets) func([]byte) bool {
	return func(data []byte) bool {
		if !isPushallReport(data) {
			return false
		}
		status, err := parseReport(data)
		if err != nil || status == nil {
			return false
		}
		if targets.NozzleCelsius != nil {
			if status.Temperatures == nil || status.Temperatures.Nozzle == nil ||
				status.Temperatures.Nozzle.TargetCelsius == nil ||
				!temperatureTargetMatches(*status.Temperatures.Nozzle.TargetCelsius, *targets.NozzleCelsius) {
				return false
			}
		}
		if targets.BedCelsius != nil {
			if status.Temperatures == nil || status.Temperatures.Bed == nil ||
				status.Temperatures.Bed.TargetCelsius == nil ||
				!temperatureTargetMatches(*status.Temperatures.Bed.TargetCelsius, *targets.BedCelsius) {
				return false
			}
		}
		return true
	}
}

func temperatureTargetMatches(observed, requested float64) bool {
	return math.Abs(observed-requested) < 0.01
}
