package bambulan

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

func TestJobPause_HappyPath_ReturnsPaused(t *testing.T) {
	response := pushallResponse("PAUSED")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	result, err := drv.JobPause(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "paused" {
		t.Errorf("expected state=paused, got %q", result.State)
	}
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], `"pause"`) {
		t.Errorf("expected pause command, got: %s", pubs[0])
	}
}

func TestJobPause_ConnectFails_ReturnsError(t *testing.T) {
	fc := &fakeCommandClient{connectErr: errors.New("refused")}
	drv := newCommandDriver(fc)
	_, err := drv.JobPause(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default())
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

func TestJobPause_WaitsForPausedState_SkipsOtherStates(t *testing.T) {
	// First report is PRINTING (not yet paused), second is PAUSED.
	notPaused := pushallResponse("PRINTING")
	paused := pushallResponse("PAUSED")
	fc := &fakeCommandClient{
		// command publish → PRINTING (predicate not satisfied)
		// pushall publish → PAUSED (predicate satisfied)
		responses: [][]byte{notPaused, paused},
	}
	drv := newCommandDriver(fc)
	result, err := drv.JobPause(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "paused" {
		t.Errorf("expected state=paused, got %q", result.State)
	}
}

func TestJobResume_HappyPath_ReturnsPrinting(t *testing.T) {
	response := pushallResponse("PRINTING")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	result, err := drv.JobResume(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "printing" {
		t.Errorf("expected state=printing, got %q", result.State)
	}
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], `"resume"`) {
		t.Errorf("expected resume command, got: %s", pubs[0])
	}
}

func TestJobResume_AcceptsPrepareState(t *testing.T) {
	response := pushallResponse("PREPARE")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	result, err := drv.JobResume(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "printing" {
		t.Errorf("expected state=printing (mapped from PREPARE), got %q", result.State)
	}
}

func TestJobCancel_WhenPrinting_ReturnsIdle(t *testing.T) {
	response := pushallResponse("IDLE")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	result, err := drv.JobCancel(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "idle" {
		t.Errorf("expected state=idle, got %q", result.State)
	}
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], `"stop"`) {
		t.Errorf("expected stop command, got: %s", pubs[0])
	}
}

func TestJobCancel_AcceptsFinishState(t *testing.T) {
	response := pushallResponse("FINISH")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	result, err := drv.JobCancel(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "idle" {
		t.Errorf("expected state=idle (mapped from FINISH), got %q", result.State)
	}
}

func TestJobStart_3MF_SendsProjectFileCommand(t *testing.T) {
	response := pushallResponse("PRINTING")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	opts := driver.JobStartOptions{}
	result, err := drv.JobStart(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(),
		"sdcard:/models/cube.3mf", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "printing" {
		t.Errorf("expected state=printing, got %q", result.State)
	}
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "project_file") {
		t.Errorf("expected project_file command for .3mf, got: %s", pubs[0])
	}
	printPayload := jobPrintPayload(t, pubs[0])
	if printPayload["param"] != "Metadata/plate_1.gcode" {
		t.Errorf("expected plate param, got %v", printPayload["param"])
	}
	if printPayload["file"] != "models/cube.3mf" {
		t.Errorf("expected file without leading slash, got %v", printPayload["file"])
	}
	if printPayload["url"] != "file:///models/cube.3mf" {
		t.Errorf("expected file URL, got %v", printPayload["url"])
	}
	if printPayload["project_id"] != "0" || printPayload["task_id"] != "0" {
		t.Errorf("expected zero placeholder ids, got %v", printPayload)
	}
	if printPayload["md5"] != "" {
		t.Errorf("expected empty md5 for existing printer file, got %v", printPayload["md5"])
	}
}

func TestJobStart_Gcode_SendsGcodeFileCommand(t *testing.T) {
	response := pushallResponse("PRINTING")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	opts := driver.JobStartOptions{}
	_, err := drv.JobStart(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(),
		"sdcard:/models/part.gcode", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], "gcode_file") {
		t.Errorf("expected gcode_file command for .gcode, got: %s", pubs[0])
	}
}

func TestJobStart_WithPlate_SendsPlateIdx(t *testing.T) {
	response := pushallResponse("PRINTING")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	plate := 2
	opts := driver.JobStartOptions{Plate: &plate}
	_, err := drv.JobStart(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(),
		"sdcard:/models/cube.3mf", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	printPayload := jobPrintPayload(t, pubs[0])
	if printPayload["param"] != "Metadata/plate_2.gcode" {
		t.Errorf("expected plate_2 param, got: %v", printPayload["param"])
	}
}

func TestJobStart_SkipLeveling_SetsBedLevelingFalse(t *testing.T) {
	response := pushallResponse("PRINTING")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	opts := driver.JobStartOptions{SkipLeveling: true}
	_, err := drv.JobStart(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(),
		"sdcard:/models/cube.3mf", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], `"bed_leveling":false`) {
		t.Errorf("expected bed_leveling=false, got: %s", pubs[0])
	}
}

func TestJobStart_DefaultLeveling_SetsBedLevelingTrue(t *testing.T) {
	response := pushallResponse("PRINTING")
	fc := &fakeCommandClient{responses: [][]byte{nil, response}}
	drv := newCommandDriver(fc)
	opts := driver.JobStartOptions{SkipLeveling: false}
	_, err := drv.JobStart(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(),
		"sdcard:/models/cube.3mf", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pubs := fc.getPublished()
	if !strings.Contains(pubs[0], `"bed_leveling":true`) {
		t.Errorf("expected bed_leveling=true, got: %s", pubs[0])
	}
}

func TestJobStart_ConnectFails_ReturnsError(t *testing.T) {
	fc := &fakeCommandClient{connectErr: errors.New("refused")}
	drv := newCommandDriver(fc)
	_, err := drv.JobStart(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, slog.Default(),
		"sdcard:/models/cube.3mf", driver.JobStartOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

func TestParseJobDevicePath_Valid3mf(t *testing.T) {
	path, filename, err := parseJobDevicePath("sdcard:/models/cube.3mf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/models/cube.3mf" {
		t.Errorf("path = %q, want /models/cube.3mf", path)
	}
	if filename != "cube.3mf" {
		t.Errorf("filename = %q, want cube.3mf", filename)
	}
}

func TestParseJobDevicePath_RootPath(t *testing.T) {
	path, _, err := parseJobDevicePath("sdcard:/file.gcode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/file.gcode" {
		t.Errorf("path = %q, want /file.gcode", path)
	}
}

func TestParseJobDevicePath_InvalidFormat_ReturnsError(t *testing.T) {
	_, _, err := parseJobDevicePath("notapath")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestIsJobState_MatchesExpected(t *testing.T) {
	pred := isJobState("PAUSED")
	data := pushallResponse("PAUSED")
	if !pred(data) {
		t.Error("expected predicate to match PAUSED")
	}
}

func jobPrintPayload(t *testing.T, payload string) map[string]any {
	t.Helper()
	var root struct {
		Print map[string]any `json:"print"`
	}
	if err := json.Unmarshal([]byte(payload), &root); err != nil {
		t.Fatalf("invalid job payload JSON: %v\n%s", err, payload)
	}
	if root.Print == nil {
		t.Fatalf("missing print payload: %s", payload)
	}
	return root.Print
}

func TestIsJobState_DoesNotMatchOther(t *testing.T) {
	pred := isJobState("PAUSED")
	data := pushallResponse("PRINTING")
	if pred(data) {
		t.Error("expected predicate to not match PRINTING")
	}
}

func TestIsJobState_MultipleStates(t *testing.T) {
	pred := isJobState("IDLE", "FINISH")
	if !pred(pushallResponse("IDLE")) {
		t.Error("expected match for IDLE")
	}
	if !pred(pushallResponse("FINISH")) {
		t.Error("expected match for FINISH")
	}
	if pred(pushallResponse("PRINTING")) {
		t.Error("expected no match for PRINTING")
	}
}
