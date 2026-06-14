# Plan 3: `printer status` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `printer status` — reads live state from a Bambu LAN printer via MQTT and returns a portable `StatusResult`.

**Architecture:** Extend the `Driver` interface with `Capabilities()` and `Status()`; add shared types in `internal/driver/types.go`; implement MQTT status polling in `bambulan.go` using an injected `mqttConn` factory for testability; wire a `printer status` Cobra command following the same dep-injection pattern as `printer add`.

**Tech Stack:** Go 1.26.4, Cobra, paho MQTT v1.5.1, `log/slog` (stdlib), `crypto/tls`, `encoding/json`.

---

## File Map

| File | Action |
|---|---|
| `internal/driver/types.go` | Create — all shared data types |
| `internal/driver/driver.go` | Modify — add `Capabilities()` and `Status()` to interface |
| `internal/drivers/bambulan/bambulan.go` | Modify — add `mqttConn`, `buildTLSConfig`, `fingerprintMismatchError`, `Capabilities()`, `Status()`, `parseReport` and helpers; refactor `ConnectCheck` |
| `internal/drivers/bambulan/bambulan_status_test.go` | Create — internal package tests for `buildTLSConfig`, `parseReport`, `mapState`, `Status` via fakeClient |
| `internal/drivers/bambulan/bambulan_test.go` | Modify — add `Capabilities` test; update `stubDriver` is NOT here (it's in cmd layer) |
| `cmd/printer/add_test.go` | Modify — add `Capabilities()` and `Status()` stubs to `stubDriver` |
| `cmd/printer/status.go` | Create — `printer status` command |
| `cmd/printer/status_test.go` | Create — command-layer tests with stub driver |
| `cmd/printer/printer.go` | Modify — wire `statusCommand()` |

---

## Task 1: Shared types + Driver interface update

**Files:**
- Create: `internal/driver/types.go`
- Modify: `internal/driver/driver.go`
- Modify: `cmd/printer/add_test.go`
- Modify: `internal/drivers/bambulan/bambulan.go` (minimal stubs only)

- [ ] **Step 1: Create `internal/driver/types.go`**

```go
package driver

import "time"

// Capabilities describes which optional operations a driver supports.
type Capabilities struct {
	Status           bool
	Discovery        bool
	JobUpload        bool
	JobStart         bool
	JobPause         bool
	JobCancel        bool
	TemperatureRead  bool
	TemperatureWrite bool
	MotionControl    bool
}

// SecretsBundle carries runtime secrets for a printer connection.
type SecretsBundle struct {
	AccessCode     string // LAN access code
	TLSFingerprint string // "sha256:<hex>"; empty when insecure
}

// ProfileInput carries non-secret profile fields needed for a driver call.
type ProfileInput struct {
	Name     string
	Driver   string
	Host     string
	Serial   string
	Timeout  time.Duration
	Insecure bool
}

// Temperature holds a sensor reading and optional target.
type Temperature struct {
	CurrentCelsius float64  `json:"currentCelsius"`
	TargetCelsius  *float64 `json:"targetCelsius,omitempty"`
}

// Temperatures groups per-sensor temperature readings.
type Temperatures struct {
	Nozzle  *Temperature `json:"nozzle,omitempty"`
	Bed     *Temperature `json:"bed,omitempty"`
	Chamber *Temperature `json:"chamber,omitempty"`
}

// Job describes the currently active print job.
type Job struct {
	Name string `json:"name"`
}

// Progress describes how far through a print job the printer is.
type Progress struct {
	Percent      int  `json:"percent"`
	CurrentLayer *int `json:"currentLayer,omitempty"`
	TotalLayers  *int `json:"totalLayers,omitempty"`
}

// StatusError describes a hardware or firmware error reported by the printer.
type StatusError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StatusWarning describes a non-fatal condition in the status result.
type StatusWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StatusResult is the portable representation of printer state returned by Driver.Status.
// Errors and Warnings are always non-nil slices (serialize as [] not null).
type StatusResult struct {
	State        string          `json:"state"`
	Temperatures *Temperatures   `json:"temperatures"`
	Job          *Job            `json:"job"`
	Progress     *Progress       `json:"progress"`
	Errors       []StatusError   `json:"errors"`
	Warnings     []StatusWarning `json:"warnings"`
	Capabilities Capabilities    `json:"capabilities"`
}
```

- [ ] **Step 2: Update `internal/driver/driver.go` to extend the interface**

Replace the entire file:

```go
package driver

import (
	"context"
	"log/slog"
	"time"
)

// Driver defines the interface every printer driver must satisfy.
type Driver interface {
	// Name returns the driver identifier string (e.g. "bambu-lan").
	Name() string

	// Capabilities returns which optional operations this driver supports.
	Capabilities() Capabilities

	// ConnectCheck verifies that the printer is reachable and credentials are valid.
	// Returns the SHA-256 leaf certificate fingerprint as "sha256:<lowercase-hex>".
	// Returns ("", nil) immediately when insecure is true.
	ConnectCheck(
		ctx context.Context,
		host, serial, accessCode string,
		insecure bool,
		timeout time.Duration,
	) (fingerprint string, err error)

	// Status fetches the current printer state over the driver protocol.
	Status(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
	) (*StatusResult, error)
}
```

- [ ] **Step 3: Verify the build is broken**

Run: `go build ./...`

Expected: compilation errors — `bambulan.Driver` no longer satisfies `driver.Driver` (missing `Capabilities` and `Status`), and `stubDriver` in `cmd/printer/add_test.go` also fails.

- [ ] **Step 4: Add minimal stubs to `internal/drivers/bambulan/bambulan.go`**

Add these two methods after the existing `Name()` method (before `ConnectCheck`):

```go
// Capabilities returns the bambu-lan driver's supported operations.
// Status is implemented; all other capabilities are added in future plans.
func (d *Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{Status: true}
}

// Status fetches current printer state via the Bambu LAN MQTT protocol.
// Implemented in Task 3; this stub satisfies the Driver interface for now.
func (d *Driver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return nil, apperr.New(5, "status not yet implemented")
}
```

Also add the required imports to `bambulan.go` — extend the import block to include:

```go
import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/eclipse/paho.mqtt.golang/packets"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)
```

- [ ] **Step 5: Update `stubDriver` in `cmd/printer/add_test.go`**

Add two new methods to the existing `stubDriver` struct and update its imports:

In the import block, add `"log/slog"` to the existing imports.

After the existing `ConnectCheck` method, add:

```go
func (s *stubDriver) Capabilities() driver.Capabilities { return driver.Capabilities{} }
func (s *stubDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return nil, nil
}
```

- [ ] **Step 6: Verify the build passes and all existing tests pass**

Run: `go test ./...`

Expected: PASS — all existing tests pass; the new stubs satisfy the interface without changing any behaviour.

- [ ] **Step 7: Commit**

```bash
git add internal/driver/types.go internal/driver/driver.go \
        internal/drivers/bambulan/bambulan.go cmd/printer/add_test.go
git commit -m "feat: add driver.Capabilities/Status interface and shared types (Plan 3 Task 1)"
```

---

## Task 2: bambulan refactor — `mqttConn` + `buildTLSConfig` + `Capabilities` tests

**Files:**
- Modify: `internal/drivers/bambulan/bambulan.go`
- Create: `internal/drivers/bambulan/bambulan_status_test.go`
- Modify: `internal/drivers/bambulan/bambulan_test.go`

- [ ] **Step 1: Write failing test for `Capabilities` in `bambulan_test.go`**

Add to the existing `bambulan_test.go` (after `TestConnectCheck_UnreachableHost_ExitsCode4`):

```go
func TestCapabilities_StatusTrue(t *testing.T) {
	caps := bambulan.New().Capabilities()
	if !caps.Status {
		t.Error("Capabilities().Status should be true for bambu-lan driver")
	}
}
```

Run: `go test ./internal/drivers/bambulan/...`

Expected: PASS (the stub from Task 1 already returns `Capabilities{Status: true}`).

- [ ] **Step 2: Create `internal/drivers/bambulan/bambulan_status_test.go` with `mapState` tests**

```go
package bambulan

import "testing"

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
```

Run: `go test ./internal/drivers/bambulan/...`

Expected: FAIL — `mapState` is not defined yet.

- [ ] **Step 3: Refactor `bambulan.go` — add `mqttConn`, `fingerprintMismatchError`, `buildTLSConfig`, `newClient` field, refactor `ConnectCheck`, implement `mapState`**

Replace the entire `bambulan.go` with:

```go
package bambulan

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/eclipse/paho.mqtt.golang/packets"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

// mqttConn is the subset of mqtt.Client used by this driver.
// mqtt.Client already satisfies this interface — no wrapper needed.
type mqttConn interface {
	Connect() mqtt.Token
	Subscribe(topic string, qos byte, callback mqtt.MessageHandler) mqtt.Token
	Publish(topic string, qos byte, retained bool, payload any) mqtt.Token
	Disconnect(quiesce uint)
}

// fingerprintMismatchError is returned by buildTLSConfig's VerifyConnection
// when the presented cert does not match the pinned fingerprint.
type fingerprintMismatchError struct {
	got  string
	want string
}

func (e *fingerprintMismatchError) Error() string {
	return fmt.Sprintf("TLS fingerprint mismatch: got %s, want %s", e.got, e.want)
}

// Driver implements the bambu-lan protocol for Bambu Lab printers.
type Driver struct {
	newClient func(*mqtt.ClientOptions) mqttConn
}

// New returns a bambu-lan Driver backed by a real paho MQTT client.
func New() *Driver {
	return &Driver{
		newClient: func(o *mqtt.ClientOptions) mqttConn { return mqtt.NewClient(o) },
	}
}

func (d *Driver) Name() string { return "bambu-lan" }

// Capabilities returns the bambu-lan driver's supported operations.
func (d *Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{Status: true}
}

// buildTLSConfig returns a TLS config for connecting to a Bambu LAN printer.
// When insecure is false and fingerprint is non-empty, VerifyConnection compares
// the leaf cert's SHA-256 hash against fingerprint and returns fingerprintMismatchError
// on mismatch. When fingerprint is empty (ConnectCheck capture mode), no verification
// callback is set.
func buildTLSConfig(serial, fingerprint string, insecure bool) (*tls.Config, error) {
	cfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Bambu CA not in OS trust stores; leaf cert pinned by TOFU (ADR 0007)
		ServerName:         serial,
	}
	if !insecure && fingerprint != "" {
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return apperr.New(4, "TLS handshake completed but no certificate received")
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			got := "sha256:" + hex.EncodeToString(sum[:])
			if got != fingerprint {
				return &fingerprintMismatchError{got: got, want: fingerprint}
			}
			return nil
		}
	}
	return cfg, nil
}

// ConnectCheck performs a full TLS+MQTT handshake to verify credentials and capture the
// leaf certificate fingerprint. Returns ("", nil) immediately when insecure=true.
//
// Exit codes on error:
//   - 3: MQTT auth rejected
//   - 4: TLS dial failure, network timeout, or context cancelled
func (d *Driver) ConnectCheck(ctx context.Context, host, serial, accessCode string, insecure bool, timeout time.Duration) (string, error) {
	if insecure {
		return "", nil
	}

	tlsCfg, _ := buildTLSConfig(serial, "", false) // capture mode: no fingerprint to check yet

	var (
		mu      sync.Mutex
		leafDER []byte
	)
	tlsCfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) > 0 {
			mu.Lock()
			leafDER = cs.PeerCertificates[0].Raw
			mu.Unlock()
		}
		return nil
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tls://%s:8883", host))
	opts.SetClientID(randomClientID())
	opts.SetUsername("bblp")
	opts.SetPassword(accessCode)
	opts.SetTLSConfig(tlsCfg)
	opts.SetConnectTimeout(timeout)
	opts.SetAutoReconnect(false)
	opts.SetKeepAlive(60)

	client := d.newClient(opts)
	done := make(chan error, 1)
	go func() {
		token := client.Connect()
		token.Wait()
		done <- token.Error()
	}()

	select {
	case err := <-done:
		if err != nil {
			return "", classifyMQTTError(err)
		}
	case <-ctx.Done():
		go client.Disconnect(0)
		return "", apperr.New(4, "connection cancelled")
	}
	client.Disconnect(250)

	mu.Lock()
	raw := make([]byte, len(leafDER))
	copy(raw, leafDER)
	mu.Unlock()

	if len(raw) == 0 {
		return "", apperr.New(4, "TLS handshake completed but no certificate received")
	}

	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Status fetches current printer state via the Bambu LAN MQTT protocol.
// Implemented in Task 3.
func (d *Driver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return nil, apperr.New(5, "status not yet implemented")
}

// classifyMQTTError maps paho connect errors to apperr exit codes.
func classifyMQTTError(err error) error {
	if errors.Is(err, packets.ErrorRefusedBadUsernameOrPassword) ||
		errors.Is(err, packets.ErrorRefusedNotAuthorised) {
		return apperr.Newf(3, "MQTT authentication rejected: %s", err)
	}
	return apperr.Newf(4, "connection failed: %s", err)
}

// classifyStatusError extends classifyMQTTError with fingerprint mismatch handling.
func classifyStatusError(err error) error {
	var fpErr *fingerprintMismatchError
	if errors.As(err, &fpErr) {
		return apperr.Newf(3, "%s", err)
	}
	return classifyMQTTError(err)
}

// mapState converts a Bambu gcode_state string to a portable state name.
func mapState(gcodeState string) string {
	switch gcodeState {
	case "IDLE", "FINISH":
		return "idle"
	case "PRINTING", "PREPARE", "RUNNING", "SLICING":
		return "printing"
	case "PAUSED":
		return "paused"
	case "FAILED":
		return "error"
	default:
		return "unknown"
	}
}

func randomClientID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "polimero-" + hex.EncodeToString(b)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/drivers/bambulan/...`

Expected: PASS — `TestMapState` passes, existing tests pass.

- [ ] **Step 5: Add `buildTLSConfig` tests to `bambulan_status_test.go`**

Add to `internal/drivers/bambulan/bambulan_status_test.go`:

```go
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

	// Construct a fake ConnectionState with a self-signed cert whose fingerprint won't match.
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
```

Also add the following imports and helper to `bambulan_status_test.go`:

```go
package bambulan

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
	"time"
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
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/drivers/bambulan/...`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/drivers/bambulan/bambulan.go \
        internal/drivers/bambulan/bambulan_status_test.go \
        internal/drivers/bambulan/bambulan_test.go
git commit -m "feat: refactor bambulan with mqttConn, buildTLSConfig, and mapState (Plan 3 Task 2)"
```

---

## Task 3: `bambulan.Status` + `parseReport` + tests

**Files:**
- Modify: `internal/drivers/bambulan/bambulan.go`
- Modify: `internal/drivers/bambulan/bambulan_status_test.go`

- [ ] **Step 1: Write failing tests for `parseReport` in `bambulan_status_test.go`**

Add to `bambulan_status_test.go` (these need `context`, `log/slog`, `github.com/polimero-app/cli/internal/apperr`, `github.com/polimero-app/cli/internal/driver` — add them to the import block):

```go
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
```

Run: `go test ./internal/drivers/bambulan/...`

Expected: FAIL — `parseReport` not defined.

- [ ] **Step 2: Implement `parseReport` and helpers in `bambulan.go`**

Add the following to `bambulan.go` (before the `randomClientID` function), also adding `"encoding/json"` to the import block:

```go
// bambuReport is the top-level shape of a Bambu LAN pushall report.
type bambuReport struct {
	Print bambuPrint `json:"print"`
}

type bambuPrint struct {
	GcodeState         string     `json:"gcode_state"`
	NozzleTemper       float64    `json:"nozzle_temper"`
	NozzleTargetTemper float64    `json:"nozzle_target_temper"`
	BedTemper          float64    `json:"bed_temper"`
	BedTargetTemper    float64    `json:"bed_target_temper"`
	ChamberTemper      float64    `json:"chamber_temper"`
	SubtaskName        string     `json:"subtask_name"`
	McPercent          int        `json:"mc_percent"`
	McLayerNum         int        `json:"mc_layer_num"`
	TotalLayerNum      int        `json:"total_layer_num"`
	HMS                []bambuHMS `json:"hms"`
}

type bambuHMS struct {
	Attr uint32 `json:"attr"`
	Code uint32 `json:"code"`
}

// parseReport unmarshals a Bambu pushall report payload into a StatusResult.
// This is a pure function — no network access, safe to unit test with raw bytes.
func parseReport(data []byte) (*driver.StatusResult, error) {
	var rep bambuReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, apperr.Newf(4, "invalid status report: %s", err)
	}
	p := rep.Print
	result := &driver.StatusResult{
		State:        mapState(p.GcodeState),
		Temperatures: mapTemperatures(p),
		Progress:     mapProgress(p),
		Errors:       mapHMSErrors(p),
		Warnings:     []driver.StatusWarning{},
		Capabilities: driver.Capabilities{Status: true},
	}
	if p.SubtaskName != "" {
		result.Job = &driver.Job{Name: p.SubtaskName}
	}
	return result, nil
}

func mapTemperatures(p bambuPrint) *driver.Temperatures {
	nozzleTarget := p.NozzleTargetTemper
	bedTarget := p.BedTargetTemper
	temps := &driver.Temperatures{
		Nozzle: &driver.Temperature{
			CurrentCelsius: p.NozzleTemper,
			TargetCelsius:  &nozzleTarget,
		},
		Bed: &driver.Temperature{
			CurrentCelsius: p.BedTemper,
			TargetCelsius:  &bedTarget,
		},
	}
	if p.ChamberTemper > 0 {
		temps.Chamber = &driver.Temperature{CurrentCelsius: p.ChamberTemper}
	}
	return temps
}

func mapProgress(p bambuPrint) *driver.Progress {
	prog := &driver.Progress{Percent: p.McPercent}
	if p.McLayerNum > 0 {
		v := p.McLayerNum
		prog.CurrentLayer = &v
	}
	if p.TotalLayerNum > 0 {
		v := p.TotalLayerNum
		prog.TotalLayers = &v
	}
	return prog
}

func mapHMSErrors(p bambuPrint) []driver.StatusError {
	errs := make([]driver.StatusError, 0, len(p.HMS))
	for _, h := range p.HMS {
		if h.Attr != 0 || h.Code != 0 {
			errs = append(errs, driver.StatusError{
				Code:    fmt.Sprintf("hms:%08x:%08x", h.Attr, h.Code),
				Message: "hardware error",
			})
		}
	}
	return errs
}
```

- [ ] **Step 3: Run the parseReport tests**

Run: `go test ./internal/drivers/bambulan/... -run TestParseReport`

Expected: PASS.

- [ ] **Step 4: Write failing Status tests using fakeClient**

Add to `bambulan_status_test.go` (update the import block to add `"context"`, `"log/slog"`, and `"github.com/polimero-app/cli/internal/driver"`):

```go
// --- fakeClient helpers ---

type fakeToken struct{ err error }

func (t *fakeToken) Wait() bool                       { return true }
func (t *fakeToken) WaitTimeout(_ time.Duration) bool { return true }
func (t *fakeToken) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
func (t *fakeToken) Error() error { return t.err }

type fakeMessage struct {
	topic   string
	payload []byte
}

func (m *fakeMessage) Duplicate() bool    { return false }
func (m *fakeMessage) Qos() byte          { return 0 }
func (m *fakeMessage) Retained() bool     { return false }
func (m *fakeMessage) Topic() string      { return m.topic }
func (m *fakeMessage) MessageID() uint16  { return 0 }
func (m *fakeMessage) Payload() []byte    { return m.payload }
func (m *fakeMessage) Ack()               {}

// fakeClient is an mqttConn that returns immediately.
// If payload is non-nil, the Subscribe handler is called synchronously with it.
// If connectErr is non-nil, Connect returns that error.
type fakeClient struct {
	connectErr error
	payload    []byte
}

func (f *fakeClient) Connect() mqtt.Token {
	return &fakeToken{err: f.connectErr}
}

func (f *fakeClient) Subscribe(topic string, _ byte, cb mqtt.MessageHandler) mqtt.Token {
	if f.payload != nil {
		cb(nil, &fakeMessage{topic: topic, payload: f.payload})
	}
	return &fakeToken{}
}

func (f *fakeClient) Publish(_ string, _ byte, _ bool, _ any) mqtt.Token {
	return &fakeToken{}
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
	if result.Capabilities.Status != true {
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
```

Run: `go test ./internal/drivers/bambulan/... -run TestStatus`

Expected: FAIL on `TestStatus_HappyPath` (Status returns not-implemented error).

- [ ] **Step 5: Implement `Status` in `bambulan.go`**

Replace the stub `Status` method with the full implementation:

```go
// Status fetches current printer state via the Bambu LAN MQTT protocol.
// Sequence: connect → subscribe report topic → publish pushall → wait for report → parse.
//
// Exit codes on error:
//   - 3: TLS fingerprint mismatch or MQTT auth rejected
//   - 4: network failure, subscribe/publish failure, or context deadline exceeded
func (d *Driver) Status(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	tlsCfg, _ := buildTLSConfig(p.Serial, s.TLSFingerprint, p.Insecure)

	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tls://%s:8883", p.Host))
	opts.SetClientID(randomClientID())
	opts.SetUsername("bblp")
	opts.SetPassword(s.AccessCode)
	opts.SetTLSConfig(tlsCfg)
	opts.SetConnectTimeout(p.Timeout)
	opts.SetAutoReconnect(false)
	opts.SetKeepAlive(60)

	client := d.newClient(opts)

	connectDone := make(chan error, 1)
	go func() {
		token := client.Connect()
		token.Wait()
		connectDone <- token.Error()
	}()

	select {
	case err := <-connectDone:
		if err != nil {
			return nil, classifyStatusError(err)
		}
	case <-ctx.Done():
		go client.Disconnect(0)
		return nil, apperr.New(4, "status check cancelled")
	}
	defer client.Disconnect(250)

	ch := make(chan []byte, 1)
	reportTopic := fmt.Sprintf("device/%s/report", p.Serial)
	requestTopic := fmt.Sprintf("device/%s/request", p.Serial)

	subToken := client.Subscribe(reportTopic, 0, func(_ mqtt.Client, msg mqtt.Message) {
		select {
		case ch <- msg.Payload():
		default: // drop duplicate reports
		}
	})
	subToken.Wait()
	if err := subToken.Error(); err != nil {
		return nil, apperr.Newf(4, "subscribe failed: %s", err)
	}

	const pushall = `{"pushing":{"sequence_id":"1","command":"pushall","version":1,"push_target":1}}`
	pubToken := client.Publish(requestTopic, 0, false, pushall)
	pubToken.Wait()
	if err := pubToken.Error(); err != nil {
		return nil, apperr.Newf(4, "publish failed: %s", err)
	}

	select {
	case data := <-ch:
		return parseReport(data)
	case <-ctx.Done():
		return nil, apperr.New(4, "status check timed out")
	}
}
```

- [ ] **Step 6: Run all bambulan tests**

Run: `go test ./internal/drivers/bambulan/...`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/drivers/bambulan/bambulan.go \
        internal/drivers/bambulan/bambulan_status_test.go
git commit -m "feat: implement bambulan.Status, parseReport, and internal tests (Plan 3 Task 3)"
```

---

## Task 4: `printer status` command + tests + wire up

**Files:**
- Create: `cmd/printer/status.go`
- Create: `cmd/printer/status_test.go`
- Modify: `cmd/printer/printer.go`

- [ ] **Step 1: Write failing command tests in `cmd/printer/status_test.go`**

```go
package printer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/printer"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/spf13/cobra"
)

// stubStatusDriver satisfies driver.Driver for status command tests.
type stubStatusDriver struct {
	result *driver.StatusResult
	err    error
	caps   driver.Capabilities
}

func (s *stubStatusDriver) Name() string                        { return "bambu-lan" }
func (s *stubStatusDriver) Capabilities() driver.Capabilities  { return s.caps }
func (s *stubStatusDriver) ConnectCheck(_ context.Context, _, _, _ string, _ bool, _ time.Duration) (string, error) {
	return "", nil
}
func (s *stubStatusDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return s.result, s.err
}

func defaultStatusDriver() *stubStatusDriver {
	nozzleTarget := 220.0
	bedTarget := 60.0
	layer := 10
	total := 50
	return &stubStatusDriver{
		caps: driver.Capabilities{Status: true},
		result: &driver.StatusResult{
			State: "printing",
			Temperatures: &driver.Temperatures{
				Nozzle: &driver.Temperature{CurrentCelsius: 215.0, TargetCelsius: &nozzleTarget},
				Bed:    &driver.Temperature{CurrentCelsius: 60.0, TargetCelsius: &bedTarget},
			},
			Job:      &driver.Job{Name: "bracket.3mf"},
			Progress: &driver.Progress{Percent: 42, CurrentLayer: &layer, TotalLayers: &total},
			Errors:   []driver.StatusError{},
			Warnings: []driver.StatusWarning{},
			Capabilities: driver.Capabilities{Status: true},
		},
	}
}

func statusDeps(t *testing.T, dir string, kc *keychain.Mock, drv driver.Driver) printer.StatusDeps {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	return printer.StatusDeps{
		KC: kc,
		GetDriver: func(name string) (driver.Driver, bool) {
			if name == "bambu-lan" && drv != nil {
				return drv, true
			}
			return nil, false
		},
		Log: slog.Default(),
	}
}

func testRootForStatus(deps printer.StatusDeps) *cobra.Command {
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	sub := &cobra.Command{Use: "printer"}
	sub.AddCommand(printer.StatusCommandWithDeps(deps))
	root.AddCommand(sub)
	return root
}

func runStatusCmd(t *testing.T, deps printer.StatusDeps, args ...string) (string, error) {
	t.Helper()
	root := testRootForStatus(deps)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"printer", "status"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// --- Tests ---

func TestStatus_NoArgs_ShowsHelp(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	out, err := runStatusCmd(t, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected help output")
	}
}

func TestStatus_TooManyArgs_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "one", "two")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStatus_InvalidProfileName_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "_invalid")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStatus_ProfileNotFound_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "nonexistent")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStatus_MissingAccessCode_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	_ = kc.Delete("polimero", "bambu-lan:myprinter:access-code")
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestStatus_MissingTLSFingerprint_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false) // secure profile
	_ = kc.Delete("polimero", "bambu-lan:myprinter:tls-fingerprint")
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestStatus_InsecureProfile_SkipsFingerprint(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true) // insecure: no fingerprint stored
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "myprinter")
	if err != nil {
		t.Fatalf("expected success for insecure profile, got: %v", err)
	}
}

func TestStatus_InsecureFlag_SkipsFingerprint(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false) // secure profile in config
	_ = kc.Delete("polimero", "bambu-lan:myprinter:tls-fingerprint") // but fingerprint missing
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "myprinter", "--insecure")
	if err != nil {
		t.Fatalf("expected success with --insecure flag, got: %v", err)
	}
}

func TestStatus_CapabilityUnsupported_ExitsCode5(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubStatusDriver{caps: driver.Capabilities{Status: false}}
	deps := statusDeps(t, dir, kc, drv)
	_, err := runStatusCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Errorf("expected exit 5, got %v", err)
	}
}

func TestStatus_AuthFailure_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubStatusDriver{
		caps: driver.Capabilities{Status: true},
		err:  apperr.New(3, "MQTT authentication rejected"),
	}
	deps := statusDeps(t, dir, kc, drv)
	_, err := runStatusCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestStatus_NetworkTimeout_ExitsCode4(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubStatusDriver{
		caps: driver.Capabilities{Status: true},
		err:  apperr.New(4, "status check timed out"),
	}
	deps := statusDeps(t, dir, kc, drv)
	_, err := runStatusCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4, got %v", err)
	}
}

func TestStatus_HumanOutput_FullResult(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	out, err := runStatusCmd(t, deps, "myprinter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"State: printing", "Progress: 42%", "Nozzle:", "Bed:", "Job: bracket.3mf"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
}

func TestStatus_HumanOutput_WithWarnings(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubStatusDriver{
		caps: driver.Capabilities{Status: true},
		result: &driver.StatusResult{
			State:    "idle",
			Errors:   []driver.StatusError{},
			Warnings: []driver.StatusWarning{{Code: "low_filament", Message: "filament running low"}},
			Capabilities: driver.Capabilities{Status: true},
		},
	}
	deps := statusDeps(t, dir, kc, drv)
	out, err := runStatusCmd(t, deps, "myprinter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains([]byte(out), []byte("filament running low")) {
		t.Errorf("expected warning in output:\n%s", out)
	}
}

func TestStatus_JSON_Envelope(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	out, err := runStatusCmd(t, deps, "myprinter", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	if env["ok"] != true {
		t.Errorf("ok = %v, want true", env["ok"])
	}
	if env["data"] == nil {
		t.Error("data must be present")
	}
	meta, ok := env["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is %T, want map", env["meta"])
	}
	if meta["command"] != "printer status" {
		t.Errorf("meta.command = %v, want printer status", meta["command"])
	}
	if meta["durationMs"] == nil {
		t.Error("meta.durationMs must be present for successful status call")
	}
}
```

Run: `go test ./cmd/printer/... -run TestStatus`

Expected: FAIL — `printer.StatusCommandWithDeps` does not exist yet.

- [ ] **Step 2: Create `cmd/printer/status.go`**

```go
package printer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/spf13/cobra"
)

// StatusDeps holds injectable dependencies for the printer status command.
type StatusDeps struct {
	KC        keychain.Keychain
	GetDriver func(string) (driver.Driver, bool)
	Log       *slog.Logger
}

func statusCommand() *cobra.Command {
	return StatusCommandWithDeps(StatusDeps{
		KC:        keychain.NewReal(),
		GetDriver: drivers.Get,
		Log:       slog.Default(),
	})
}

// StatusCommandWithDeps constructs the "status" cobra command with injected dependencies.
func StatusCommandWithDeps(deps StatusDeps) *cobra.Command {
	var flags struct {
		timeout  string
		insecure bool
	}

	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show the current status of a printer",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if len(args) > 1 {
				return writeStatusUsageError(cmd, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runStatus(cmd, args[0], flags.timeout, flags.insecure, deps)
		},
	}
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	return cmd
}

func writeStatusUsageError(cmd *cobra.Command, message string) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}
	return writeStatusError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, apperr.New(2, message))
}

func runStatus(cmd *cobra.Command, nameArg, timeoutFlag string, insecureFlag bool, deps StatusDeps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}

	result, durationMs, err := doStatus(cmd, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeStatusError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, err)
	}
	return writeStatusSuccess(cmd.OutOrStdout(), format, nameArg, result, durationMs)
}

func doStatus(cmd *cobra.Command, nameArg, timeoutFlag string, insecureFlag bool, deps StatusDeps) (*driver.StatusResult, int64, error) {
	name := strings.ToLower(nameArg)
	if err := validateProfileName(name); err != nil {
		return nil, 0, err
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return nil, 0, apperr.Newf(1, "cannot resolve config directory: %s", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return nil, 0, apperr.Newf(2, "cannot load config: %s", err)
	}
	p, ok := cfg.GetProfile(name)
	if !ok {
		return nil, 0, apperr.Newf(2, "printer profile %q not found", name)
	}

	timeoutStr := p.Timeout
	if timeoutFlag != "" {
		timeoutStr = timeoutFlag
	}
	if timeoutStr == "" {
		timeoutStr = "10s"
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return nil, 0, apperr.Newf(2, "invalid --timeout %q: %s", timeoutStr, err)
	}
	if timeout <= 0 {
		return nil, 0, apperr.Newf(2, "--timeout must be greater than zero")
	}

	insecure := p.Insecure || insecureFlag

	kcAcct := fmt.Sprintf("%s:%s:access-code", p.Driver, name)
	accessCode, err := deps.KC.Get("polimero", kcAcct)
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return nil, 0, apperr.Newf(3, "access code not found in keychain for %q", name)
		}
		return nil, 0, apperr.Newf(3, "cannot read access code from keychain: %s", err)
	}

	var tlsFingerprint string
	if !insecure {
		kcFpAcct := fmt.Sprintf("%s:%s:tls-fingerprint", p.Driver, name)
		tlsFingerprint, err = deps.KC.Get("polimero", kcFpAcct)
		if err != nil {
			if errors.Is(err, keychain.ErrNotFound) {
				return nil, 0, apperr.Newf(3, "TLS fingerprint not found in keychain for %q", name)
			}
			return nil, 0, apperr.Newf(3, "cannot read TLS fingerprint from keychain: %s", err)
		}
	}

	drv, ok := deps.GetDriver(p.Driver)
	if !ok {
		return nil, 0, apperr.Newf(2, "unknown driver %q", p.Driver)
	}
	if !drv.Capabilities().Status {
		return nil, 0, apperr.Newf(5, "driver %q does not support the status command", p.Driver)
	}

	pi := driver.ProfileInput{
		Name:     name,
		Driver:   p.Driver,
		Host:     p.Host,
		Serial:   p.Serial,
		Timeout:  timeout,
		Insecure: insecure,
	}
	secrets := driver.SecretsBundle{
		AccessCode:     accessCode,
		TLSFingerprint: tlsFingerprint,
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	start := time.Now()
	result, err := drv.Status(ctx, pi, secrets, deps.Log)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		return nil, 0, err
	}
	return result, durationMs, nil
}

func writeStatusSuccess(w io.Writer, format output.Format, name string, result *driver.StatusResult, durationMs int64) error {
	if format == output.FormatJSON {
		dm := durationMs
		return output.WriteEnvelope(w, output.Envelope{
			OK:    true,
			Data:  result,
			Error: nil,
			Meta:  output.Meta{Command: "printer status", DurationMs: &dm},
		})
	}
	lines := []string{
		fmt.Sprintf("Printer: %s", name),
		fmt.Sprintf("State: %s", result.State),
	}
	if result.Progress != nil {
		lines = append(lines, fmt.Sprintf("Progress: %d%%", result.Progress.Percent))
	}
	if result.Temperatures != nil {
		if n := result.Temperatures.Nozzle; n != nil {
			if n.TargetCelsius != nil {
				lines = append(lines, fmt.Sprintf("Nozzle: %.1f °C / %.1f °C", n.CurrentCelsius, *n.TargetCelsius))
			} else {
				lines = append(lines, fmt.Sprintf("Nozzle: %.1f °C", n.CurrentCelsius))
			}
		}
		if b := result.Temperatures.Bed; b != nil {
			if b.TargetCelsius != nil {
				lines = append(lines, fmt.Sprintf("Bed: %.1f °C / %.1f °C", b.CurrentCelsius, *b.TargetCelsius))
			} else {
				lines = append(lines, fmt.Sprintf("Bed: %.1f °C", b.CurrentCelsius))
			}
		}
		if c := result.Temperatures.Chamber; c != nil {
			lines = append(lines, fmt.Sprintf("Chamber: %.1f °C", c.CurrentCelsius))
		}
	}
	if result.Job != nil {
		lines = append(lines, fmt.Sprintf("Job: %s", result.Job.Name))
	}
	for _, warn := range result.Warnings {
		lines = append(lines, fmt.Sprintf("Warning: %s", warn.Message))
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}

func writeStatusError(out, errOut io.Writer, format output.Format, err error) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &output.ErrDetail{Code: statusErrorCode(err), Message: err.Error()},
			Meta:  output.Meta{Command: "printer status"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", err)
	}
	return apperr.New(code, "")
}

func statusErrorCode(err error) string {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		return "error"
	}
	switch exitErr.Code {
	case 2:
		return "config_error"
	case 3:
		return "auth_error"
	case 4:
		return "network_error"
	case 5:
		return "capability_unsupported"
	default:
		return "error"
	}
}
```

- [ ] **Step 3: Wire `statusCommand()` into `cmd/printer/printer.go`**

```go
package printer

import "github.com/spf13/cobra"

// Command returns the "printer" subcommand with all its children attached.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "printer",
		Short: "Manage 3D printer profiles",
	}
	cmd.AddCommand(driversCommand())
	cmd.AddCommand(listCommand())
	cmd.AddCommand(addCommand())
	cmd.AddCommand(removeCommand())
	cmd.AddCommand(statusCommand())
	return cmd
}
```

- [ ] **Step 4: Run all tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 5: Run full CI check**

Run: `make ci`

Expected: PASS — lint and tests green.

- [ ] **Step 6: Commit**

```bash
git add cmd/printer/status.go cmd/printer/status_test.go cmd/printer/printer.go
git commit -m "feat: implement printer status command with dep injection and tests (Plan 3 Task 4)"
```
