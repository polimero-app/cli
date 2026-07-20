package bambulan

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

// fanAckTolerancePercent absorbs Bambu's fan speed quantization. The printer
// reports fan speeds on a 0-15 gear scale, so a requested percentage echoes
// back rounded to the nearest gear step (~6.7%); half a step plus integer
// rounding is at most 4 points.
const fanAckTolerancePercent = 4

// FanSet sends M106 G-code to set a fan speed and waits for a status report
// that echoes the requested speed on the requested fan.
func (d *Driver) FanSet(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, target driver.FanTarget) (driver.FanControlResult, error) {
	if !d.Capabilities().FanControl {
		return driver.FanControlResult{}, apperr.New(5, "fan control is not supported by this printer")
	}

	fanGcode, supported := fanKeyToGcode(target.Fan)
	if !supported {
		return driver.FanControlResult{}, apperr.New(5, fmt.Sprintf("unsupported fan key: %s", target.Fan))
	}

	// Convert percent to PWM (0-255)
	pwm := int(math.Round(float64(target.SpeedPercent) * 255 / 100))
	gcode := fmt.Sprintf("%s S%d\n", fanGcode, pwm)

	payload, err := buildGcodeLinePayload(gcode)
	if err != nil {
		return driver.FanControlResult{}, apperr.Wrap(4, "failed to build fan command", err)
	}

	// Acknowledgment: a report whose fan reading echoes the requested speed
	// within gear-quantization tolerance. A full report that lacks the fan key
	// means the connected model does not expose that fan; stop and report
	// unsupported instead of waiting for an echo that can never arrive.
	unsupportedOnModel := false
	predicate := func(data []byte) bool {
		status, perr := parseReport(data)
		if perr != nil || status == nil {
			return false
		}
		got, present := status.Fans[target.Fan]
		if present {
			return absInt(got-target.SpeedPercent) <= fanAckTolerancePercent
		}
		if isPushallReport(data) {
			unsupportedOnModel = true
			return true
		}
		return false // delta report without fan fields; keep waiting
	}

	data, err := d.mqttCommand(ctx, p, s, payload, predicate)
	if err != nil {
		return driver.FanControlResult{}, err
	}
	if unsupportedOnModel {
		return driver.FanControlResult{}, apperr.New(5, fmt.Sprintf("fan %q is not available on this printer model", target.Fan))
	}

	status, err := parseReport(data)
	if err != nil {
		return driver.FanControlResult{}, err
	}

	warnings := status.Warnings
	if warnings == nil {
		warnings = []driver.StatusWarning{}
	}

	// The echo is quantized to gear steps, so report the requested percentage
	// once it is confirmed within tolerance.
	return driver.FanControlResult{
		Fan:          target.Fan,
		SpeedPercent: target.SpeedPercent,
		Warnings:     warnings,
		Capabilities: d.Capabilities(),
	}, nil
}

// LightSet sends M960 G-code to control the chamber light and waits for a
// status report where lights_report shows the requested state.
func (d *Driver) LightSet(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, target driver.LightTarget) (driver.LightControlResult, error) {
	if !d.Capabilities().LightControl {
		return driver.LightControlResult{}, apperr.New(5, "light control is not supported by this printer")
	}

	if target.Light != "chamber" {
		return driver.LightControlResult{}, apperr.New(5, fmt.Sprintf("unsupported light key: %s", target.Light))
	}

	var gcode string
	if target.State == driver.LightStateOn {
		gcode = "M960 S1\n"
	} else {
		gcode = "M960 S0\n"
	}

	payload, err := buildGcodeLinePayload(gcode)
	if err != nil {
		return driver.LightControlResult{}, apperr.Wrap(4, "failed to build light command", err)
	}

	// Acknowledgment: a report whose lights_report contains the chamber light
	// in the requested state.
	wantMode := string(target.State)
	predicate := func(data []byte) bool {
		status, perr := parseReport(data)
		if perr != nil || status == nil {
			return false
		}
		return status.Lights["chamber_light"] == wantMode
	}

	data, err := d.mqttCommand(ctx, p, s, payload, predicate)
	if err != nil {
		return driver.LightControlResult{}, err
	}

	status, err := parseReport(data)
	if err != nil {
		return driver.LightControlResult{}, err
	}

	warnings := status.Warnings
	if warnings == nil {
		warnings = []driver.StatusWarning{}
	}

	return driver.LightControlResult{
		Light:        "chamber",
		State:        target.State,
		Warnings:     warnings,
		Capabilities: d.Capabilities(),
	}, nil
}

// SpeedSet sends the Bambu print_speed command to select a speed level and
// waits for a status report where spd_lvl echoes the requested level.
func (d *Driver) SpeedSet(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, profile string) (driver.SpeedControlResult, error) {
	if !d.Capabilities().SpeedControl {
		return driver.SpeedControlResult{}, apperr.New(5, "speed control is not supported by this printer")
	}

	level, supported := speedProfileToLevel(profile)
	if !supported {
		return driver.SpeedControlResult{}, apperr.New(5, fmt.Sprintf("unsupported speed profile: %s", profile))
	}

	payload, err := buildPrintSpeedPayload(level)
	if err != nil {
		return driver.SpeedControlResult{}, apperr.Wrap(4, "failed to build speed command", err)
	}

	// Acknowledgment: a report whose spd_lvl maps back to the requested profile.
	predicate := func(data []byte) bool {
		status, perr := parseReport(data)
		if perr != nil || status == nil {
			return false
		}
		return status.SpeedLevel != nil && *status.SpeedLevel == profile
	}

	data, err := d.mqttCommand(ctx, p, s, payload, predicate)
	if err != nil {
		return driver.SpeedControlResult{}, err
	}

	status, err := parseReport(data)
	if err != nil {
		return driver.SpeedControlResult{}, err
	}

	warnings := status.Warnings
	if warnings == nil {
		warnings = []driver.StatusWarning{}
	}

	return driver.SpeedControlResult{
		SpeedProfile: profile,
		Warnings:     warnings,
		Capabilities: d.Capabilities(),
	}, nil
}

// buildPrintSpeedPayload constructs the Bambu print_speed MQTT payload JSON.
func buildPrintSpeedPayload(level int) (string, error) {
	type printSpeedCmd struct {
		SequenceID string `json:"sequence_id"`
		Command    string `json:"command"`
		Param      string `json:"param"`
	}
	type payload struct {
		Print printSpeedCmd `json:"print"`
	}
	pl := payload{Print: printSpeedCmd{
		SequenceID: nextSequenceID(),
		Command:    "print_speed",
		Param:      strconv.Itoa(level),
	}}
	b, err := json.Marshal(pl)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// fanKeyToGcode maps a canonical fan key to its M106 G-code prefix.
// Returns (gcode, supported).
func fanKeyToGcode(fan string) (string, bool) {
	switch fan {
	case "partCooling":
		return "M106", true
	case "auxiliary":
		return "M106 P2", true
	case "chamber":
		return "M106 P3", true
	default:
		return "", false
	}
}

// speedProfileToLevel maps a portable speed profile to the Bambu print_speed
// level, the inverse of bambuSpeedLevels used for status mapping.
// Returns (level, supported).
func speedProfileToLevel(profile string) (int, bool) {
	for level, name := range bambuSpeedLevels {
		if name == profile {
			return level, true
		}
	}
	return 0, false
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
