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
	if result.Temperatures != nil && result.Temperatures.Nozzle != nil && result.Temperatures.Nozzle.TargetCelsius != nil {
		t.Errorf("NozzleTarget should be nil when target is 0, got %v", result.Temperatures.Nozzle.TargetCelsius)
	}
	if result.Temperatures != nil && result.Temperatures.Bed != nil && result.Temperatures.Bed.TargetCelsius != nil {
		t.Errorf("BedTarget should be nil when target is 0, got %v", result.Temperatures.Bed.TargetCelsius)
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
				"humidity":"25",
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
	if unit.Humidity == nil || *unit.Humidity != 25 {
		t.Errorf("unit.Humidity = %v, want 25", unit.Humidity)
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
