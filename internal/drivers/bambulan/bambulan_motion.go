package bambulan

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/polimero-app/cli/internal/driver"
)

// MotionHome homes the printer axes by sending a G28 G-code command and
// waiting for a full status report to confirm the command was accepted.
// If axes is empty or nil, all axes are homed (G28 with no arguments).
func (d *Driver) MotionHome(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, axes []driver.Axis) (driver.MotionResult, error) {
	gcode := buildHomeGcode(axes)
	payload, err := buildGcodeLinePayload(gcode)
	if err != nil {
		return driver.MotionResult{}, err
	}

	data, err := d.mqttCommand(ctx, p, s, payload, isPushallReport)
	if err != nil {
		return driver.MotionResult{}, err
	}

	return motionResultFromReport(data, d)
}

// MotionJog moves the toolhead by a relative delta using G91+G1+G90 G-code and
// waits for a full status report to confirm the command was accepted.
func (d *Driver) MotionJog(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, delta driver.JogDelta) (driver.MotionResult, error) {
	gcode := buildJogGcode(delta)
	payload, err := buildGcodeLinePayload(gcode)
	if err != nil {
		return driver.MotionResult{}, err
	}

	data, err := d.mqttCommand(ctx, p, s, payload, isPushallReport)
	if err != nil {
		return driver.MotionResult{}, err
	}

	return motionResultFromReport(data, d)
}

// buildHomeGcode returns a G28 G-code string for the given axes.
func buildHomeGcode(axes []driver.Axis) string {
	if len(axes) == 0 {
		return "G28\n"
	}
	var sb strings.Builder
	sb.WriteString("G28")
	for _, a := range axes {
		sb.WriteString(" ")
		sb.WriteString(strings.ToUpper(string(a)))
	}
	sb.WriteString("\n")
	return sb.String()
}

// buildJogGcode returns a relative-move G-code sequence (G91 → G1 → G90).
func buildJogGcode(delta driver.JogDelta) string {
	var sb strings.Builder
	sb.WriteString("G91\n")
	sb.WriteString("G1")
	if delta.XMillimeters != nil {
		fmt.Fprintf(&sb, " X%.3f", *delta.XMillimeters)
	}
	if delta.YMillimeters != nil {
		fmt.Fprintf(&sb, " Y%.3f", *delta.YMillimeters)
	}
	if delta.ZMillimeters != nil {
		fmt.Fprintf(&sb, " Z%.3f", *delta.ZMillimeters)
	}
	if delta.FeedrateMmPerMin > 0 {
		fmt.Fprintf(&sb, " F%d", delta.FeedrateMmPerMin)
	}
	sb.WriteString("\n")
	sb.WriteString("G90\n")
	return sb.String()
}

// motionResultFromReport parses a pushall report and returns an accepted result.
func motionResultFromReport(data []byte, d *Driver) (driver.MotionResult, error) {
	status, err := parseReport(data)
	if err != nil {
		return driver.MotionResult{}, err
	}
	warnings := status.Warnings
	if warnings == nil {
		warnings = []driver.StatusWarning{}
	}
	return driver.MotionResult{
		State:        driver.MotionStateAccepted,
		Warnings:     warnings,
		Capabilities: d.Capabilities(),
	}, nil
}
