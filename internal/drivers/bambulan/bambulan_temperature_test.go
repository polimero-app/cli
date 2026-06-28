package bambulan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"log/slog"
)

func float64Ptr(v float64) *float64 { return &v }

func temperaturePushallResponse(nozzleTarget, bedTarget float64) []byte {
	return []byte(fmt.Sprintf(
		`{"print":{"gcode_state":"IDLE","nozzle_temper":25.0,"nozzle_target_temper":%.1f,"bed_temper":22.0,"bed_target_temper":%.1f,"chamber_temper":0,"mc_percent":0,"hms":[]}}`,
		nozzleTarget,
		bedTarget,
	))
}

func TestTemperatureSet_NozzleOnly_PublishesM104(t *testing.T) {
	response := temperaturePushallResponse(200, 60)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	targets := driver.TemperatureTargets{NozzleCelsius: float64Ptr(200)}
	_, err := drv.TemperatureSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), targets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	if len(pubs) < 1 {
		t.Fatal("expected at least 1 publish")
	}
	if !strings.Contains(pubs[0], "M104 S200") {
		t.Errorf("expected M104 S200 in command, got: %s", pubs[0])
	}
	if strings.Contains(pubs[0], "M140") || strings.Contains(pubs[0], "M141") {
		t.Errorf("expected only M104, got: %s", pubs[0])
	}
}

func TestTemperatureSet_BedOnly_PublishesM140(t *testing.T) {
	response := temperaturePushallResponse(200, 60)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	targets := driver.TemperatureTargets{BedCelsius: float64Ptr(60)}
	_, err := drv.TemperatureSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), targets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M140 S60") {
		t.Errorf("expected M140 S60, got: %s", pubs[0])
	}
}

func TestTemperatureSet_ChamberOnly_PublishesM141(t *testing.T) {
	response := temperaturePushallResponse(200, 60)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	targets := driver.TemperatureTargets{ChamberCelsius: float64Ptr(45)}
	_, err := drv.TemperatureSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), targets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M141 S45") {
		t.Errorf("expected M141 S45, got: %s", pubs[0])
	}
}

func TestTemperatureSet_AllTargets_PublishesAllGcodes(t *testing.T) {
	response := temperaturePushallResponse(220, 65)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	targets := driver.TemperatureTargets{
		NozzleCelsius:  float64Ptr(220),
		BedCelsius:     float64Ptr(65),
		ChamberCelsius: float64Ptr(40),
	}
	_, err := drv.TemperatureSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), targets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	cmd := pubs[0]
	if !strings.Contains(cmd, "M104 S220") {
		t.Errorf("expected M104 S220, got: %s", cmd)
	}
	if !strings.Contains(cmd, "M140 S65") {
		t.Errorf("expected M140 S65, got: %s", cmd)
	}
	if !strings.Contains(cmd, "M141 S40") {
		t.Errorf("expected M141 S40, got: %s", cmd)
	}
}

func TestTemperatureSet_EmptyTargets_ReturnsError(t *testing.T) {
	fc := &fakeCommandClient{}
	drv := newCommandDriver(fc)
	targets := driver.TemperatureTargets{} // no targets
	_, err := drv.TemperatureSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), targets)
	if err == nil {
		t.Fatal("expected error for empty targets")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit code 2, got %v", err)
	}
}

func TestTemperatureSet_HappyPath_ReturnsTargetsFromReport(t *testing.T) {
	response := []byte(`{"print":{"gcode_state":"IDLE","nozzle_temper":25.0,"nozzle_target_temper":200.0,"bed_temper":22.0,"bed_target_temper":60.0,"chamber_temper":0,"mc_percent":0,"hms":[]}}`)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	targets := driver.TemperatureTargets{
		NozzleCelsius: float64Ptr(200),
		BedCelsius:    float64Ptr(60),
	}
	result, err := drv.TemperatureSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), targets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Targets.NozzleCelsius == nil || *result.Targets.NozzleCelsius != 200.0 {
		t.Errorf("expected nozzle target 200, got %v", result.Targets.NozzleCelsius)
	}
	if result.Targets.BedCelsius == nil || *result.Targets.BedCelsius != 60.0 {
		t.Errorf("expected bed target 60, got %v", result.Targets.BedCelsius)
	}
}

func TestTemperatureSet_ConnectFails_ReturnsError(t *testing.T) {
	fc := &fakeCommandClient{connectErr: errors.New("refused")}
	drv := newCommandDriver(fc)
	targets := driver.TemperatureTargets{NozzleCelsius: float64Ptr(200)}
	_, err := drv.TemperatureSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), targets)
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

func TestTemperatureSet_ZeroNozzle_SendsM104S0(t *testing.T) {
	// A value of 0 means "turn off the heater"
	response := temperaturePushallResponse(0, 60)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	zero := 0.0
	targets := driver.TemperatureTargets{NozzleCelsius: &zero}
	_, err := drv.TemperatureSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), targets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "M104 S0") {
		t.Errorf("expected M104 S0, got: %s", pubs[0])
	}
}

func TestTemperatureSet_ZeroNozzle_ReturnsAcknowledgedZeroTarget(t *testing.T) {
	response := temperaturePushallResponse(0, 60)
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	zero := 0.0
	targets := driver.TemperatureTargets{NozzleCelsius: &zero}
	result, err := drv.TemperatureSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), targets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Targets.NozzleCelsius == nil || *result.Targets.NozzleCelsius != 0 {
		t.Fatalf("expected acknowledged nozzle target 0, got %v", result.Targets.NozzleCelsius)
	}
}

func TestTemperatureSet_WaitsForRequestedTargets(t *testing.T) {
	oldTargets := temperaturePushallResponse(180, 50)
	newTargets := temperaturePushallResponse(210, 65)
	fc := &fakeCommandClient{responses: [][]byte{oldTargets, newTargets}}
	drv := newCommandDriver(fc)
	targets := driver.TemperatureTargets{
		NozzleCelsius: float64Ptr(210),
		BedCelsius:    float64Ptr(65),
	}
	result, err := drv.TemperatureSet(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), targets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Targets.NozzleCelsius == nil || *result.Targets.NozzleCelsius != 210 {
		t.Errorf("expected nozzle target 210, got %v", result.Targets.NozzleCelsius)
	}
	if result.Targets.BedCelsius == nil || *result.Targets.BedCelsius != 65 {
		t.Errorf("expected bed target 65, got %v", result.Targets.BedCelsius)
	}
}
