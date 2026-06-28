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

func TestMotionHome_NoAxes_SendsG28(t *testing.T) {
	response := pushallResponse("IDLE")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	_, err := drv.MotionHome(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	// The G-code param should be G28 (all axes)
	if !strings.Contains(pubs[0], "G28") {
		t.Errorf("expected G28 in command, got: %s", pubs[0])
	}
	// Should NOT have axis specifiers when none provided
	if strings.Contains(pubs[0], "G28 X") || strings.Contains(pubs[0], "G28 Y") || strings.Contains(pubs[0], "G28 Z") {
		t.Errorf("expected plain G28 (all axes), got: %s", pubs[0])
	}
}

func TestMotionHome_XAxisOnly_SendsG28X(t *testing.T) {
	response := pushallResponse("IDLE")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	_, err := drv.MotionHome(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), []driver.Axis{driver.AxisX})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "G28 X") {
		t.Errorf("expected G28 X, got: %s", pubs[0])
	}
	if strings.Contains(pubs[0], "G28 X Y") || strings.Contains(pubs[0], "G28 X Z") {
		t.Errorf("expected only X axis, got: %s", pubs[0])
	}
}

func TestMotionHome_YZAxes_SendsG28YZ(t *testing.T) {
	response := pushallResponse("IDLE")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	_, err := drv.MotionHome(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), []driver.Axis{driver.AxisY, driver.AxisZ})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "G28 Y") || !strings.Contains(pubs[0], "Z") {
		t.Errorf("expected G28 Y Z, got: %s", pubs[0])
	}
}

func TestMotionHome_HappyPath_ReturnsResult(t *testing.T) {
	response := pushallResponse("IDLE")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	result, err := drv.MotionHome(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Warnings == nil {
		t.Error("Warnings must be non-nil slice")
	}
	if result.State != driver.MotionStateAccepted {
		t.Errorf("expected state=accepted, got %q", result.State)
	}
}

func TestMotionHome_ConnectFails_ReturnsError(t *testing.T) {
	fc := &fakeCommandClient{connectErr: errors.New("refused")}
	drv := newCommandDriver(fc)
	_, err := drv.MotionHome(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

func TestMotionJog_XAxisOnly_SendsG91G1G90(t *testing.T) {
	response := pushallResponse("IDLE")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	x := 5.0
	delta := driver.JogDelta{XMillimeters: &x, FeedrateMmPerMin: 3000}
	_, err := drv.MotionJog(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), delta)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	cmd := pubs[0]
	if !strings.Contains(cmd, "G91") {
		t.Errorf("expected G91 (relative mode), got: %s", cmd)
	}
	if !strings.Contains(cmd, "G1") {
		t.Errorf("expected G1 move, got: %s", cmd)
	}
	if !strings.Contains(cmd, "X5") {
		t.Errorf("expected X5 in G1 command, got: %s", cmd)
	}
	if !strings.Contains(cmd, "F3000") {
		t.Errorf("expected F3000 feedrate, got: %s", cmd)
	}
	if !strings.Contains(cmd, "G90") {
		t.Errorf("expected G90 (absolute mode restore), got: %s", cmd)
	}
}

func TestMotionJog_MultiAxis_IncludesAllDeltas(t *testing.T) {
	response := pushallResponse("IDLE")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	x, y, z := 1.0, -2.5, 0.1
	delta := driver.JogDelta{XMillimeters: &x, YMillimeters: &y, ZMillimeters: &z, FeedrateMmPerMin: 1500}
	_, err := drv.MotionJog(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), delta)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	cmd := pubs[0]
	if !strings.Contains(cmd, "X1") {
		t.Errorf("expected X component, got: %s", cmd)
	}
	if !strings.Contains(cmd, "Y-2") {
		t.Errorf("expected Y-2.5 component, got: %s", cmd)
	}
	if !strings.Contains(cmd, "Z0") {
		t.Errorf("expected Z component, got: %s", cmd)
	}
}

func TestMotionJog_HappyPath_ReturnsResult(t *testing.T) {
	response := pushallResponse("IDLE")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	x := 1.0
	delta := driver.JogDelta{XMillimeters: &x, FeedrateMmPerMin: 3000}
	result, err := drv.MotionJog(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), delta)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Warnings == nil {
		t.Error("Warnings must be non-nil slice")
	}
	if result.State != driver.MotionStateAccepted {
		t.Errorf("expected state=accepted, got %q", result.State)
	}
}

func TestMotionJog_ConnectFails_ReturnsError(t *testing.T) {
	fc := &fakeCommandClient{connectErr: errors.New("refused")}
	drv := newCommandDriver(fc)
	x := 5.0
	delta := driver.JogDelta{XMillimeters: &x, FeedrateMmPerMin: 3000}
	_, err := drv.MotionJog(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(), delta)
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}
