package bambulan

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/eclipse/paho.mqtt.golang/packets"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

const (
	testFingerprintA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testFingerprintB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// makeSelfSignedCert generates a throwaway self-signed cert for testing.
func makeSelfSignedCert(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return cert
}

func TestMapState(t *testing.T) {
	cases := []struct {
		gcodeState string
		want       string
	}{
		{"IDLE", "idle"},
		{"FINISH", "idle"},
		{"PRINTING", "printing"},
		{"PREPARE", "printing"},
		{"RUNNING", "printing"},
		{"SLICING", "printing"},
		{"PAUSED", "paused"},
		{"FAILED", "error"},
		{"", "unknown"},
		{"UNKNOWN_STATE", "unknown"},
	}
	for _, c := range cases {
		got := mapState(c.gcodeState)
		if got != c.want {
			t.Errorf("mapState(%q) = %q, want %q", c.gcodeState, got, c.want)
		}
	}
}

func TestBuildTLSConfig_Insecure_NoVerifyConnection(t *testing.T) {
	cfg, err := buildTLSConfig("SN001", testFingerprintA, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VerifyConnection != nil {
		t.Error("VerifyConnection should be nil for insecure mode")
	}
	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true")
	}
}

func TestBuildTLSConfig_EmptyFingerprint_ReturnsError(t *testing.T) {
	_, err := buildTLSConfig("SN001", "", false)
	if err == nil {
		t.Fatal("expected error for empty secure fingerprint")
	}
}

func TestBuildTLSConfig_Mismatch_ReturnsFingerprintMismatchError(t *testing.T) {
	cfg, err := buildTLSConfig("SN001", testFingerprintA, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cert := makeSelfSignedCert(t)
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}

	verifyErr := cfg.VerifyConnection(cs)
	if verifyErr == nil {
		t.Fatal("expected fingerprint mismatch error, got nil")
	}
	var fpErr *fingerprintMismatchError
	if !errors.As(verifyErr, &fpErr) {
		t.Fatalf("expected *fingerprintMismatchError, got %T: %v", verifyErr, verifyErr)
	}
	if fpErr.want != testFingerprintA {
		t.Errorf("want = %q, expected %s", fpErr.want, testFingerprintA)
	}
}

func TestBuildTLSConfig_Match_ReturnsNil(t *testing.T) {
	cert := makeSelfSignedCert(t)
	sum := sha256.Sum256(cert.Raw)
	fp := "sha256:" + hex.EncodeToString(sum[:])

	cfg, _ := buildTLSConfig("SN001", fp, false)
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}

	if err := cfg.VerifyConnection(cs); err != nil {
		t.Errorf("unexpected error for matching fingerprint: %v", err)
	}
}

func TestParseReport_PrintingState(t *testing.T) {
	data := []byte(`{"print":{
        "gcode_state":"PRINTING",
        "nozzle_temper":215.5,"nozzle_target_temper":220.0,
        "bed_temper":60.0,"bed_target_temper":60.0,
        "chamber_temper":35.0,
        "subtask_name":"bracket.3mf",
        "mc_percent":42,"mc_layer_num":10,"total_layer_num":50,
        "hms":[]
    }}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.State != "printing" {
		t.Errorf("State = %q, want printing", result.State)
	}
	if result.Job == nil || result.Job.Name != "bracket.3mf" {
		t.Errorf("Job = %v, want bracket.3mf", result.Job)
	}
	if result.Progress == nil || result.Progress.Percent != 42 {
		t.Errorf("Progress.Percent = %v, want 42", result.Progress)
	}
	if result.Progress.CurrentLayer == nil || *result.Progress.CurrentLayer != 10 {
		t.Errorf("Progress.CurrentLayer = %v, want 10", result.Progress.CurrentLayer)
	}
	if result.Progress.TotalLayers == nil || *result.Progress.TotalLayers != 50 {
		t.Errorf("Progress.TotalLayers = %v, want 50", result.Progress.TotalLayers)
	}
	if result.Temperatures == nil || result.Temperatures.Nozzle == nil {
		t.Fatal("expected nozzle temperature")
	}
	if result.Temperatures.Nozzle.CurrentCelsius != 215.5 {
		t.Errorf("NozzleTemp = %v, want 215.5", result.Temperatures.Nozzle.CurrentCelsius)
	}
	if result.Temperatures.Nozzle.TargetCelsius == nil || *result.Temperatures.Nozzle.TargetCelsius != 220.0 {
		t.Errorf("NozzleTarget = %v, want 220.0", result.Temperatures.Nozzle.TargetCelsius)
	}
	if result.Temperatures.Chamber == nil || result.Temperatures.Chamber.CurrentCelsius != 35.0 {
		t.Errorf("ChamberTemp = %v, want 35.0", result.Temperatures.Chamber)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want []", result.Errors)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("Warnings = %v, want []", result.Warnings)
	}
}

func TestParseReport_IdleState_NoJob_NoChamber(t *testing.T) {
	data := []byte(`{"print":{
        "gcode_state":"IDLE",
        "nozzle_temper":24.5,"nozzle_target_temper":0,
        "bed_temper":23.0,"bed_target_temper":0,
        "chamber_temper":0,
        "subtask_name":"",
        "mc_percent":0,"mc_layer_num":0,"total_layer_num":0,
        "hms":[]
    }}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "idle" {
		t.Errorf("State = %q, want idle", result.State)
	}
	if result.Job != nil {
		t.Errorf("Job = %v, want nil (no active job)", result.Job)
	}
	if result.Temperatures != nil && result.Temperatures.Chamber != nil {
		t.Errorf("Chamber should be nil when chamber_temper=0, got %v", result.Temperatures.Chamber)
	}
	if result.Temperatures == nil || result.Temperatures.Nozzle == nil ||
		result.Temperatures.Nozzle.TargetCelsius == nil || *result.Temperatures.Nozzle.TargetCelsius != 0 {
		t.Errorf("NozzleTarget = %v, want 0", result.Temperatures)
	}
	if result.Temperatures == nil || result.Temperatures.Bed == nil ||
		result.Temperatures.Bed.TargetCelsius == nil || *result.Temperatures.Bed.TargetCelsius != 0 {
		t.Errorf("BedTarget = %v, want 0", result.Temperatures)
	}
	// Errors and Warnings must be empty slices, never nil
	if result.Errors == nil {
		t.Error("Errors must be non-nil slice")
	}
	if result.Warnings == nil {
		t.Error("Warnings must be non-nil slice")
	}
}

func TestParseReport_HMSErrors(t *testing.T) {
	data := []byte(`{"print":{
        "gcode_state":"FAILED",
        "nozzle_temper":24.5,"nozzle_target_temper":0,
        "bed_temper":23.0,"bed_target_temper":0,
        "chamber_temper":0,"subtask_name":"",
        "mc_percent":0,"mc_layer_num":0,"total_layer_num":0,
        "hms":[{"attr":1,"code":2},{"attr":0,"code":0}]
    }}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "error" {
		t.Errorf("State = %q, want error", result.State)
	}
	// attr=0,code=0 is filtered out; only attr=1,code=2 counts
	if len(result.Errors) != 1 {
		t.Fatalf("len(Errors) = %d, want 1", len(result.Errors))
	}
	if result.Errors[0].Code != "hms:00000001:00000002" {
		t.Errorf("Errors[0].Code = %q, want hms:00000001:00000002", result.Errors[0].Code)
	}
}

func TestParseReport_FallsBackToGcodeFile(t *testing.T) {
	data := []byte(`{"print":{
        "gcode_state":"PRINTING",
        "gcode_file":"fallback.3mf",
        "mc_percent":5,
        "nozzle_temper":200.0,
        "bed_temper":55.0,
        "chamber_temper":0,
        "hms":[]
    }}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Job == nil || result.Job.Name != "fallback.3mf" {
		t.Fatalf("Job = %v, want fallback.3mf", result.Job)
	}
}

func TestParseReport_PrinterErrorCode(t *testing.T) {
	data := []byte(`{"print":{
        "gcode_state":"FAILED",
        "mc_percent":0,
        "mc_print_error_code":"12345",
        "hms":[]
    }}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("len(Errors) = %d, want 1", len(result.Errors))
	}
	if result.Errors[0].Code != "printer_error" {
		t.Errorf("Errors[0].Code = %q, want printer_error", result.Errors[0].Code)
	}
	if result.Errors[0].Message != "printer error: 12345" {
		t.Errorf("Errors[0].Message = %q", result.Errors[0].Message)
	}
}

func TestParseReport_MissingOptionalFieldsReturnNullsAndWarnings(t *testing.T) {
	data := []byte(`{"print":{"gcode_state":"PRINTING","hms":[]}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Temperatures != nil {
		t.Fatalf("Temperatures = %v, want nil", result.Temperatures)
	}
	if result.Progress != nil {
		t.Fatalf("Progress = %v, want nil", result.Progress)
	}
	if len(result.Warnings) != 2 {
		t.Fatalf("len(Warnings) = %d, want 2: %v", len(result.Warnings), result.Warnings)
	}
	wantCodes := map[string]bool{
		"temperature_data_unavailable": false,
		"progress_unavailable":         false,
	}
	for _, warning := range result.Warnings {
		if _, ok := wantCodes[warning.Code]; ok {
			wantCodes[warning.Code] = true
		}
	}
	for code, seen := range wantCodes {
		if !seen {
			t.Errorf("missing warning code %q in %v", code, result.Warnings)
		}
	}
}

func TestParseReport_LayerNumPreferredOverLegacyMcLayerNum(t *testing.T) {
	data := []byte(`{"print":{
        "gcode_state":"PRINTING",
        "mc_percent":50,
        "layer_num":7,
        "mc_layer_num":3,
        "total_layer_num":12,
        "nozzle_temper":200.0,
        "bed_temper":55.0,
        "chamber_temper":0,
        "hms":[]
    }}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Progress == nil || result.Progress.CurrentLayer == nil {
		t.Fatalf("CurrentLayer missing in progress: %v", result.Progress)
	}
	if *result.Progress.CurrentLayer != 7 {
		t.Errorf("CurrentLayer = %d, want 7", *result.Progress.CurrentLayer)
	}
}

func TestParseReport_ExtendedFields_Fans(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"PRINTING",
		"nozzle_temper":215.0,"nozzle_target_temper":220.0,
		"bed_temper":60.0,"bed_target_temper":60.0,
		"chamber_temper":35.0,
		"subtask_name":"test.3mf",
		"mc_percent":50,"layer_num":10,"total_layer_num":100,
		"hms":[],
		"cooling_fan_speed":"15",
		"heatbreak_fan_speed":"10",
		"big_fan1_speed":"7",
		"big_fan2_speed":"4"
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Fans == nil {
		t.Fatal("expected fans to be set")
	}
	if result.Fans["partCooling"] != 100 {
		t.Errorf("partCooling = %d, want 100", result.Fans["partCooling"])
	}
	if result.Fans["heatbreak"] != 66 {
		t.Errorf("heatbreak = %d, want 66", result.Fans["heatbreak"])
	}
	if result.Fans["auxiliary"] != 46 {
		t.Errorf("auxiliary = %d, want 46", result.Fans["auxiliary"])
	}
	if result.Fans["chamber"] != 26 {
		t.Errorf("chamber = %d, want 26", result.Fans["chamber"])
	}
}

func TestParseReport_ExtendedFields_TimeAndSpeed(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"PRINTING",
		"nozzle_temper":215.0,"nozzle_target_temper":220.0,
		"bed_temper":60.0,"bed_target_temper":60.0,
		"mc_percent":50,"layer_num":10,"total_layer_num":100,
		"hms":[],
		"mc_remaining_time":90,
		"spd_lvl":2
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TimeEstimates == nil {
		t.Fatal("expected time estimates")
	}
	if result.TimeEstimates.RemainingSeconds == nil || *result.TimeEstimates.RemainingSeconds != 5400 {
		t.Errorf("RemainingSeconds = %v, want 5400", result.TimeEstimates.RemainingSeconds)
	}
	if result.SpeedLevel == nil || *result.SpeedLevel != "standard" {
		t.Errorf("SpeedLevel = %v, want standard", result.SpeedLevel)
	}
}

func TestParseReport_ExtendedFields_WifiAndLights(t *testing.T) {
	data := []byte(`{
		"print":{
			"gcode_state":"IDLE",
			"nozzle_temper":24.0,"nozzle_target_temper":0,
			"bed_temper":23.0,"bed_target_temper":0,
			"mc_percent":0,
			"hms":[],
			"wifi_signal":"-45"
		},
		"lights_report":[{"node":"chamber_light","mode":"on"}]
	}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Wifi == nil {
		t.Fatal("expected wifi data")
	}
	if result.Wifi.SignalDbm != -45 {
		t.Errorf("SignalDbm = %d, want -45", result.Wifi.SignalDbm)
	}
	if result.Lights == nil {
		t.Fatal("expected lights data")
	}
	if result.Lights["chamber_light"] != "on" {
		t.Errorf("chamber_light = %q, want on", result.Lights["chamber_light"])
	}
}

func TestParseReport_ExtendedFields_Stage(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"PRINTING",
		"nozzle_temper":215.0,"nozzle_target_temper":220.0,
		"bed_temper":60.0,"bed_target_temper":60.0,
		"mc_percent":50,"layer_num":10,"total_layer_num":100,
		"hms":[],
		"stg_cur":2
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Stage == nil || *result.Stage != "heatbed_preheating" {
		t.Errorf("Stage = %v, want heatbed_preheating", result.Stage)
	}
}

func TestParseReport_ExtendedFields_Timelapse(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"PRINTING",
		"nozzle_temper":215.0,"nozzle_target_temper":220.0,
		"bed_temper":60.0,"bed_target_temper":60.0,
		"mc_percent":50,"layer_num":10,"total_layer_num":100,
		"hms":[],
		"ipcam_record_timelapse":"enable"
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Timelapse == nil {
		t.Fatal("expected timelapse data")
	}
	if !result.Timelapse.Recording {
		t.Error("expected timelapse recording to be true")
	}
}

func TestParseReport_ExtendedFields_GcodePosition(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"PRINTING",
		"nozzle_temper":215.0,"nozzle_target_temper":220.0,
		"bed_temper":60.0,"bed_target_temper":60.0,
		"mc_percent":50,"layer_num":10,"total_layer_num":100,
		"hms":[],
		"cur_line_num":"48201",
		"total_line_num":"112400"
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.GcodePosition == nil {
		t.Fatal("expected gcode position data")
	}
	if result.GcodePosition.CurrentLine != 48201 {
		t.Errorf("CurrentLine = %d, want 48201", result.GcodePosition.CurrentLine)
	}
	if result.GcodePosition.TotalLines != 112400 {
		t.Errorf("TotalLines = %d, want 112400", result.GcodePosition.TotalLines)
	}
}

func TestParseReport_ExtendedFields_AMS(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"PRINTING",
		"nozzle_temper":215.0,"nozzle_target_temper":220.0,
		"bed_temper":60.0,"bed_target_temper":60.0,
		"mc_percent":50,"layer_num":10,"total_layer_num":100,
		"hms":[],
		"ams":{
			"ams":[{
				"id":"0",
				"humidity":"3",
				"temp":"28.5",
				"tray":[
					{"id":"0","tray_type":"PLA","tray_color":"FF0000","remain":85,"nozzle_temp_min":190,"nozzle_temp_max":230},
					{"id":"1","tray_type":"PETG","tray_color":"000000","remain":40,"nozzle_temp_min":220,"nozzle_temp_max":260},
					{"id":"2","tray_type":"","tray_color":"","remain":null,"nozzle_temp_min":null,"nozzle_temp_max":null},
					{"id":"3"}
				]
			}],
			"ams_exist_bits":"1",
			"tray_now":"0"
		}
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Extensions == nil {
		t.Fatal("expected extensions")
	}
	ext, ok := result.Extensions["bambu-lan"]
	if !ok {
		t.Fatal("expected bambu-lan extension")
	}
	bambu, ok := ext.(*driver.BambuExtension)
	if !ok {
		t.Fatalf("bambu-lan extension type = %T, want *driver.BambuExtension", ext)
	}
	if bambu.AMS == nil {
		t.Fatal("expected AMS data")
	}
	if len(bambu.AMS.Units) != 1 {
		t.Fatalf("AMS units = %d, want 1", len(bambu.AMS.Units))
	}
	unit := bambu.AMS.Units[0]
	if unit.ID != 0 {
		t.Errorf("unit.ID = %d, want 0", unit.ID)
	}
	wantRange := "20-30%"
	wantLevel := "moderate"
	if unit.HumidityRange == nil || *unit.HumidityRange != wantRange {
		t.Errorf("unit.HumidityRange = %v, want %q", unit.HumidityRange, wantRange)
	}
	if unit.HumidityLevel == nil || *unit.HumidityLevel != wantLevel {
		t.Errorf("unit.HumidityLevel = %v, want %q", unit.HumidityLevel, wantLevel)
	}
	if unit.Temperature == nil || *unit.Temperature != 28.5 {
		t.Errorf("unit.Temperature = %v, want 28.5", unit.Temperature)
	}
	if len(unit.Trays) != 4 {
		t.Fatalf("trays = %d, want 4", len(unit.Trays))
	}
	tray0 := unit.Trays[0]
	if tray0.FilamentType == nil || *tray0.FilamentType != "PLA" {
		t.Errorf("tray0.FilamentType = %v, want PLA", tray0.FilamentType)
	}
	if tray0.Color == nil || *tray0.Color != "FF0000" {
		t.Errorf("tray0.Color = %v, want FF0000", tray0.Color)
	}
	if tray0.RemainingPercent == nil || *tray0.RemainingPercent != 85 {
		t.Errorf("tray0.RemainingPercent = %v, want 85", tray0.RemainingPercent)
	}
	if tray0.NozzleTempMin == nil || *tray0.NozzleTempMin != 190 {
		t.Errorf("tray0.NozzleTempMin = %v, want 190", tray0.NozzleTempMin)
	}
	// Tray 3 should have nil filament type (empty)
	tray3 := unit.Trays[3]
	if tray3.FilamentType != nil {
		t.Errorf("tray3.FilamentType = %v, want nil", tray3.FilamentType)
	}
}

func TestParseReport_NumberOrStringProtocolFields(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"PRINTING",
		"nozzle_temper":"215.5","nozzle_target_temper":"220.0",
		"bed_temper":"60.0","bed_target_temper":"60",
		"chamber_temper":"35.0",
		"subtask_name":"string-fields.3mf",
		"gcode_file":"string-fields.gcode",
		"mc_percent":"42.0","layer_num":"10","mc_layer_num":"9","total_layer_num":"50",
		"mc_print_error_code":"0.0",
		"hms":[{"attr":"1","code":"2"}],
		"mc_remaining_time":"90",
		"spd_lvl":"2",
		"stg_cur":"2",
		"file_size":"14893261",
		"nozzle_diameter":0.4,
		"bed_type":4.0,
		"cur_line_num":"48201.0",
		"total_line_num":112400,
		"ams":{
			"ams":[{
				"id":0,
				"humidity":3,
				"temp":28.5,
				"tray":[
					{"id":1,"tray_type":"PLA","tray_color":"FF0000","remain":"85.0","nozzle_temp_min":"190","nozzle_temp_max":"230.0"}
				]
			}]
		}
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want []", result.Warnings)
	}
	if result.Temperatures == nil || result.Temperatures.Nozzle == nil || result.Temperatures.Bed == nil || result.Temperatures.Chamber == nil {
		t.Fatalf("missing temperatures: %v", result.Temperatures)
	}
	if result.Temperatures.Nozzle.CurrentCelsius != 215.5 {
		t.Errorf("nozzle temp = %v, want 215.5", result.Temperatures.Nozzle.CurrentCelsius)
	}
	if result.Temperatures.Nozzle.TargetCelsius == nil || *result.Temperatures.Nozzle.TargetCelsius != 220.0 {
		t.Errorf("nozzle target = %v, want 220.0", result.Temperatures.Nozzle.TargetCelsius)
	}
	if result.Temperatures.Bed.CurrentCelsius != 60.0 {
		t.Errorf("bed temp = %v, want 60.0", result.Temperatures.Bed.CurrentCelsius)
	}
	if result.Temperatures.Chamber.CurrentCelsius != 35.0 {
		t.Errorf("chamber temp = %v, want 35.0", result.Temperatures.Chamber.CurrentCelsius)
	}
	if result.Progress == nil {
		t.Fatal("expected progress")
	}
	if result.Progress.Percent != 42 {
		t.Errorf("progress percent = %d, want 42", result.Progress.Percent)
	}
	if result.Progress.CurrentLayer == nil || *result.Progress.CurrentLayer != 10 {
		t.Errorf("current layer = %v, want 10", result.Progress.CurrentLayer)
	}
	if result.Progress.TotalLayers == nil || *result.Progress.TotalLayers != 50 {
		t.Errorf("total layers = %v, want 50", result.Progress.TotalLayers)
	}
	if len(result.Errors) != 1 || result.Errors[0].Code != "hms:00000001:00000002" {
		t.Fatalf("Errors = %v, want HMS error", result.Errors)
	}
	if result.TimeEstimates == nil || result.TimeEstimates.RemainingSeconds == nil || *result.TimeEstimates.RemainingSeconds != 5400 {
		t.Errorf("RemainingSeconds = %v, want 5400", result.TimeEstimates)
	}
	if result.SpeedLevel == nil || *result.SpeedLevel != "standard" {
		t.Errorf("SpeedLevel = %v, want standard", result.SpeedLevel)
	}
	if result.Stage == nil || *result.Stage != "heatbed_preheating" {
		t.Errorf("Stage = %v, want heatbed_preheating", result.Stage)
	}
	if result.PrintMeta == nil {
		t.Fatal("expected print meta")
	}
	if result.PrintMeta.FileSize == nil || *result.PrintMeta.FileSize != 14893261 {
		t.Errorf("FileSize = %v, want 14893261", result.PrintMeta.FileSize)
	}
	if result.PrintMeta.NozzleDiameter == nil || *result.PrintMeta.NozzleDiameter != 0.4 {
		t.Errorf("NozzleDiameter = %v, want 0.4", result.PrintMeta.NozzleDiameter)
	}
	if result.PrintMeta.BedType == nil || *result.PrintMeta.BedType != "textured_pei" {
		t.Errorf("BedType = %v, want textured_pei", result.PrintMeta.BedType)
	}
	if result.GcodePosition == nil || result.GcodePosition.CurrentLine != 48201 || result.GcodePosition.TotalLines != 112400 {
		t.Errorf("GcodePosition = %v, want 48201/112400", result.GcodePosition)
	}
	if result.Extensions == nil {
		t.Fatal("expected extensions")
	}
	bambu, ok := result.Extensions["bambu-lan"].(*driver.BambuExtension)
	if !ok || bambu.AMS == nil || len(bambu.AMS.Units) != 1 || len(bambu.AMS.Units[0].Trays) != 1 {
		t.Fatalf("AMS extension = %#v, want one unit with one tray", result.Extensions["bambu-lan"])
	}
	unit := bambu.AMS.Units[0]
	if unit.ID != 0 {
		t.Errorf("AMS unit ID = %d, want 0", unit.ID)
	}
	if unit.Temperature == nil || *unit.Temperature != 28.5 {
		t.Errorf("AMS unit temperature = %v, want 28.5", unit.Temperature)
	}
	tray := unit.Trays[0]
	if tray.Slot != 1 {
		t.Errorf("tray slot = %d, want 1", tray.Slot)
	}
	if tray.RemainingPercent == nil || *tray.RemainingPercent != 85 {
		t.Errorf("tray remaining = %v, want 85", tray.RemainingPercent)
	}
	if tray.NozzleTempMin == nil || *tray.NozzleTempMin != 190 {
		t.Errorf("tray nozzle min = %v, want 190", tray.NozzleTempMin)
	}
	if tray.NozzleTempMax == nil || *tray.NozzleTempMax != 230 {
		t.Errorf("tray nozzle max = %v, want 230", tray.NozzleTempMax)
	}
}

func TestParseReport_ExtendedFields_PrintMeta(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"PRINTING",
		"nozzle_temper":215.0,"nozzle_target_temper":220.0,
		"bed_temper":60.0,"bed_target_temper":60.0,
		"mc_percent":50,"layer_num":10,"total_layer_num":100,
		"hms":[],
		"gcode_file":"bracket.gcode",
		"subtask_name":"bracket.3mf",
		"file_size":14893261,
		"nozzle_diameter":"0.4",
		"bed_type":"4"
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PrintMeta == nil {
		t.Fatal("expected print meta")
	}
	if result.PrintMeta.FileName != "bracket.gcode" {
		t.Errorf("FileName = %q, want bracket.gcode", result.PrintMeta.FileName)
	}
	if result.PrintMeta.FileSize == nil || *result.PrintMeta.FileSize != 14893261 {
		t.Errorf("FileSize = %v, want 14893261", result.PrintMeta.FileSize)
	}
	if result.PrintMeta.NozzleDiameter == nil || *result.PrintMeta.NozzleDiameter != 0.4 {
		t.Errorf("NozzleDiameter = %v, want 0.4", result.PrintMeta.NozzleDiameter)
	}
	if result.PrintMeta.BedType == nil || *result.PrintMeta.BedType != "textured_pei" {
		t.Errorf("BedType = %v, want textured_pei", result.PrintMeta.BedType)
	}
}

func TestParseReport_ExtendedFields_NoExtendedData(t *testing.T) {
	// A minimal report should produce nil extended fields (not empty structs).
	data := []byte(`{"print":{
		"gcode_state":"IDLE",
		"nozzle_temper":24.0,"nozzle_target_temper":0,
		"bed_temper":23.0,"bed_target_temper":0,
		"mc_percent":0,
		"hms":[]
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Fans != nil {
		t.Errorf("expected nil Fans, got %v", result.Fans)
	}
	if result.TimeEstimates != nil {
		t.Errorf("expected nil TimeEstimates, got %v", result.TimeEstimates)
	}
	if result.SpeedLevel != nil {
		t.Errorf("expected nil SpeedLevel, got %v", result.SpeedLevel)
	}
	if result.Wifi != nil {
		t.Errorf("expected nil Wifi, got %v", result.Wifi)
	}
	if result.Lights != nil {
		t.Errorf("expected nil Lights, got %v", result.Lights)
	}
	if result.PrintMeta != nil {
		t.Errorf("expected nil PrintMeta, got %v", result.PrintMeta)
	}
	if result.Stage != nil {
		t.Errorf("expected nil Stage, got %v", result.Stage)
	}
	if result.Timelapse != nil {
		t.Errorf("expected nil Timelapse, got %v", result.Timelapse)
	}
	if result.GcodePosition != nil {
		t.Errorf("expected nil GcodePosition, got %v", result.GcodePosition)
	}
	if result.Extensions != nil {
		t.Errorf("expected nil Extensions, got %v", result.Extensions)
	}
}

func TestParseReport_InvalidJSON(t *testing.T) {
	_, err := parseReport([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4, got %v", err)
	}
}

func TestParseReport_TypeMismatchReturnsPartialStatusWithWarning(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"IDLE",
		"nozzle_temper":24.0,
		"bed_temper":23.0,
		"chamber_temper":0,
		"mc_percent":0,
		"hms":[],
		"ipcam_record_timelapse":true
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "idle" {
		t.Errorf("State = %q, want idle", result.State)
	}
	if result.Temperatures == nil || result.Temperatures.Nozzle == nil || result.Temperatures.Bed == nil {
		t.Fatalf("missing temperatures: %v", result.Temperatures)
	}
	if result.Progress == nil || result.Progress.Percent != 0 {
		t.Fatalf("Progress = %v, want 0 percent", result.Progress)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want one type mismatch warning", result.Warnings)
	}
	if result.Warnings[0].Code != "status_field_type_mismatch" {
		t.Errorf("warning code = %q, want status_field_type_mismatch", result.Warnings[0].Code)
	}
}

// --- fakeClient helpers ---

type fakeToken struct {
	err  error
	done chan struct{}
}

func newFakeToken(err error) *fakeToken {
	ch := make(chan struct{})
	close(ch)
	return &fakeToken{err: err, done: ch}
}

func newPendingFakeToken() *fakeToken {
	return &fakeToken{done: make(chan struct{})}
}

func (t *fakeToken) Wait() bool {
	<-t.done
	return true
}
func (t *fakeToken) WaitTimeout(timeout time.Duration) bool {
	select {
	case <-t.done:
		return true
	case <-time.After(timeout):
		return false
	}
}
func (t *fakeToken) Done() <-chan struct{} { return t.done }
func (t *fakeToken) Error() error          { return t.err }

type fakeMessage struct {
	topic   string
	payload []byte
}

func (m *fakeMessage) Duplicate() bool   { return false }
func (m *fakeMessage) Qos() byte         { return 0 }
func (m *fakeMessage) Retained() bool    { return false }
func (m *fakeMessage) Topic() string     { return m.topic }
func (m *fakeMessage) MessageID() uint16 { return 0 }
func (m *fakeMessage) Payload() []byte   { return m.payload }
func (m *fakeMessage) Ack()              {}

// fakeClient is an mqttConn that returns immediately.
// If payload is non-nil, the Subscribe handler is called synchronously with it.
// If connectErr is non-nil, Connect returns that error.
type fakeClient struct {
	connectErr     error
	payload        []byte
	subscribeToken mqtt.Token
	publishToken   mqtt.Token
}

func (f *fakeClient) Connect() mqtt.Token {
	return newFakeToken(f.connectErr)
}

func (f *fakeClient) Subscribe(topic string, _ byte, cb mqtt.MessageHandler) mqtt.Token {
	if f.payload != nil {
		cb(nil, &fakeMessage{topic: topic, payload: f.payload})
	}
	if f.subscribeToken != nil {
		return f.subscribeToken
	}
	return newFakeToken(nil)
}

func (f *fakeClient) Publish(_ string, _ byte, _ bool, _ any) mqtt.Token {
	if f.publishToken != nil {
		return f.publishToken
	}
	return newFakeToken(nil)
}

func (f *fakeClient) Disconnect(_ uint) {}

// --- Status tests ---

func newFakeDriver(fc *fakeClient) *Driver {
	return &Driver{newClient: func(_ *mqtt.ClientOptions) mqttConn { return fc }}
}

func defaultProfileInput() driver.ProfileInput {
	return driver.ProfileInput{
		Name:     "myprinter",
		Driver:   "bambu-lan",
		Host:     "192.0.2.1",
		Serial:   "SN001",
		Timeout:  5 * time.Second,
		Insecure: true,
	}
}

func TestStatus_HappyPath(t *testing.T) {
	payload := []byte(`{"print":{
        "gcode_state":"PRINTING",
        "nozzle_temper":215.0,"nozzle_target_temper":220.0,
        "bed_temper":60.0,"bed_target_temper":60.0,
        "chamber_temper":0,"subtask_name":"part.3mf",
        "mc_percent":55,"mc_layer_num":11,"total_layer_num":20,"hms":[]
    }}`)
	drv := newFakeDriver(&fakeClient{payload: payload})
	result, err := drv.Status(context.Background(), defaultProfileInput(), driver.SecretsBundle{AccessCode: "code"}, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "printing" {
		t.Errorf("State = %q, want printing", result.State)
	}
	if result.Job == nil || result.Job.Name != "part.3mf" {
		t.Errorf("Job = %v, want part.3mf", result.Job)
	}
	if !result.Capabilities.Status {
		t.Error("Capabilities.Status should be true")
	}
}

func TestStatus_AuthFailure_ExitsCode3(t *testing.T) {
	fc := &fakeClient{connectErr: packets.ErrorRefusedBadUsernameOrPassword}
	drv := newFakeDriver(fc)
	_, err := drv.Status(context.Background(), defaultProfileInput(), driver.SecretsBundle{AccessCode: "bad"}, slog.Default())
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestStatus_FingerprintMismatch_ExitsCode3(t *testing.T) {
	fc := &fakeClient{connectErr: &fingerprintMismatchError{got: testFingerprintA, want: testFingerprintB}}
	drv := newFakeDriver(fc)
	_, err := drv.Status(context.Background(), defaultProfileInput(), driver.SecretsBundle{AccessCode: "code"}, slog.Default())
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3 for fingerprint mismatch, got %v", err)
	}
}

func TestStatus_EmptySecureFingerprint_ExitsCode3(t *testing.T) {
	p := defaultProfileInput()
	p.Insecure = false
	drv := newFakeDriver(&fakeClient{})
	_, err := drv.Status(context.Background(), p, driver.SecretsBundle{AccessCode: "code"}, slog.Default())
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3 for empty secure fingerprint, got %v", err)
	}
}

func TestStatus_Timeout_ExitsCode4(t *testing.T) {
	// Subscribe handler is never called (payload nil) → ctx expires waiting for report.
	fc := &fakeClient{payload: nil}
	drv := newFakeDriver(fc)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := drv.Status(ctx, defaultProfileInput(), driver.SecretsBundle{AccessCode: "code"}, slog.Default())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4 for timeout, got %v", err)
	}
}

func TestStatus_SubscribeWaitHonorsContext(t *testing.T) {
	fc := &fakeClient{subscribeToken: newPendingFakeToken()}
	drv := newFakeDriver(fc)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := drv.Status(ctx, defaultProfileInput(), driver.SecretsBundle{AccessCode: "code"}, slog.Default())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4 for subscribe timeout, got %v", err)
	}
}

func TestStatus_PublishWaitHonorsContext(t *testing.T) {
	fc := &fakeClient{publishToken: newPendingFakeToken()}
	drv := newFakeDriver(fc)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := drv.Status(ctx, defaultProfileInput(), driver.SecretsBundle{AccessCode: "code"}, slog.Default())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4 for publish timeout, got %v", err)
	}
}

// --- H2C compatibility tests ---

func TestIsPushallReport_H2C_StgArray(t *testing.T) {
	// H2C sends "stg" as an array instead of int. isPushallReport must still
	// detect the report via gcode_state.
	data := []byte(`{"print":{"gcode_state":"IDLE","stg":[],"stg_cur":-1}}`)
	if !isPushallReport(data) {
		t.Error("isPushallReport should accept H2C payload with stg as array")
	}
}

func TestParseReport_H2C_TypeMismatchFields(t *testing.T) {
	// H2C payload with stg as array and numeric values sometimes sent as strings.
	data := []byte(`{"print":{
		"gcode_state":"IDLE",
		"nozzle_temper":31.0,"nozzle_target_temper":0.0,
		"bed_temper":23.0,"bed_target_temper":0.0,
		"chamber_temper":"32.5",
		"mc_percent":0,
		"hms":[],
		"stg":[],"stg_cur":-1,
		"gcode_file_prepare_percent":"0",
		"spd_lvl":2,
		"wifi_signal":"-69dBm",
		"lights_report":[{"node":"chamber_light","mode":"off"}]
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "idle" {
		t.Errorf("State = %q, want idle", result.State)
	}
	if result.Temperatures == nil || result.Temperatures.Nozzle == nil {
		t.Fatal("expected nozzle temperature")
	}
	if result.Temperatures.Nozzle.CurrentCelsius != 31.0 {
		t.Errorf("nozzle temp = %v, want 31.0", result.Temperatures.Nozzle.CurrentCelsius)
	}
	if result.Temperatures.Chamber == nil || result.Temperatures.Chamber.CurrentCelsius != 32.5 {
		t.Errorf("chamber temp = %v, want 32.5", result.Temperatures.Chamber)
	}
	for _, warning := range result.Warnings {
		if warning.Code == "chamber_temperature_unavailable" {
			t.Errorf("unexpected chamber unavailable warning: %v", result.Warnings)
		}
	}
	if result.SpeedLevel == nil || *result.SpeedLevel != "standard" {
		t.Errorf("SpeedLevel = %v, want standard", result.SpeedLevel)
	}
	// Wi-Fi should parse "-69dBm" suffix format.
	if result.Wifi == nil {
		t.Fatal("expected wifi data")
	}
	if result.Wifi.SignalDbm != -69 {
		t.Errorf("SignalDbm = %d, want -69", result.Wifi.SignalDbm)
	}
	// Lights nested inside print on H2C.
	if result.Lights == nil {
		t.Fatal("expected lights data")
	}
	if result.Lights["chamber_light"] != "off" {
		t.Errorf("chamber_light = %q, want off", result.Lights["chamber_light"])
	}
}

func TestParseReport_H2C_ChamberTemperatureAliases(t *testing.T) {
	tests := []struct {
		name string
		data string
		want float64
	}{
		{
			name: "chamber_temp",
			data: `{"print":{
				"gcode_state":"IDLE",
				"nozzle_temper":31.0,
				"bed_temper":23.0,
				"mc_percent":0,
				"hms":[],
				"chamber_temp":"32.5"
			}}`,
			want: 32.5,
		},
		{
			name: "chamber_temperature",
			data: `{"print":{
				"gcode_state":"IDLE",
				"nozzle_temper":31.0,
				"bed_temper":23.0,
				"mc_percent":0,
				"hms":[],
				"chamber_temperature":33.0
			}}`,
			want: 33.0,
		},
		{
			name: "nested chamber temp",
			data: `{"print":{
				"gcode_state":"IDLE",
				"nozzle_temper":31.0,
				"bed_temper":23.0,
				"mc_percent":0,
				"hms":[],
				"chamber":{"temp":"34.5"}
			}}`,
			want: 34.5,
		},
		{
			name: "top-level chamber temp",
			data: `{
				"print":{
					"gcode_state":"IDLE",
					"nozzle_temper":31.0,
					"bed_temper":23.0,
					"mc_percent":0,
					"hms":[]
				},
				"chamber_temp":"35.5"
			}`,
			want: 35.5,
		},
		{
			name: "H2C device.ctc.info.temp",
			data: `{"print":{
				"gcode_state":"IDLE",
				"nozzle_temper":34.0,
				"bed_temper":27.0,
				"mc_percent":100,
				"hms":[],
				"device":{"ctc":{"info":{"temp":27},"state":0},"bed":{"info":{"temp":27},"state":0},"bed_temp":27}
			}}`,
			want: 27.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseReport([]byte(tt.data))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Temperatures == nil || result.Temperatures.Chamber == nil {
				t.Fatalf("chamber temperature missing: %+v", result.Temperatures)
			}
			if result.Temperatures.Chamber.CurrentCelsius != tt.want {
				t.Errorf("chamber temp = %v, want %v", result.Temperatures.Chamber.CurrentCelsius, tt.want)
			}
			for _, warning := range result.Warnings {
				if warning.Code == "chamber_temperature_unavailable" {
					t.Errorf("unexpected chamber unavailable warning: %v", result.Warnings)
				}
			}
		})
	}
}

func TestParseReport_H2C_ChamberTargetAliasDoesNotBecomeCurrentTemperature(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"IDLE",
		"nozzle_temper":31.0,
		"bed_temper":23.0,
		"mc_percent":0,
		"hms":[],
		"chamber_target_temp":45.0
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Temperatures == nil {
		t.Fatal("expected temperatures")
	}
	if result.Temperatures.Chamber != nil {
		t.Fatalf("chamber target was parsed as current temperature: %+v", result.Temperatures.Chamber)
	}
	foundWarning := false
	for _, warning := range result.Warnings {
		if warning.Code == "chamber_temperature_unavailable" {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Fatalf("expected chamber unavailable warning, got %v", result.Warnings)
	}
}

func TestParseReport_H2C_LightsInsidePrint(t *testing.T) {
	// When lights_report is inside print (H2C) and absent at top level.
	data := []byte(`{"print":{
		"gcode_state":"IDLE",
		"nozzle_temper":25.0,
		"bed_temper":22.0,
		"mc_percent":0,
		"hms":[],
		"lights_report":[{"node":"chamber_light","mode":"on"},{"node":"work_light","mode":"off"}]
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Lights == nil || len(result.Lights) != 2 {
		t.Fatalf("expected 2 lights, got %v", result.Lights)
	}
	if result.Lights["chamber_light"] != "on" {
		t.Errorf("chamber_light = %q, want on", result.Lights["chamber_light"])
	}
	if result.Lights["work_light"] != "off" {
		t.Errorf("work_light = %q, want off", result.Lights["work_light"])
	}
}

func TestMapWifi_DBMSuffix(t *testing.T) {
	sig := rawValueString("-69dBm")
	p := &bambuPrint{WifiSignal: &sig}
	wifi := mapWifi(p)
	if wifi == nil {
		t.Fatal("expected wifi result")
	}
	if wifi.SignalDbm != -69 {
		t.Errorf("SignalDbm = %d, want -69", wifi.SignalDbm)
	}
}

func TestParseReport_H2C_RemainTimeFallback(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"PRINTING",
		"nozzle_temper":215.0,"nozzle_target_temper":220.0,
		"bed_temper":60.0,"bed_target_temper":60.0,
		"mc_percent":42,"layer_num":10,"total_layer_num":100,
		"hms":[],
		"remain_time":45
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TimeEstimates == nil {
		t.Fatal("expected time estimates from remain_time")
	}
	if result.TimeEstimates.RemainingSeconds == nil || *result.TimeEstimates.RemainingSeconds != 2700 {
		t.Errorf("RemainingSeconds = %v, want 2700", result.TimeEstimates.RemainingSeconds)
	}
}

func TestParseReport_H2C_RemainTimeNotUsedWhenMcPresent(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"PRINTING",
		"nozzle_temper":215.0,"nozzle_target_temper":220.0,
		"bed_temper":60.0,"bed_target_temper":60.0,
		"mc_percent":50,
		"hms":[],
		"mc_remaining_time":90,
		"remain_time":100
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TimeEstimates == nil {
		t.Fatal("expected time estimates")
	}
	// mc_remaining_time should take priority over remain_time.
	if result.TimeEstimates.RemainingSeconds == nil || *result.TimeEstimates.RemainingSeconds != 5400 {
		t.Errorf("RemainingSeconds = %v, want 5400 (mc_remaining_time preferred)", result.TimeEstimates.RemainingSeconds)
	}
}

func TestParseReport_H2C_IpcamTimelapse(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		recording bool
	}{
		{
			name: "ipcam.timelapse enable",
			data: `{"print":{
				"gcode_state":"PRINTING",
				"nozzle_temper":215.0,"bed_temper":60.0,
				"mc_percent":50,"hms":[],
				"ipcam":{"timelapse":"enable"}
			}}`,
			recording: true,
		},
		{
			name: "ipcam.timelapse disable",
			data: `{"print":{
				"gcode_state":"PRINTING",
				"nozzle_temper":215.0,"bed_temper":60.0,
				"mc_percent":50,"hms":[],
				"ipcam":{"timelapse":"disable"}
			}}`,
			recording: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseReport([]byte(tt.data))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Timelapse == nil {
				t.Fatal("expected timelapse data from ipcam.timelapse")
			}
			if result.Timelapse.Recording != tt.recording {
				t.Errorf("Recording = %v, want %v", result.Timelapse.Recording, tt.recording)
			}
		})
	}
}

func TestParseReport_H2C_TopLevelTimelapsePreferred(t *testing.T) {
	// When both ipcam_record_timelapse and ipcam.timelapse are present,
	// the top-level field takes priority.
	data := []byte(`{"print":{
		"gcode_state":"PRINTING",
		"nozzle_temper":215.0,"bed_temper":60.0,
		"mc_percent":50,"hms":[],
		"ipcam_record_timelapse":"enable",
		"ipcam":{"timelapse":"disable"}
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Timelapse == nil {
		t.Fatal("expected timelapse data")
	}
	if !result.Timelapse.Recording {
		t.Error("expected recording=true from ipcam_record_timelapse taking priority")
	}
}

func TestParseReport_A1Mini_FullPayload(t *testing.T) {
	// Real A1 Mini (Georgia) payload structure.
	data := []byte(`{"print":{"nozzle_temper":24.375,"nozzle_target_temper":0,"bed_temper":24.90625,"bed_target_temper":0,"chamber_temper":5,"mc_print_stage":"1","heatbreak_fan_speed":"0","cooling_fan_speed":"0","big_fan1_speed":"0","big_fan2_speed":"0","mc_percent":100,"mc_remaining_time":0,"spd_mag":100,"spd_lvl":2,"gcode_state":"FINISH","gcode_file_prepare_percent":"100","subtask_name":"Silksong_Cog_Fly_-_Articulated_Wings_+_Head.gcode.3mf","gcode_file":"","stg":[],"stg_cur":255,"print_type":"idle","layer_num":168,"total_layer_num":168,"nozzle_diameter":"0.4","nozzle_type":"stainless_steel","wifi_signal":"-57dBm","ipcam":{"ipcam_dev":"1","ipcam_record":"enable","timelapse":"disable","resolution":"1080p","tutk_server":"disable","mode_bits":3},"hms":[],"ams":{"ams":[],"ams_exist_bits":"0","tray_exist_bits":"0","tray_is_bbl_bits":"0","tray_tar":"255","tray_now":"254","tray_pre":"254","tray_read_done_bits":"0","tray_reading_bits":"0","version":4,"insert_flag":true,"power_on_flag":false},"vt_tray":{"id":"254","tag_uid":"0000000000000000","tray_id_name":"","tray_info_idx":"GFL99","tray_type":"PLA","tray_sub_brands":"","tray_color":"5A657BFF","tray_weight":"0","tray_diameter":"0.00","tray_temp":"0","tray_time":"0","bed_temp_type":"0","bed_temp":"0","nozzle_temp_max":"240","nozzle_temp_min":"190","xcam_info":"000000000000000000000000","tray_uuid":"00000000000000000000000000000000","remain":0,"k":0.019999999552965164,"n":1,"cali_idx":-1},"lights_report":[{"node":"chamber_light","mode":"off"}],"command":"push_status","msg":0,"sequence_id":"3582"}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// State.
	if result.State != "idle" {
		t.Errorf("State = %q, want idle", result.State)
	}

	// Temperatures.
	if result.Temperatures == nil {
		t.Fatal("expected temperatures")
	}
	if result.Temperatures.Nozzle == nil || result.Temperatures.Nozzle.CurrentCelsius != 24.375 {
		t.Errorf("Nozzle = %+v, want 24.375", result.Temperatures.Nozzle)
	}
	if result.Temperatures.Bed == nil || result.Temperatures.Bed.CurrentCelsius != 24.90625 {
		t.Errorf("Bed = %+v, want 24.90625", result.Temperatures.Bed)
	}
	if result.Temperatures.Chamber == nil || result.Temperatures.Chamber.CurrentCelsius != 5 {
		t.Errorf("Chamber = %+v, want 5", result.Temperatures.Chamber)
	}

	// Progress.
	if result.Progress == nil {
		t.Fatal("expected progress")
	}
	if result.Progress.Percent != 100 {
		t.Errorf("Percent = %d, want 100", result.Progress.Percent)
	}
	if result.Progress.CurrentLayer == nil || *result.Progress.CurrentLayer != 168 {
		t.Errorf("CurrentLayer = %v, want 168", result.Progress.CurrentLayer)
	}

	// Speed level.
	if result.SpeedLevel == nil || *result.SpeedLevel != "standard" {
		t.Errorf("SpeedLevel = %v, want standard", result.SpeedLevel)
	}

	// Stage: stg_cur=255 should be nil (no active stage).
	if result.Stage != nil {
		t.Errorf("Stage = %v, want nil for stg_cur=255", *result.Stage)
	}

	// Wi-Fi.
	if result.Wifi == nil || result.Wifi.SignalDbm != -57 {
		t.Errorf("Wifi = %+v, want -57 dBm", result.Wifi)
	}

	// Lights.
	if result.Lights == nil || result.Lights["chamber_light"] != "off" {
		t.Errorf("Lights = %+v, want chamber_light=off", result.Lights)
	}

	// Timelapse from nested ipcam.
	if result.Timelapse == nil {
		t.Fatal("expected timelapse from ipcam.timelapse")
	}
	if result.Timelapse.Recording {
		t.Error("expected timelapse recording=false")
	}

	// PrintMeta: nozzle diameter should be present.
	if result.PrintMeta == nil {
		t.Fatal("expected print meta")
	}
	if result.PrintMeta.NozzleDiameter == nil || *result.PrintMeta.NozzleDiameter != 0.4 {
		t.Errorf("NozzleDiameter = %v, want 0.4", result.PrintMeta.NozzleDiameter)
	}

	// Extensions: vt_tray should produce an AMS unit with PLA filament.
	if result.Extensions == nil {
		t.Fatal("expected extensions from vt_tray")
	}
	ext, ok := result.Extensions["bambu-lan"]
	if !ok {
		t.Fatal("expected bambu-lan extension")
	}
	bambuExt, ok := ext.(*driver.BambuExtension)
	if !ok || bambuExt.AMS == nil {
		t.Fatal("expected AMS data from vt_tray")
	}
	if len(bambuExt.AMS.Units) != 1 {
		t.Fatalf("AMS units = %d, want 1 (external spool)", len(bambuExt.AMS.Units))
	}
	vtUnit := bambuExt.AMS.Units[0]
	if vtUnit.ID != 254 {
		t.Errorf("vt_tray unit ID = %d, want 254", vtUnit.ID)
	}
	if len(vtUnit.Trays) != 1 {
		t.Fatalf("vt_tray trays = %d, want 1", len(vtUnit.Trays))
	}
	if vtUnit.Trays[0].FilamentType == nil || *vtUnit.Trays[0].FilamentType != "PLA" {
		t.Errorf("vt_tray filament = %v, want PLA", vtUnit.Trays[0].FilamentType)
	}
	if vtUnit.Trays[0].Color == nil || *vtUnit.Trays[0].Color != "5A657BFF" {
		t.Errorf("vt_tray color = %v, want 5A657BFF", vtUnit.Trays[0].Color)
	}
	if vtUnit.Trays[0].NozzleTempMin == nil || *vtUnit.Trays[0].NozzleTempMin != 190 {
		t.Errorf("vt_tray nozzle min = %v, want 190", vtUnit.Trays[0].NozzleTempMin)
	}
	if vtUnit.Trays[0].NozzleTempMax == nil || *vtUnit.Trays[0].NozzleTempMax != 240 {
		t.Errorf("vt_tray nozzle max = %v, want 240", vtUnit.Trays[0].NozzleTempMax)
	}

	// No warnings about chamber temperature (it's present with value 5).
	for _, w := range result.Warnings {
		if w.Code == "chamber_temperature_unavailable" {
			t.Errorf("unexpected chamber_temperature_unavailable warning")
		}
	}
}

func TestParseReport_H2C_FullPayload(t *testing.T) {
	// Real H2C (Dakota) payload structure.
	data := []byte(`{"print":{"gcode_state":"FINISH","nozzle_temper":34.0,"nozzle_target_temper":0.0,"bed_temper":27.0,"bed_target_temper":0.0,"mc_percent":100,"mc_remaining_time":0,"hms":[],"subtask_name":"Chaveiro_Flex_AstroBot","gcode_file":"/data/Metadata/plate_1.gcode","layer_num":18,"total_layer_num":18,"heatbreak_fan_speed":"0","cooling_fan_speed":"0","big_fan1_speed":"0","big_fan2_speed":"0","spd_lvl":2,"spd_mag":100,"wifi_signal":"-67dBm","nozzle_diameter":"0.4","nozzle_type":"HS01-0.4","stg":[79,29,13,74,72,4,54,39,8,14,1,3],"stg_cur":-1,"remain_time":0,"ipcam":{"agora_service":"disable","brtc_service":"enable","ipcam_dev":"1","ipcam_record":"enable","resolution":"1080p","rtsp_url":"rtsps://10.20.20.10:322/streaming/live/1","timelapse":"disable","tutk_server":"disable"},"lights_report":[{"mode":"off","node":"chamber_light"},{"mode":"flashing","node":"work_light"},{"mode":"off","node":"chamber_light2"}],"device":{"ctc":{"info":{"temp":27},"state":0},"bed":{"info":{"temp":27},"state":0},"bed_temp":27,"extruder":{"info":[{"id":0,"temp":34},{"id":1,"temp":30}],"state":33042},"nozzle":{"info":[{"color_m":"46A8F9FF","diameter":0.4,"fila_id":"GFL96","id":16,"p_t":1716,"sn":"20D06A5A2424071","stat":0,"tm":350,"type":"HS01","wear":128.0}],"src_id":17,"state":0,"tar_id":17},"plate":{"base":6,"cur_id":"P0102","mat":1,"tar_id":""}},"ams":{"ams":[{"humidity":"2","humidity_raw":"32","id":"0","temp":"28.4","tray":[{"id":"0","state":10},{"id":"1","state":10},{"id":"2","state":10},{"id":"3","state":10}]},{"humidity":"2","humidity_raw":"33","id":"1","temp":"26.2","tray":[{"id":"0","state":0},{"id":"1","state":0},{"id":"2","state":10},{"id":"3","state":10}]},{"humidity":"1","humidity_raw":"50","id":"128","temp":"24.9","tray":[{"id":"0","state":10}]}],"ams_exist_bits":"13","tray_now":"255","tray_pre":"255","tray_tar":"255","version":9101},"vir_slot":[{"id":"254","tray_color":"00000000","tray_type":"","nozzle_temp_max":"0","nozzle_temp_min":"0","remain":0},{"id":"255","tray_color":"00000000","tray_type":"","nozzle_temp_max":"0","nozzle_temp_min":"0","remain":0}],"mc_print_error_code":"0"}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// State.
	if result.State != "idle" {
		t.Errorf("State = %q, want idle", result.State)
	}

	// Temperatures.
	if result.Temperatures == nil {
		t.Fatal("expected temperatures")
	}
	if result.Temperatures.Nozzle == nil || result.Temperatures.Nozzle.CurrentCelsius != 34.0 {
		t.Errorf("Nozzle = %+v, want 34.0", result.Temperatures.Nozzle)
	}
	if result.Temperatures.Bed == nil || result.Temperatures.Bed.CurrentCelsius != 27.0 {
		t.Errorf("Bed = %+v, want 27.0", result.Temperatures.Bed)
	}
	// Chamber temp from device.ctc.info.temp.
	if result.Temperatures.Chamber == nil {
		t.Fatal("expected chamber temperature from device.ctc.info.temp")
	}
	if result.Temperatures.Chamber.CurrentCelsius != 27.0 {
		t.Errorf("Chamber = %v, want 27.0", result.Temperatures.Chamber.CurrentCelsius)
	}

	// Progress.
	if result.Progress == nil {
		t.Fatal("expected progress")
	}
	if result.Progress.Percent != 100 {
		t.Errorf("Percent = %d, want 100", result.Progress.Percent)
	}
	if result.Progress.CurrentLayer == nil || *result.Progress.CurrentLayer != 18 {
		t.Errorf("CurrentLayer = %v, want 18", result.Progress.CurrentLayer)
	}
	if result.Progress.TotalLayers == nil || *result.Progress.TotalLayers != 18 {
		t.Errorf("TotalLayers = %v, want 18", result.Progress.TotalLayers)
	}

	// Job.
	if result.Job == nil || result.Job.Name != "Chaveiro_Flex_AstroBot" {
		t.Errorf("Job = %+v, want Chaveiro_Flex_AstroBot", result.Job)
	}

	// Speed level.
	if result.SpeedLevel == nil || *result.SpeedLevel != "standard" {
		t.Errorf("SpeedLevel = %v, want standard", result.SpeedLevel)
	}

	// Stage: stg_cur=-1 should be nil.
	if result.Stage != nil {
		t.Errorf("Stage = %v, want nil for stg_cur=-1", *result.Stage)
	}

	// Wi-Fi.
	if result.Wifi == nil || result.Wifi.SignalDbm != -67 {
		t.Errorf("Wifi = %+v, want -67 dBm", result.Wifi)
	}

	// Lights: H2C has 3 light nodes.
	if result.Lights == nil {
		t.Fatal("expected lights")
	}
	if result.Lights["chamber_light"] != "off" {
		t.Errorf("chamber_light = %q, want off", result.Lights["chamber_light"])
	}
	if result.Lights["work_light"] != "flashing" {
		t.Errorf("work_light = %q, want flashing", result.Lights["work_light"])
	}
	if result.Lights["chamber_light2"] != "off" {
		t.Errorf("chamber_light2 = %q, want off", result.Lights["chamber_light2"])
	}

	// Timelapse from nested ipcam.
	if result.Timelapse == nil {
		t.Fatal("expected timelapse from ipcam.timelapse")
	}
	if result.Timelapse.Recording {
		t.Error("expected timelapse recording=false")
	}

	// PrintMeta.
	if result.PrintMeta == nil {
		t.Fatal("expected print meta")
	}
	if result.PrintMeta.FileName != "/data/Metadata/plate_1.gcode" {
		t.Errorf("FileName = %q, want /data/Metadata/plate_1.gcode", result.PrintMeta.FileName)
	}
	if result.PrintMeta.NozzleDiameter == nil || *result.PrintMeta.NozzleDiameter != 0.4 {
		t.Errorf("NozzleDiameter = %v, want 0.4", result.PrintMeta.NozzleDiameter)
	}

	// AMS: 3 units (IDs 0, 1, 128) from real AMS data.
	if result.Extensions == nil {
		t.Fatal("expected extensions")
	}
	ext, ok := result.Extensions["bambu-lan"]
	if !ok {
		t.Fatal("expected bambu-lan extension")
	}
	bambuExt, ok := ext.(*driver.BambuExtension)
	if !ok || bambuExt.AMS == nil {
		t.Fatal("expected AMS data")
	}
	// 3 AMS units, vir_slot entries are empty so should be excluded.
	if len(bambuExt.AMS.Units) != 3 {
		t.Errorf("AMS units = %d, want 3", len(bambuExt.AMS.Units))
	}

	// Verify AMS unit IDs.
	unitIDs := make(map[int]bool)
	for _, u := range bambuExt.AMS.Units {
		unitIDs[u.ID] = true
	}
	if !unitIDs[0] || !unitIDs[1] || !unitIDs[128] {
		t.Errorf("expected AMS unit IDs {0, 1, 128}, got %v", unitIDs)
	}

	// Verify humidity and temperature on first unit.
	unit0 := bambuExt.AMS.Units[0]
	if unit0.HumidityLevel == nil || *unit0.HumidityLevel != "dry" {
		t.Errorf("unit0 humidity level = %v, want dry", unit0.HumidityLevel)
	}
	if unit0.Temperature == nil || *unit0.Temperature != 28.4 {
		t.Errorf("unit0 temperature = %v, want 28.4", unit0.Temperature)
	}

	// No chamber_temperature_unavailable warning.
	for _, w := range result.Warnings {
		if w.Code == "chamber_temperature_unavailable" {
			t.Error("unexpected chamber_temperature_unavailable warning")
		}
	}
}

func TestParseReport_StgCur255_IsNil(t *testing.T) {
	data := []byte(`{"print":{
		"gcode_state":"IDLE",
		"nozzle_temper":24.0,"bed_temper":23.0,
		"mc_percent":0,"hms":[],
		"stg_cur":255
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Stage != nil {
		t.Errorf("Stage = %q, want nil for stg_cur=255", *result.Stage)
	}
}

func TestParseReport_VtTray_Empty_NotIncluded(t *testing.T) {
	// Empty vt_tray (no filament) should not create an AMS extension entry.
	data := []byte(`{"print":{
		"gcode_state":"IDLE",
		"nozzle_temper":24.0,"bed_temper":23.0,
		"mc_percent":0,"hms":[],
		"vt_tray":{"id":"254","tray_type":"","tray_color":"00000000","remain":0,"nozzle_temp_min":"0","nozzle_temp_max":"0"}
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Extensions != nil {
		t.Errorf("expected nil extensions for empty vt_tray, got %+v", result.Extensions)
	}
}

func TestParseReport_VirSlot_Empty_NotIncluded(t *testing.T) {
	// Empty vir_slot entries should not create AMS extension entries.
	data := []byte(`{"print":{
		"gcode_state":"IDLE",
		"nozzle_temper":24.0,"bed_temper":23.0,
		"mc_percent":0,"hms":[],
		"vir_slot":[
			{"id":"254","tray_color":"00000000","tray_type":"","nozzle_temp_max":"0","nozzle_temp_min":"0","remain":0},
			{"id":"255","tray_color":"00000000","tray_type":"","nozzle_temp_max":"0","nozzle_temp_min":"0","remain":0}
		]
	}}`)

	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Extensions != nil {
		t.Errorf("expected nil extensions for empty vir_slot, got %+v", result.Extensions)
	}
}

func TestParseReport_SyntheticJobID(t *testing.T) {
	// LAN-only print: task_id and subtask_id are "0".
	data := []byte(`{"print":{"gcode_state":"PRINTING","subtask_name":"my_model.3mf","task_id":"0","subtask_id":"0","nozzle_temper":200,"bed_temper":60}}`)
	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Job == nil {
		t.Fatal("expected job")
	}
	if result.Job.Name != "my_model.3mf" {
		t.Errorf("Job.Name = %q, want %q", result.Job.Name, "my_model.3mf")
	}
	if result.Job.ID == nil {
		t.Fatal("expected synthetic job ID")
	}
	if !strings.HasPrefix(*result.Job.ID, "lan-") {
		t.Errorf("Job.ID = %q, want lan- prefix", *result.Job.ID)
	}

	// Same name should produce the same synthetic ID (deterministic).
	result2, _ := parseReport(data)
	if *result.Job.ID != *result2.Job.ID {
		t.Errorf("synthetic IDs differ for same input: %q vs %q", *result.Job.ID, *result2.Job.ID)
	}
}

func TestParseReport_RealJobID(t *testing.T) {
	// Cloud print: subtask_id is a real value.
	data := []byte(`{"print":{"gcode_state":"PRINTING","subtask_name":"cube.3mf","task_id":"12345","subtask_id":"893120535","nozzle_temper":200,"bed_temper":60}}`)
	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Job == nil || result.Job.ID == nil {
		t.Fatal("expected job with ID")
	}
	if *result.Job.ID != "893120535" {
		t.Errorf("Job.ID = %q, want %q", *result.Job.ID, "893120535")
	}
}

func TestSDCardState(t *testing.T) {
	tests := []struct {
		name     string
		homeFlag *rawValueString
		want     string
	}{
		{"nil", nil, ""},
		{"no sd card (bits 00)", rawPtr("256"), "normal"}, // 256 = 1 << 8 = bits[8:9]=01
		{"has sd card normal", rawPtr("256"), "normal"},
		{"no sd card", rawPtr("0"), "none"},
		{"abnormal", rawPtr("512"), "abnormal"},           // 512 = 2 << 8
		{"readonly", rawPtr("768"), "readonly"},           // 768 = 3 << 8
		{"with other bits set", rawPtr("1280"), "normal"}, // 1280 = 0x500 = bits[8:9]=01, bit 10 set
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sdCardState(tt.homeFlag)
			if got != tt.want {
				t.Errorf("sdCardState(%v) = %q, want %q", tt.homeFlag, got, tt.want)
			}
		})
	}
}

func rawPtr(s string) *rawValueString {
	v := rawValueString(s)
	return &v
}

func TestHasEMMC(t *testing.T) {
	tests := []struct {
		name string
		fun2 *string
		want bool
	}{
		{"nil", nil, false},
		{"empty", strPtr(""), false},
		{"no emmc", strPtr("0"), false},
		{"has emmc (bit 17)", strPtr("20000"), true},         // 0x20000 = 1 << 17
		{"has emmc with other bits", strPtr("3e0000"), true}, // bit 17 set among others
		{"just below bit 17", strPtr("1ffff"), false},
		{"invalid hex", strPtr("xyz"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasEMMC(tt.fun2)
			if got != tt.want {
				t.Errorf("hasEMMC(%v) = %v, want %v", tt.fun2, got, tt.want)
			}
		})
	}
}

func strPtr(s string) *string { return &s }

func TestParseReport_HomeFlag_SDCardState(t *testing.T) {
	data := []byte(`{"print":{"gcode_state":"IDLE","home_flag":"256","nozzle_temper":24}}`)
	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ext, ok := result.Extensions["bambu-lan"]
	if !ok {
		t.Fatal("expected bambu-lan extension")
	}
	bambu, ok := ext.(*driver.BambuExtension)
	if !ok {
		t.Fatal("expected *driver.BambuExtension")
	}
	if bambu.SDCardState == nil || *bambu.SDCardState != "normal" {
		t.Errorf("SDCardState = %v, want 'normal'", bambu.SDCardState)
	}
}

func TestParseReport_Fun2_EMMCStorage(t *testing.T) {
	data := []byte(`{"print":{"gcode_state":"IDLE","fun2":"20000","nozzle_temper":24}}`)
	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ext, ok := result.Extensions["bambu-lan"]
	if !ok {
		t.Fatal("expected bambu-lan extension")
	}
	bambu, ok := ext.(*driver.BambuExtension)
	if !ok {
		t.Fatal("expected *driver.BambuExtension")
	}
	if bambu.EMMCStorage == nil || !*bambu.EMMCStorage {
		t.Error("expected EMMCStorage = true")
	}
}

func TestParseReport_FirmwareVersion_OtaVersion(t *testing.T) {
	data := []byte(`{"print":{"gcode_state":"IDLE","ota_version":"01.08.00.00","nozzle_temper":24}}`)
	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FirmwareVersion == nil {
		t.Fatal("expected firmware version")
	}
	if *result.FirmwareVersion != "01.08.00.00" {
		t.Errorf("FirmwareVersion = %q, want %q", *result.FirmwareVersion, "01.08.00.00")
	}
}

func TestParseReport_FirmwareVersion_InfoModule(t *testing.T) {
	data := []byte(`{"print":{"gcode_state":"IDLE","nozzle_temper":24},"info":{"module":[{"name":"ota","sw_ver":"01.07.02.00"},{"name":"ams","sw_ver":"00.00.06.32"}]}}`)
	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FirmwareVersion == nil {
		t.Fatal("expected firmware version from info.module")
	}
	if *result.FirmwareVersion != "01.07.02.00" {
		t.Errorf("FirmwareVersion = %q, want %q", *result.FirmwareVersion, "01.07.02.00")
	}
}

func TestParseReport_WifiIP(t *testing.T) {
	data := []byte(`{"print":{"gcode_state":"IDLE","nozzle_temper":24,"wifi_ip":"192.168.1.50"}}`)
	result, err := parseReport(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ext, ok := result.Extensions["bambu-lan"]
	if !ok {
		t.Fatal("expected bambu-lan extension")
	}
	bambu, ok := ext.(*driver.BambuExtension)
	if !ok {
		t.Fatal("expected *driver.BambuExtension")
	}
	if bambu.ReportedIP == nil || *bambu.ReportedIP != "192.168.1.50" {
		t.Errorf("ReportedIP = %v, want '192.168.1.50'", bambu.ReportedIP)
	}
}
