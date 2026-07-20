package bambulan

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

// auxPushall returns a pushall-style report with extra fields spliced into the
// print object, e.g. `"cooling_fan_speed":"9"`.
func auxPushall(gcodeState, extraFields string) []byte {
	fields := `"gcode_state":"` + gcodeState + `","hms":[]`
	if extraFields != "" {
		fields += "," + extraFields
	}
	return []byte(`{"print":{` + fields + `}}`)
}

func shortCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

// FanSet tests

func TestFanSet_PartCooling_EchoConfirms(t *testing.T) {
	// gear 9 -> 9*100/15 = 60%
	response := auxPushall("IDLE", `"cooling_fan_speed":"9"`)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	target := driver.FanTarget{Fan: "partCooling", SpeedPercent: 60}
	result, err := drv.FanSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Fan != "partCooling" {
		t.Errorf("expected fan=partCooling, got %q", result.Fan)
	}
	if result.SpeedPercent != 60 {
		t.Errorf("expected speedPercent=60, got %d", result.SpeedPercent)
	}

	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M106 S153") {
		t.Errorf("expected M106 S153 command, got: %s", pubs[0])
	}
}

func TestFanSet_Auxiliary_EchoConfirms(t *testing.T) {
	response := auxPushall("IDLE", `"big_fan1_speed":"15"`)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	target := driver.FanTarget{Fan: "auxiliary", SpeedPercent: 100}
	result, err := drv.FanSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Fan != "auxiliary" {
		t.Errorf("expected fan=auxiliary, got %q", result.Fan)
	}

	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M106 P2 S255") {
		t.Errorf("expected M106 P2 S255, got: %s", pubs[0])
	}
}

func TestFanSet_Chamber_EchoWithinGearTolerance(t *testing.T) {
	// Request 50%; gear 8 echoes as 8*100/15 = 53%, within tolerance.
	response := auxPushall("IDLE", `"big_fan2_speed":"8"`)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	target := driver.FanTarget{Fan: "chamber", SpeedPercent: 50}
	result, err := drv.FanSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SpeedPercent != 50 {
		t.Errorf("expected requested speedPercent=50 reported, got %d", result.SpeedPercent)
	}

	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M106 P3 S") {
		t.Errorf("expected M106 P3 S command, got: %s", pubs[0])
	}
}

func TestFanSet_ZeroPercent(t *testing.T) {
	response := auxPushall("IDLE", `"cooling_fan_speed":"0"`)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	target := driver.FanTarget{Fan: "partCooling", SpeedPercent: 0}
	result, err := drv.FanSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SpeedPercent != 0 {
		t.Errorf("expected speedPercent=0, got %d", result.SpeedPercent)
	}

	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M106 S0") {
		t.Errorf("expected M106 S0, got: %s", pubs[0])
	}
}

func TestFanSet_DeltaReportEchoConfirms(t *testing.T) {
	// Delta report (no gcode_state) carrying the fan echo must be accepted:
	// P1/A1 printers push value changes as deltas.
	delta := []byte(`{"print":{"cooling_fan_speed":"9"}}`)
	fc := &fakeCommandClient{responses: [][]byte{nil, delta}}
	drv := newCommandDriver(fc)

	target := driver.FanTarget{Fan: "partCooling", SpeedPercent: 60}
	result, err := drv.FanSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SpeedPercent != 60 {
		t.Errorf("expected speedPercent=60, got %d", result.SpeedPercent)
	}
}

func TestFanSet_FanMissingFromFullReport_Unsupported(t *testing.T) {
	// A full report without the requested fan key means the model does not
	// expose that fan: exit code 5, not a timeout.
	response := auxPushall("IDLE", `"cooling_fan_speed":"0"`)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	target := driver.FanTarget{Fan: "chamber", SpeedPercent: 50}
	_, err := drv.FanSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err == nil {
		t.Fatal("expected error for fan unavailable on model")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Errorf("expected exit code 5, got %v", err)
	}
}

func TestFanSet_NoEcho_TimesOut(t *testing.T) {
	// Fan present but stuck at the old speed: no acknowledgment, exit code 4.
	response := auxPushall("IDLE", `"cooling_fan_speed":"0"`)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	target := driver.FanTarget{Fan: "partCooling", SpeedPercent: 60}
	_, err := drv.FanSet(shortCtx(t), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err == nil {
		t.Fatal("expected timeout error")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

func TestFanSet_UnsupportedFan(t *testing.T) {
	fc := &fakeCommandClient{}
	drv := newCommandDriver(fc)

	target := driver.FanTarget{Fan: "unknown", SpeedPercent: 50}
	_, err := drv.FanSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err == nil {
		t.Fatal("expected error for unsupported fan")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Errorf("expected exit code 5, got %v", err)
	}
}

func TestFanSet_ConnectFails(t *testing.T) {
	fc := &fakeCommandClient{connectErr: errors.New("connection refused")}
	drv := newCommandDriver(fc)

	target := driver.FanTarget{Fan: "partCooling", SpeedPercent: 50}
	_, err := drv.FanSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err == nil {
		t.Fatal("expected error")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

// LightSet tests

func TestLightSet_ChamberOn_EchoConfirms(t *testing.T) {
	response := auxPushall("IDLE", `"lights_report":[{"node":"chamber_light","mode":"on"}]`)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	target := driver.LightTarget{Light: "chamber", State: driver.LightStateOn}
	result, err := drv.LightSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Light != "chamber" {
		t.Errorf("expected light=chamber, got %q", result.Light)
	}
	if result.State != driver.LightStateOn {
		t.Errorf("expected state=on, got %q", result.State)
	}

	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M960 S1") {
		t.Errorf("expected M960 S1, got: %s", pubs[0])
	}
}

func TestLightSet_ChamberOff_EchoConfirms(t *testing.T) {
	response := auxPushall("IDLE", `"lights_report":[{"node":"chamber_light","mode":"off"}]`)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	target := driver.LightTarget{Light: "chamber", State: driver.LightStateOff}
	result, err := drv.LightSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.State != driver.LightStateOff {
		t.Errorf("expected state=off, got %q", result.State)
	}

	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M960 S0") {
		t.Errorf("expected M960 S0, got: %s", pubs[0])
	}
}

func TestLightSet_WrongStateEcho_TimesOut(t *testing.T) {
	// Report shows the opposite state: no acknowledgment, exit code 4.
	response := auxPushall("IDLE", `"lights_report":[{"node":"chamber_light","mode":"off"}]`)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	target := driver.LightTarget{Light: "chamber", State: driver.LightStateOn}
	_, err := drv.LightSet(shortCtx(t), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err == nil {
		t.Fatal("expected timeout error")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

func TestLightSet_UnsupportedLight(t *testing.T) {
	fc := &fakeCommandClient{}
	drv := newCommandDriver(fc)

	target := driver.LightTarget{Light: "unknown", State: driver.LightStateOn}
	_, err := drv.LightSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err == nil {
		t.Fatal("expected error for unsupported light")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Errorf("expected exit code 5, got %v", err)
	}
}

func TestLightSet_ConnectFails(t *testing.T) {
	fc := &fakeCommandClient{connectErr: errors.New("connection refused")}
	drv := newCommandDriver(fc)

	target := driver.LightTarget{Light: "chamber", State: driver.LightStateOn}
	_, err := drv.LightSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err == nil {
		t.Fatal("expected error")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

// SpeedSet tests

func TestSpeedSet_AllProfiles_EchoConfirms(t *testing.T) {
	cases := []struct {
		profile string
		level   string
	}{
		{"silent", "1"},
		{"standard", "2"},
		{"sport", "3"},
		{"ludicrous", "4"},
	}

	for _, tc := range cases {
		response := auxPushall("PRINTING", `"spd_lvl":"`+tc.level+`"`)
		fc := &fakeCommandClient{responses: [][]byte{nil, response}}
		drv := newCommandDriver(fc)

		result, err := drv.SpeedSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), tc.profile)
		if err != nil {
			t.Fatalf("SpeedSet(%s): unexpected error: %v", tc.profile, err)
		}

		if result.SpeedProfile != tc.profile {
			t.Errorf("SpeedSet(%s): got profile %q", tc.profile, result.SpeedProfile)
		}

		pubs := fc.getPublished()
		if !strings.Contains(pubs[0], `"command":"print_speed"`) {
			t.Errorf("SpeedSet(%s): expected print_speed command, got: %s", tc.profile, pubs[0])
		}
		if !strings.Contains(pubs[0], `"param":"`+tc.level+`"`) {
			t.Errorf("SpeedSet(%s): expected param %s, got: %s", tc.profile, tc.level, pubs[0])
		}
	}
}

func TestSpeedSet_WrongLevelEcho_TimesOut(t *testing.T) {
	response := auxPushall("PRINTING", `"spd_lvl":"2"`)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	_, err := drv.SpeedSet(shortCtx(t), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), "sport")
	if err == nil {
		t.Fatal("expected timeout error")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

func TestSpeedSet_UnsupportedProfile(t *testing.T) {
	fc := &fakeCommandClient{}
	drv := newCommandDriver(fc)

	_, err := drv.SpeedSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), "unknown")
	if err == nil {
		t.Fatal("expected error for unsupported profile")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Errorf("expected exit code 5, got %v", err)
	}
}

func TestSpeedSet_ConnectFails(t *testing.T) {
	fc := &fakeCommandClient{connectErr: errors.New("connection refused")}
	drv := newCommandDriver(fc)

	_, err := drv.SpeedSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), "standard")
	if err == nil {
		t.Fatal("expected error")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

// Helper function tests

func TestFanKeyToGcode_AllKeys(t *testing.T) {
	cases := []struct {
		key   string
		gcode string
		ok    bool
	}{
		{"partCooling", "M106", true},
		{"auxiliary", "M106 P2", true},
		{"chamber", "M106 P3", true},
		{"unknown", "", false},
	}

	for _, tc := range cases {
		gcode, ok := fanKeyToGcode(tc.key)
		if ok != tc.ok {
			t.Errorf("fanKeyToGcode(%q): got ok=%v, want %v", tc.key, ok, tc.ok)
		}
		if ok && gcode != tc.gcode {
			t.Errorf("fanKeyToGcode(%q): got %q, want %q", tc.key, gcode, tc.gcode)
		}
	}
}

func TestSpeedProfileToLevel_AllProfiles(t *testing.T) {
	cases := []struct {
		profile string
		level   int
		ok      bool
	}{
		{"silent", 1, true},
		{"standard", 2, true},
		{"sport", 3, true},
		{"ludicrous", 4, true},
		{"unknown", 0, false},
	}

	for _, tc := range cases {
		level, ok := speedProfileToLevel(tc.profile)
		if ok != tc.ok {
			t.Errorf("speedProfileToLevel(%q): got ok=%v, want %v", tc.profile, ok, tc.ok)
		}
		if ok && level != tc.level {
			t.Errorf("speedProfileToLevel(%q): got %d, want %d", tc.profile, level, tc.level)
		}
	}
}

func TestFanSet_PercentToPWMConversion(t *testing.T) {
	cases := []struct {
		percent  int
		echoGear string // printer-side gear echo: round(percent*15/100)
		expected string // substring to find in command
	}{
		{0, "0", "M106 S0"},
		{50, "8", "M106 S128"},
		{100, "15", "M106 S255"},
		{25, "4", "M106 S64"},
	}

	for _, tc := range cases {
		response := auxPushall("IDLE", `"cooling_fan_speed":"`+tc.echoGear+`"`)
		fc := &fakeCommandClient{responses: [][]byte{nil, response}}
		drv := newCommandDriver(fc)

		target := driver.FanTarget{Fan: "partCooling", SpeedPercent: tc.percent}
		_, err := drv.FanSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
		if err != nil {
			t.Fatalf("FanSet(%d%%): unexpected error: %v", tc.percent, err)
		}

		pubs := fc.getPublished()
		if !strings.Contains(pubs[0], tc.expected) {
			t.Errorf("FanSet(%d%%): got %s, expected substring %s", tc.percent, pubs[0], tc.expected)
		}
	}
}
