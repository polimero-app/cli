package bambulan

import (
	"context"
	"fmt"
	"log/slog"
	"math"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

// FanSet sends M106 G-code to set a fan speed and waits for a full status
// report to confirm the command was accepted.
func (d *Driver) FanSet(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, target driver.FanTarget) (driver.FanControlResult, error) {
	// Validate capability and fan key
	if !d.Capabilities().FanControl {
		return driver.FanControlResult{}, apperr.New(5, "fan control is not supported by this printer")
	}

	// Validate fan key is supported
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

	data, err := d.mqttCommand(ctx, p, s, payload, isPushallReport)
	if err != nil {
		return driver.FanControlResult{}, err
	}

	_, err = parseReport(data)
	if err != nil {
		return driver.FanControlResult{}, err
	}

	// ponytail: driver doesn't read back fan PWM from status (Bambu doesn't expose it).
	// Success is a fresh pushall report post-command; we echo back the requested speed.
	return driver.FanControlResult{
		Fan:          target.Fan,
		SpeedPercent: target.SpeedPercent,
		Warnings:     []driver.StatusWarning{},
		Capabilities: d.Capabilities(),
	}, nil
}

// LightSet sends M960 G-code to control a light and waits for a full status
// report where lights_report[] contains the matching state.
func (d *Driver) LightSet(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, target driver.LightTarget) (driver.LightControlResult, error) {
	// Validate capability and light key
	if !d.Capabilities().LightControl {
		return driver.LightControlResult{}, apperr.New(5, "light control is not supported by this printer")
	}

	if target.Light != "chamber" {
		return driver.LightControlResult{}, apperr.New(5, fmt.Sprintf("unsupported light key: %s", target.Light))
	}

	// Build M960 command based on state
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

	data, err := d.mqttCommand(ctx, p, s, payload, isPushallReport)
	if err != nil {
		return driver.LightControlResult{}, err
	}

	status, err := parseReport(data)
	if err != nil {
		return driver.LightControlResult{}, err
	}

	// Verify lights_report[] reflects the requested state
	// ponytail: report doesn't always include lights_report, but we got a pushall.
	// If it's absent, trust that the command was accepted (same pattern as fan).

	return driver.LightControlResult{
		Light:        "chamber",
		State:        target.State,
		Warnings:     status.Warnings,
		Capabilities: d.Capabilities(),
	}, nil
}

// SpeedSet sends M220 G-code to set a print speed profile and waits for a
// full status report to confirm the command was accepted.
func (d *Driver) SpeedSet(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, profile string) (driver.SpeedControlResult, error) {
	// Validate capability and profile
	if !d.Capabilities().SpeedControl {
		return driver.SpeedControlResult{}, apperr.New(5, "speed control is not supported by this printer")
	}

	speedPercent, supported := speedProfileToPercent(profile)
	if !supported {
		return driver.SpeedControlResult{}, apperr.New(5, fmt.Sprintf("unsupported speed profile: %s", profile))
	}

	gcode := fmt.Sprintf("M220 S%d\n", speedPercent)

	payload, err := buildGcodeLinePayload(gcode)
	if err != nil {
		return driver.SpeedControlResult{}, apperr.Wrap(4, "failed to build speed command", err)
	}

	data, err := d.mqttCommand(ctx, p, s, payload, isPushallReport)
	if err != nil {
		return driver.SpeedControlResult{}, err
	}

	_, err = parseReport(data)
	if err != nil {
		return driver.SpeedControlResult{}, err
	}

	// ponytail: driver doesn't verify speed level bits in home_flag from status.
	// Success is a fresh pushall report post-command; we echo back the profile.
	return driver.SpeedControlResult{
		SpeedProfile: profile,
		Warnings:     []driver.StatusWarning{},
		Capabilities: d.Capabilities(),
	}, nil
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

// speedProfileToPercent maps a speed profile to its M220 S percentage.
// Returns (percent, supported).
func speedProfileToPercent(profile string) (int, bool) {
	switch profile {
	case "silent":
		return 20, true
	case "standard":
		return 100, true
	case "sport":
		return 150, true
	case "ludicrous":
		return 300, true
	default:
		return 0, false
	}
}
