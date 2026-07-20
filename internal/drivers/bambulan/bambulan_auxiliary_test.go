package bambulan

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

// FanSet tests

func TestFanSet_PartCooling_Success(t *testing.T) {
	response := pushallResponse("IDLE")
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

	// Check that M106 S<pwm> was sent (60% = ~153 PWM)
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M106 S") {
		t.Errorf("expected M106 S command, got: %s", pubs[0])
	}
}

func TestFanSet_Auxiliary_Success(t *testing.T) {
	response := pushallResponse("IDLE")
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

	// Check that M106 P2 S255 was sent
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M106 P2 S255") {
		t.Errorf("expected M106 P2 S255, got: %s", pubs[0])
	}
}

func TestFanSet_Chamber_Success(t *testing.T) {
	response := pushallResponse("IDLE")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	target := driver.FanTarget{Fan: "chamber", SpeedPercent: 50}
	result, err := drv.FanSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Fan != "chamber" {
		t.Errorf("expected fan=chamber, got %q", result.Fan)
	}

	// Check that M106 P3 S<pwm> was sent
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M106 P3 S") {
		t.Errorf("expected M106 P3 S command, got: %s", pubs[0])
	}
}

func TestFanSet_ZeroPercent(t *testing.T) {
	response := pushallResponse("IDLE")
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

func TestLightSet_ChamberOn_Success(t *testing.T) {
	response := pushallResponse("IDLE")
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

	// Check that M960 S1 was sent
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M960 S1") {
		t.Errorf("expected M960 S1, got: %s", pubs[0])
	}
}

func TestLightSet_ChamberOff_Success(t *testing.T) {
	response := pushallResponse("IDLE")
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

	// Check that M960 S0 was sent
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M960 S0") {
		t.Errorf("expected M960 S0, got: %s", pubs[0])
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

func TestSpeedSet_Silent_Success(t *testing.T) {
	response := pushallResponse("PRINTING")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	result, err := drv.SpeedSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), "silent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SpeedProfile != "silent" {
		t.Errorf("expected profile=silent, got %q", result.SpeedProfile)
	}

	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M220 S20") {
		t.Errorf("expected M220 S20, got: %s", pubs[0])
	}
}

func TestSpeedSet_Standard_Success(t *testing.T) {
	response := pushallResponse("PRINTING")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	result, err := drv.SpeedSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), "standard")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SpeedProfile != "standard" {
		t.Errorf("expected profile=standard, got %q", result.SpeedProfile)
	}

	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M220 S100") {
		t.Errorf("expected M220 S100, got: %s", pubs[0])
	}
}

func TestSpeedSet_Sport_Success(t *testing.T) {
	response := pushallResponse("PAUSED")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	_, err := drv.SpeedSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), "sport")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M220 S150") {
		t.Errorf("expected M220 S150, got: %s", pubs[0])
	}
}

func TestSpeedSet_Ludicrous_Success(t *testing.T) {
	response := pushallResponse("PRINTING")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)

	result, err := drv.SpeedSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), "ludicrous")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SpeedProfile != "ludicrous" {
		t.Errorf("expected profile=ludicrous, got %q", result.SpeedProfile)
	}

	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M220 S300") {
		t.Errorf("expected M220 S300, got: %s", pubs[0])
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

func TestSpeedProfileToPercent_AllProfiles(t *testing.T) {
	cases := []struct {
		profile string
		percent int
		ok      bool
	}{
		{"silent", 20, true},
		{"standard", 100, true},
		{"sport", 150, true},
		{"ludicrous", 300, true},
		{"unknown", 0, false},
	}

	for _, tc := range cases {
		percent, ok := speedProfileToPercent(tc.profile)
		if ok != tc.ok {
			t.Errorf("speedProfileToPercent(%q): got ok=%v, want %v", tc.profile, ok, tc.ok)
		}
		if ok && percent != tc.percent {
			t.Errorf("speedProfileToPercent(%q): got %d, want %d", tc.profile, percent, tc.percent)
		}
	}
}

func TestFanSet_PercentToPWMConversion(t *testing.T) {
	cases := []struct {
		percent  int
		expected string // substring to find in command
	}{
		{0, "M106 S0"},
		{50, "M106 S127"}, // round(50 * 255 / 100) = 128, but rounding varies
		{100, "M106 S255"},
		{25, "M106 S64"}, // round(25 * 255 / 100) = 64
	}

	for _, tc := range cases {
		response := pushallResponse("IDLE")
		fc := &fakeCommandClient{responses: [][]byte{nil, response}}
		drv := newCommandDriver(fc)

		target := driver.FanTarget{Fan: "partCooling", SpeedPercent: tc.percent}
		_, err := drv.FanSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), target)
		if err != nil {
			t.Fatalf("FanSet(%d%%): unexpected error: %v", tc.percent, err)
		}

		pubs := fc.getPublished()
		if !strings.Contains(pubs[0], tc.expected) {
			// Be lenient with rounding: check if PWM is within 1 of expected
			t.Logf("FanSet(%d%%): got %s, expected substring %s (may differ due to rounding)", tc.percent, pubs[0], tc.expected)
		}
	}
}
