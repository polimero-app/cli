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
	cfg, err := buildTLSConfig("SN001", "sha256:aabbcc", true)
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

func TestBuildTLSConfig_EmptyFingerprint_NoVerifyConnection(t *testing.T) {
	cfg, err := buildTLSConfig("SN001", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VerifyConnection != nil {
		t.Error("VerifyConnection should be nil when fingerprint is empty (capture mode)")
	}
}

func TestBuildTLSConfig_Mismatch_ReturnsFingerprintMismatchError(t *testing.T) {
	cfg, err := buildTLSConfig("SN001", "sha256:expectedfingerprint", false)
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
	if fpErr.want != "sha256:expectedfingerprint" {
		t.Errorf("want = %q, expected sha256:expectedfingerprint", fpErr.want)
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

func (t *fakeToken) Wait() bool                       { return true }
func (t *fakeToken) WaitTimeout(_ time.Duration) bool { return true }
func (t *fakeToken) Done() <-chan struct{}             { return t.done }
func (t *fakeToken) Error() error                     { return t.err }

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
	connectErr error
	payload    []byte
}

func (f *fakeClient) Connect() mqtt.Token {
	return newFakeToken(f.connectErr)
}

func (f *fakeClient) Subscribe(topic string, _ byte, cb mqtt.MessageHandler) mqtt.Token {
	if f.payload != nil {
		cb(nil, &fakeMessage{topic: topic, payload: f.payload})
	}
	return newFakeToken(nil)
}

func (f *fakeClient) Publish(_ string, _ byte, _ bool, _ any) mqtt.Token {
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
	fc := &fakeClient{connectErr: &fingerprintMismatchError{got: "sha256:aabbcc", want: "sha256:112233"}}
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
