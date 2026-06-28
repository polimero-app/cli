package bambulan

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/eclipse/paho.mqtt.golang/packets"
	zeroconf "github.com/grandcat/zeroconf"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/protocoltrace"
)

// mqttConn is the subset of mqtt.Client used by this driver.
// mqtt.Client already satisfies this interface — no wrapper needed.
type mqttConn interface {
	Connect() mqtt.Token
	Subscribe(topic string, qos byte, callback mqtt.MessageHandler) mqtt.Token
	Publish(topic string, qos byte, retained bool, payload any) mqtt.Token
	Disconnect(quiesce uint)
}

// mdnsEntry is the bambulan-internal representation of a single mDNS service response.
type mdnsEntry struct {
	Host string
	Port int
	Text []string // raw "key=value" TXT records
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
	newClient           func(*mqtt.ClientOptions) mqttConn
	dialTLS             func(ctx context.Context, addr string, cfg *tls.Config) (*tls.Conn, error)
	dialRTSPSFn         func(ctx context.Context, tlsCfg *tls.Config, host, accessCode string) (io.ReadCloser, error)
	captureH264Snapshot func(ctx context.Context, tlsCfg *tls.Config, host, accessCode string) ([]byte, error)
	browse              func(ctx context.Context, service string) (<-chan *mdnsEntry, error)
	browseSSDP          func(ctx context.Context) (<-chan *mdnsEntry, error)
	browseUDP           func(ctx context.Context) (<-chan *mdnsEntry, error)
	dialFTP             ftpDialer
}

// New returns a bambu-lan Driver backed by a real paho MQTT client.
func New() *Driver {
	return &Driver{
		newClient: func(o *mqtt.ClientOptions) mqttConn { return mqtt.NewClient(o) },
		dialTLS: func(ctx context.Context, addr string, cfg *tls.Config) (*tls.Conn, error) {
			dialer := &tls.Dialer{Config: cfg}
			conn, err := dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			return conn.(*tls.Conn), nil
		},
		dialRTSPSFn:         dialRTSPS,
		captureH264Snapshot: captureRTSPSH264Snapshot,
		browse:              realBrowse,
		browseSSDP:          realBrowseSSDP,
		browseUDP:           realBrowseUDP,
	}
}

const bambuMDNSService = "_bambu._tcp"

func realBrowse(ctx context.Context, service string) (<-chan *mdnsEntry, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, err
	}
	raw := make(chan *zeroconf.ServiceEntry)
	if err := resolver.Browse(ctx, service, "local.", raw); err != nil {
		return nil, err
	}
	out := make(chan *mdnsEntry)
	go func() {
		defer close(out)
		for e := range raw {
			var host string
			if len(e.AddrIPv4) > 0 {
				host = e.AddrIPv4[0].String()
			} else if len(e.AddrIPv6) > 0 {
				host = e.AddrIPv6[0].String()
			}
			if host == "" {
				continue
			}
			select {
			case out <- &mdnsEntry{Host: host, Port: e.Port, Text: e.Text}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (d *Driver) Name() string { return "bambu-lan" }

// Capabilities returns the bambu-lan driver's supported operations.
func (d *Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{
		Status:           true,
		TLSRefresh:       true,
		Discovery:        true,
		CameraStream:     true,
		CameraSnapshot:   true,
		FileList:         true,
		FileDownload:     true,
		FileUpload:       true,
		JobStart:         true,
		JobPause:         true,
		JobResume:        true,
		JobCancel:        true,
		TemperatureWrite: true,
		MotionControl:    true,
	}
}

func buildCaptureTLSConfig(serial string) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Bambu CA not in OS trust stores; leaf cert pinned by TOFU (ADR 0007)
		ServerName:         serial,
	}
}

// buildTLSConfig returns a TLS config for connecting to a Bambu LAN printer.
// When insecure is false, VerifyConnection compares
// the leaf cert's SHA-256 hash against fingerprint and returns fingerprintMismatchError
// on mismatch.
func buildTLSConfig(serial, fingerprint string, insecure bool) (*tls.Config, error) {
	cfg := buildCaptureTLSConfig(serial)
	if insecure {
		return cfg, nil
	}
	if !driver.ValidTLSFingerprint(fingerprint) {
		return nil, apperr.New(3, "TLS fingerprint is missing or invalid")
	}
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
	return cfg, nil
}

// ValidateProfile checks bambu-lan-specific profile requirements.
func (d *Driver) ValidateProfile(p driver.ProfileInput) error {
	if p.Serial == "" {
		return apperr.New(2, "--serial is required for bambu-lan driver")
	}
	if len(p.Serial) > 64 {
		return apperr.Newf(2, "--serial too long (max 64 chars)")
	}
	for _, c := range p.Serial {
		if c < 0x21 || c > 0x7E {
			return apperr.Newf(2, "--serial contains invalid character (must be printable ASCII with no whitespace)")
		}
	}
	return nil
}

// ConnectCheck performs a full TLS+MQTT handshake to verify credentials and capture the
// leaf certificate fingerprint. Returns ("", nil) immediately when p.Insecure is true.
//
// Exit codes on error:
//   - 3: MQTT auth rejected
//   - 4: TLS dial failure, network timeout, or context cancelled
func (d *Driver) ConnectCheck(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle) (string, error) {
	if p.Insecure {
		return "", nil
	}

	trace := protocoltrace.FromContext(ctx)
	endpoint := fmt.Sprintf("%s:8883", p.Host)

	tlsCfg := buildCaptureTLSConfig(p.Serial)

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
	opts.AddBroker(fmt.Sprintf("tls://%s", endpoint))
	opts.SetClientID(randomClientID())
	opts.SetUsername("bblp")
	opts.SetPassword(s.AccessCode)
	opts.SetTLSConfig(tlsCfg)
	opts.SetConnectTimeout(p.Timeout)
	opts.SetAutoReconnect(false)
	opts.SetKeepAlive(60)

	client := d.newClient(opts)
	connectStart := time.Now()
	if err := waitMQTTToken(ctx, client.Connect()); err != nil {
		dur := time.Since(connectStart).Milliseconds()
		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     "ConnectCheck",
			Phase:         "connect",
			Transport:     "mqtt",
			Endpoint:      endpoint,
			Protocol:      "mqttv3.1.1",
			DurationMs:    &dur,
			ErrorCategory: classifyTraceError(err),
		})
		if isContextDoneErr(err) {
			// Use a short quiesce so the DISCONNECT frame is flushed.
			// P1-series printers have very few MQTT session slots; a
			// dropped DISCONNECT leaves a ghost that blocks the next
			// connect for ~60 s (one keepalive period).
			go client.Disconnect(150)
			return "", apperr.New(4, "connection cancelled")
		}
		return "", classifyMQTTError(err)
	}
	connectDur := time.Since(connectStart).Milliseconds()
	trace.Emit(protocoltrace.Event{
		Timestamp:  time.Now().UTC(),
		Driver:     "bambu-lan",
		Operation:  "ConnectCheck",
		Phase:      "connect",
		Transport:  "mqtt",
		Endpoint:   endpoint,
		Protocol:   "mqttv3.1.1",
		DurationMs: &connectDur,
	})
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
// Sequence: connect → subscribe report topic → publish pushall → wait for report → parse.
//
// Exit codes on error:
//   - 3: TLS fingerprint mismatch or MQTT auth rejected
//   - 4: network failure, subscribe/publish failure, or context deadline exceeded
func (d *Driver) Status(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	trace := protocoltrace.FromContext(ctx)
	endpoint := fmt.Sprintf("%s:8883", p.Host)

	tlsCfg, err := buildTLSConfig(p.Serial, s.TLSFingerprint, p.Insecure)
	if err != nil {
		return nil, err
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tls://%s", endpoint))
	opts.SetClientID(randomClientID())
	opts.SetUsername("bblp")
	opts.SetPassword(s.AccessCode)
	opts.SetTLSConfig(tlsCfg)
	opts.SetConnectTimeout(p.Timeout)
	opts.SetAutoReconnect(false)
	opts.SetKeepAlive(60)

	client, err := d.connectMQTTWithRetry(ctx, opts, trace, "Status", endpoint)
	if err != nil {
		if isContextDoneErr(err) {
			return nil, statusContextError(err)
		}
		return nil, classifyStatusError(err)
	}
	defer client.Disconnect(250)

	ch := make(chan []byte, 8)
	reportTopic := fmt.Sprintf("device/%s/report", p.Serial)
	requestTopic := fmt.Sprintf("device/%s/request", p.Serial)

	subStart := time.Now()
	subToken := client.Subscribe(reportTopic, 0, func(_ mqtt.Client, msg mqtt.Message) {
		payload := make([]byte, len(msg.Payload()))
		copy(payload, msg.Payload())
		select {
		case ch <- payload:
		default:
		}
	})
	if err := waitMQTTToken(ctx, subToken); err != nil {
		dur := time.Since(subStart).Milliseconds()
		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     "Status",
			Phase:         "subscribe",
			Transport:     "mqtt",
			Endpoint:      endpoint,
			DurationMs:    &dur,
			ErrorCategory: classifyTraceError(err),
		})
		if isContextDoneErr(err) {
			return nil, statusContextError(err)
		}
		return nil, apperr.Wrap(4, "status subscription failed", err)
	}
	subDur := time.Since(subStart).Milliseconds()
	trace.Emit(protocoltrace.Event{
		Timestamp:  time.Now().UTC(),
		Driver:     "bambu-lan",
		Operation:  "Status",
		Phase:      "subscribe",
		Transport:  "mqtt",
		Endpoint:   endpoint,
		DurationMs: &subDur,
	})

	pubStart := time.Now()
	const pushall = `{"pushing":{"sequence_id":"1","command":"pushall","version":1,"push_target":1}}`
	pubToken := client.Publish(requestTopic, 0, false, pushall)
	if err := waitMQTTToken(ctx, pubToken); err != nil {
		dur := time.Since(pubStart).Milliseconds()
		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     "Status",
			Phase:         "publish",
			Transport:     "mqtt",
			Endpoint:      endpoint,
			DurationMs:    &dur,
			Payload:       json.RawMessage(pushall),
			ErrorCategory: classifyTraceError(err),
		})
		if isContextDoneErr(err) {
			return nil, statusContextError(err)
		}
		return nil, apperr.Wrap(4, "status request failed", err)
	}
	pubDur := time.Since(pubStart).Milliseconds()
	trace.Emit(protocoltrace.Event{
		Timestamp:  time.Now().UTC(),
		Driver:     "bambu-lan",
		Operation:  "Status",
		Phase:      "publish",
		Transport:  "mqtt",
		Endpoint:   endpoint,
		DurationMs: &pubDur,
		Payload:    json.RawMessage(pushall),
	})

	receiveStart := time.Now()
	for {
		select {
		case data := <-ch:
			if isPushallReport(data) {
				dur := time.Since(receiveStart).Milliseconds()
				bc := int64(len(data))
				trace.Emit(protocoltrace.Event{
					Timestamp:  time.Now().UTC(),
					Driver:     "bambu-lan",
					Operation:  "Status",
					Phase:      "receive",
					Transport:  "mqtt",
					Endpoint:   endpoint,
					DurationMs: &dur,
					ByteCount:  &bc,
					Payload:    json.RawMessage(data),
				})
				result, parseErr := parseReport(data)
				emitParseEvent(trace, data, result)
				return result, parseErr
			}
		case <-ctx.Done():
			dur := time.Since(receiveStart).Milliseconds()
			trace.Emit(protocoltrace.Event{
				Timestamp:     time.Now().UTC(),
				Driver:        "bambu-lan",
				Operation:     "Status",
				Phase:         "receive",
				Transport:     "mqtt",
				Endpoint:      endpoint,
				DurationMs:    &dur,
				ErrorCategory: "timeout",
			})
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil, apperr.New(4, "status check cancelled")
			}
			return nil, apperr.New(4, "status check timed out")
		}
	}
}

// emitParseEvent emits a "parse" phase trace event with response key inventory
// and any parser warnings from the status result.
func emitParseEvent(trace protocoltrace.Sink, data []byte, result *driver.StatusResult) {
	keys := extractTopLevelKeys(data)
	var warning string
	if result != nil && len(result.Warnings) > 0 {
		codes := make([]string, 0, len(result.Warnings))
		for _, w := range result.Warnings {
			codes = append(codes, w.Code)
		}
		warning = strings.Join(codes, ", ")
	}
	ev := protocoltrace.Event{
		Timestamp: time.Now().UTC(),
		Driver:    "bambu-lan",
		Operation: "Status",
		Phase:     "parse",
		Keys:      keys,
	}
	if warning != "" {
		ev.Warning = warning
	}
	trace.Emit(ev)
}

// extractTopLevelKeys returns the keys from the "print" object in a Bambu report.
func extractTopLevelKeys(data []byte) []string {
	var raw struct {
		Print map[string]json.RawMessage `json:"print"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.Print == nil {
		return nil
	}
	keys := make([]string, 0, len(raw.Print))
	for k := range raw.Print {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// isPushallReport returns true when data contains a full pushall response.
// Delta reports from P1/A1 autonomous pushes omit print.gcode_state and must
// be skipped — accepting them can yield stale or partial status.
//
// Uses a minimal struct to avoid UnmarshalTypeError from fields whose JSON
// type varies across printer families (e.g. H2C sends "stg" as an array).
func isPushallReport(data []byte) bool {
	var rep struct {
		Print *struct {
			GcodeState *string `json:"gcode_state"`
		} `json:"print"`
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		return false
	}
	return rep.Print != nil && rep.Print.GcodeState != nil
}

func waitMQTTToken(ctx context.Context, token mqtt.Token) error {
	if token == nil {
		return errors.New("MQTT operation failed")
	}
	select {
	case <-token.Done():
		return token.Error()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isContextDoneErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// isMQTTAuthError reports whether the error is an MQTT authentication refusal
// (wrong access code or unauthorized). Auth errors must never be retried.
func isMQTTAuthError(err error) bool {
	return errors.Is(err, packets.ErrorRefusedBadUsernameOrPassword) ||
		errors.Is(err, packets.ErrorRefusedNotAuthorised)
}

// isFingerprintMismatch reports whether the error is a TLS fingerprint mismatch.
func isFingerprintMismatch(err error) bool {
	var fpErr *fingerprintMismatchError
	return errors.As(err, &fpErr)
}

const (
	mqttRetryAttempts = 3
	mqttRetryBaseWait = 500 * time.Millisecond
)

// connectMQTTWithRetry creates a fresh MQTT client and attempts to connect,
// retrying transient failures with exponential backoff. P1-series printers
// can temporarily refuse connections for ~1 s after a prior session
// disconnects; retrying avoids surfacing this as a user-visible error.
//
// Auth errors and TLS fingerprint mismatches are never retried.
func (d *Driver) connectMQTTWithRetry(
	ctx context.Context,
	opts *mqtt.ClientOptions,
	trace protocoltrace.Sink,
	operation string,
	endpoint string,
) (mqttConn, error) {
	var lastErr error
	wait := mqttRetryBaseWait

	for attempt := 1; attempt <= mqttRetryAttempts; attempt++ {
		client := d.newClient(opts)
		connectStart := time.Now()
		err := waitMQTTToken(ctx, client.Connect())
		dur := time.Since(connectStart).Milliseconds()

		if err == nil {
			if attempt > 1 {
				trace.Emit(protocoltrace.Event{
					Timestamp:  time.Now().UTC(),
					Driver:     "bambu-lan",
					Operation:  operation,
					Phase:      "connect",
					Transport:  "mqtt",
					Endpoint:   endpoint,
					Protocol:   "mqttv3.1.1",
					DurationMs: &dur,
					Detail:     map[string]any{"attempt": attempt},
				})
			} else {
				trace.Emit(protocoltrace.Event{
					Timestamp:  time.Now().UTC(),
					Driver:     "bambu-lan",
					Operation:  operation,
					Phase:      "connect",
					Transport:  "mqtt",
					Endpoint:   endpoint,
					Protocol:   "mqttv3.1.1",
					DurationMs: &dur,
				})
			}
			return client, nil
		}

		lastErr = err

		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     operation,
			Phase:         "connect",
			Transport:     "mqtt",
			Endpoint:      endpoint,
			Protocol:      "mqttv3.1.1",
			DurationMs:    &dur,
			ErrorCategory: classifyTraceError(err),
		})

		// Never retry context cancellation, auth errors, or TLS mismatches.
		if isContextDoneErr(err) || isMQTTAuthError(err) || isFingerprintMismatch(err) {
			go client.Disconnect(150)
			return nil, err
		}

		go client.Disconnect(150)

		if attempt < mqttRetryAttempts {
			select {
			case <-time.After(wait):
				wait *= 2
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return nil, lastErr
}

func statusContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return apperr.New(4, "status check cancelled")
	}
	return apperr.New(4, "status check timed out")
}

// classifyTraceError returns a sanitized error category for trace events.
// Never includes raw error messages or secrets.
func classifyTraceError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, packets.ErrorRefusedBadUsernameOrPassword) ||
		errors.Is(err, packets.ErrorRefusedNotAuthorised) {
		return "auth_rejected"
	}
	return "connection_error"
}

// classifyMQTTError maps paho connect errors to apperr exit codes.
func classifyMQTTError(err error) error {
	if errors.Is(err, packets.ErrorRefusedBadUsernameOrPassword) ||
		errors.Is(err, packets.ErrorRefusedNotAuthorised) {
		return apperr.Wrap(3, "MQTT authentication rejected", err)
	}
	return apperr.Wrap(4, "connection failed", err)
}

// classifyStatusError extends classifyMQTTError with fingerprint mismatch handling.
func classifyStatusError(err error) error {
	var fpErr *fingerprintMismatchError
	if errors.As(err, &fpErr) {
		return apperr.Wrap(3, "TLS fingerprint mismatch", err)
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

// bambuReport is the top-level shape of a Bambu LAN pushall report.
type bambuReport struct {
	Print        *bambuPrint        `json:"print"`
	LightsReport []bambuLightReport `json:"lights_report"`

	// Some firmware versions include info or upgrade at the top level.
	Info *bambuInfo `json:"info"`
}

// bambuInfo holds firmware version data from get_version responses or
// pushall reports that include info at the top level.
type bambuInfo struct {
	Module []bambuModule `json:"module"`
}

// bambuModule represents a single firmware module in version reports.
type bambuModule struct {
	Name    string `json:"name"`
	Version string `json:"sw_ver"`
}

type bambuLightReport struct {
	Node string `json:"node"`
	Mode string `json:"mode"`
}

type bambuPrint struct {
	GcodeState         *string         `json:"gcode_state"`
	NozzleTemper       *rawValueString `json:"nozzle_temper"`
	NozzleTargetTemper *rawValueString `json:"nozzle_target_temper"`
	BedTemper          *rawValueString `json:"bed_temper"`
	BedTargetTemper    *rawValueString `json:"bed_target_temper"`
	ChamberTemper      *rawValueString `json:"chamber_temper"`
	SubtaskName        *string         `json:"subtask_name"`
	GcodeFile          *string         `json:"gcode_file"`
	McPercent          *rawValueString `json:"mc_percent"`
	LayerNum           *rawValueString `json:"layer_num"`
	McLayerNum         *rawValueString `json:"mc_layer_num"`
	TotalLayerNum      *rawValueString `json:"total_layer_num"`
	McPrintErrorCode   *rawValueString `json:"mc_print_error_code"`
	HMS                []bambuHMS      `json:"hms"`

	// Extended fields for --detailed status.
	BigFan1Speed      *rawValueString `json:"big_fan1_speed"`
	BigFan2Speed      *rawValueString `json:"big_fan2_speed"`
	CoolingFanSpeed   *rawValueString `json:"cooling_fan_speed"`
	HeatbreakFanSpeed *rawValueString `json:"heatbreak_fan_speed"`

	McRemainingTime         *rawValueString `json:"mc_remaining_time"`
	PrintedTime             *rawValueString `json:"mc_print_line_number"` // not used; see below
	SpdLvl                  *rawValueString `json:"spd_lvl"`
	SpdMag                  *rawValueString `json:"spd_mag"`
	WifiSignal              *rawValueString `json:"wifi_signal"`
	GcodeFilePreparePercent *rawValueString `json:"gcode_file_prepare_percent"`

	// Stg type varies across families: int on X1/P1/A1, array on H2C.
	// Not used in mappings (mapStage uses StgCur); accept any JSON value.
	Stg    json.RawMessage `json:"stg"`
	StgCur *rawValueString `json:"stg_cur"`

	NozzleDiameter  *rawValueString `json:"nozzle_diameter"`
	TotalLayerCount *rawValueString `json:"total_layer_num_bak"` // not used; total_layer_num preferred
	FileSize        *rawValueString `json:"file_size"`           // not present in all FW; treat as optional

	TimelapseStat *string `json:"ipcam_record_timelapse"`

	// H2C nests timelapse inside an ipcam object.
	Ipcam *bambuIpcam `json:"ipcam"`

	// Time fields.
	RemainTime    *rawValueString `json:"remain_time"`        // H2C uses this instead of mc_remaining_time
	PrintRealTime *rawValueString `json:"mc_print_real_time"` // not always present
	PrepareTime   *rawValueString `json:"mc_prepare_time"`    // not always present

	// AMS data (nested inside print in the pushall response).
	AMS *bambuAMS `json:"ams"`

	// External spool holder (A1 Mini uses vt_tray, H2C uses vir_slot).
	VtTray  *bambuVtTray  `json:"vt_tray"`
	VirSlot []bambuVtTray `json:"vir_slot"`

	// Z position.
	ZOffset *rawValueString `json:"z_offset"` // not the current Z; see below

	// G-code line tracking.
	CurLineNum   *rawValueString `json:"cur_line_num"`
	TotalLineNum *rawValueString `json:"total_line_num"`

	// BedType (plate type identifier).
	BedType *rawValueString `json:"bed_type"`

	// LightsReport is nested inside "print" on some families (H2C).
	// On X1/P1/A1 it appears at the top level of the report instead.
	LightsReport []bambuLightReport `json:"lights_report"`

	// Job identity fields — used for synthetic ID generation (item #7).
	// LAN-only prints report these as "0" or empty; cloud prints carry real IDs.
	TaskID    *string `json:"task_id"`
	SubtaskID *string `json:"subtask_id"`

	// Capability fields (items #8).
	// home_flag: bits [8:9] encode SD card state (0=none, 1=normal, 2=abnormal, 3=readonly).
	// fun2: hex-string capability bitmask; bit 17 = internal storage (eMMC) support.
	HomeFlag *rawValueString `json:"home_flag"`
	Fun2     *string         `json:"fun2"`

	// Firmware version (item #4) — not all firmware includes this in pushall.
	OtaVersion *string `json:"ota_version"`

	// Network fields (item #10) — present in some firmware versions.
	WifiIP *string `json:"wifi_ip"`
}

type bambuAMS struct {
	AMS          []bambuAMSUnit  `json:"ams"`
	AMSExistBits *rawValueString `json:"ams_exist_bits"`
	TrayNow      *rawValueString `json:"tray_now"`
}

type bambuAMSUnit struct {
	ID       rawValueString `json:"id"`
	Humidity rawValueString `json:"humidity"`
	Temp     rawValueString `json:"temp"`
	Tray     []bambuAMSTray `json:"tray"`
}

type bambuAMSTray struct {
	ID            rawValueString  `json:"id"`
	TrayType      *string         `json:"tray_type"`
	TrayColor     *string         `json:"tray_color"`
	Remain        *rawValueString `json:"remain"`
	NozzleTempMin *rawValueString `json:"nozzle_temp_min"`
	NozzleTempMax *rawValueString `json:"nozzle_temp_max"`
}

type bambuHMS struct {
	Attr rawValueString `json:"attr"`
	Code rawValueString `json:"code"`
}

// bambuVtTray represents an external spool holder (vt_tray on A1 Mini)
// or a virtual filament slot (vir_slot on H2C).
type bambuVtTray struct {
	ID            rawValueString  `json:"id"`
	TrayType      *string         `json:"tray_type"`
	TrayColor     *string         `json:"tray_color"`
	Remain        *rawValueString `json:"remain"`
	NozzleTempMin *rawValueString `json:"nozzle_temp_min"`
	NozzleTempMax *rawValueString `json:"nozzle_temp_max"`
}

type bambuIpcam struct {
	Timelapse *string `json:"timelapse"`
}

type rawValueString string

func (v *rawValueString) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		*v = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*v = rawValueString(s)
		return nil
	}
	*v = rawValueString(trimmed)
	return nil
}

// parseReport unmarshals a Bambu pushall report payload into a StatusResult.
// This is a pure function — no network access, safe to unit test with raw bytes.
//
// Tolerates json.UnmarshalTypeError from fields whose JSON type varies across
// printer families by returning partial status with a sanitized warning.
func parseReport(data []byte) (*driver.StatusResult, error) {
	var rep bambuReport
	warnings := []driver.StatusWarning{}
	if err := json.Unmarshal(data, &rep); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return nil, apperr.Wrap(4, "invalid status report", err)
		}
		warnings = append(warnings, typeMismatchWarning(typeErr))
	}
	var rawReport struct {
		Print json.RawMessage `json:"print"`
	}
	if err := json.Unmarshal(data, &rawReport); err == nil {
		applyRawReportFallbacks(rep.Print, rawReport.Print, data)
	}
	p := rep.Print
	state := "unknown"
	if p == nil || p.GcodeState == nil || *p.GcodeState == "" {
		warnings = append(warnings, driver.StatusWarning{
			Code:    "state_unavailable",
			Message: "printer state unavailable",
		})
	} else {
		state = mapState(*p.GcodeState)
	}
	temps, tempWarnings := mapTemperatures(p)
	warnings = append(warnings, tempWarnings...)
	progress, progressWarnings := mapProgress(p)
	warnings = append(warnings, progressWarnings...)

	result := &driver.StatusResult{
		State:        state,
		Temperatures: temps,
		Progress:     progress,
		Errors:       mapStatusErrors(p),
		Warnings:     warnings,
		Capabilities: driver.Capabilities{Status: true},
	}
	result.Job = mapJob(p)

	// Extended fields.
	result.Fans = mapFans(p)
	result.TimeEstimates = mapTimeEstimates(p)
	result.SpeedLevel = mapSpeedLevel(p)
	result.Wifi = mapWifi(p)
	// lights_report lives at top level on X1/P1/A1 and inside print on H2C.
	lightsReport := rep.LightsReport
	if len(lightsReport) == 0 && p != nil {
		lightsReport = p.LightsReport
	}
	result.Lights = mapLights(lightsReport)
	result.PrintMeta = mapPrintMeta(p)
	result.Stage = mapStage(p)
	result.Timelapse = mapTimelapse(p)
	result.GcodePosition = mapGcodePosition(p)
	result.FirmwareVersion = mapFirmwareVersion(p, rep.Info)
	result.Extensions = mapExtensions(p)

	return result, nil
}

func typeMismatchWarning(typeErr *json.UnmarshalTypeError) driver.StatusWarning {
	field := strings.TrimSpace(typeErr.Field)
	if field == "" {
		field = "unknown"
	}
	return driver.StatusWarning{
		Code:    "status_field_type_mismatch",
		Message: "status field " + field + " has unsupported data type",
	}
}

func applyRawReportFallbacks(p *bambuPrint, rawPrint, rawReport json.RawMessage) {
	if p == nil || len(rawPrint) == 0 {
		return
	}
	if p.ChamberTemper == nil {
		p.ChamberTemper = findChamberTemperature(rawPrint)
	}
	if p.ChamberTemper == nil {
		p.ChamberTemper = findChamberTemperature(rawReport)
	}
}

func findChamberTemperature(raw json.RawMessage) *rawValueString {
	return findChamberTemperatureValue(raw, false)
}

func findChamberTemperatureValue(raw json.RawMessage, inChamberObject bool) *rawValueString {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}

	for _, key := range []string{
		"chamber_temp",
		"chamber_temperature",
		"chamber_current_temper",
		"chamber_current_temp",
		"chamber_current_temperature",
		"current_chamber_temper",
		"current_chamber_temp",
		"current_chamber_temperature",
		"chamber_air_temper",
		"chamber_air_temp",
		"chamber_air_temperature",
		"enclosure_temper",
		"enclosure_temp",
		"enclosure_temperature",
		"env_temper",
		"env_temp",
	} {
		if v := rawScalarValue(obj[key]); v != nil {
			return v
		}
	}

	for key, rawValue := range obj {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if isCurrentChamberTemperatureKey(normalized, inChamberObject) {
			if v := rawScalarValue(rawValue); v != nil {
				return v
			}
		}
		// "ctc" = Chamber Temperature Controller (H2C uses device.ctc.info.temp)
		isChamberRelated := normalized == "chamber" || strings.Contains(normalized, "chamber") || normalized == "ctc"
		// "device" is a hardware container that may hold chamber-related subkeys
		isContainer := normalized == "device"
		if inChamberObject || isChamberRelated || isContainer {
			nextInChamber := inChamberObject || isChamberRelated
			if v := findChamberTemperatureValue(rawValue, nextInChamber); v != nil {
				return v
			}
		}
	}
	return nil
}

func isCurrentChamberTemperatureKey(key string, inChamberObject bool) bool {
	if key == "" ||
		strings.Contains(key, "target") ||
		strings.Contains(key, "fan") ||
		strings.Contains(key, "light") ||
		strings.Contains(key, "speed") ||
		strings.Contains(key, "state") ||
		strings.Contains(key, "mode") {
		return false
	}
	hasTemperatureName := strings.Contains(key, "temper") || strings.Contains(key, "temp")
	if !hasTemperatureName {
		return false
	}
	if strings.Contains(key, "chamber") || strings.Contains(key, "enclosure") {
		return true
	}
	return inChamberObject
}

func rawScalarValue(raw json.RawMessage) *rawValueString {
	if len(raw) == 0 {
		return nil
	}
	var value rawValueString
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	if rawToFloat(&value) == nil {
		return nil
	}
	return &value
}

func mapTemperatures(p *bambuPrint) (*driver.Temperatures, []driver.StatusWarning) {
	if p == nil {
		return nil, []driver.StatusWarning{{Code: "temperature_data_unavailable", Message: "temperature data unavailable"}}
	}
	temps := &driver.Temperatures{}
	if nozzleTemper := rawToFloat(p.NozzleTemper); nozzleTemper != nil {
		temps.Nozzle = &driver.Temperature{CurrentCelsius: *nozzleTemper}
		if nozzleTarget := rawToFloat(p.NozzleTargetTemper); nozzleTarget != nil {
			t := *nozzleTarget
			temps.Nozzle.TargetCelsius = &t
		}
	}
	if bedTemper := rawToFloat(p.BedTemper); bedTemper != nil {
		temps.Bed = &driver.Temperature{CurrentCelsius: *bedTemper}
		if bedTarget := rawToFloat(p.BedTargetTemper); bedTarget != nil {
			t := *bedTarget
			temps.Bed.TargetCelsius = &t
		}
	}
	chamberTemper := rawToFloat(p.ChamberTemper)
	if chamberTemper != nil && *chamberTemper > 0 {
		temps.Chamber = &driver.Temperature{CurrentCelsius: *chamberTemper}
	}
	if temps.Nozzle == nil && temps.Bed == nil && temps.Chamber == nil {
		return nil, []driver.StatusWarning{{Code: "temperature_data_unavailable", Message: "temperature data unavailable"}}
	}
	if p.ChamberTemper == nil {
		return temps, []driver.StatusWarning{{Code: "chamber_temperature_unavailable", Message: "chamber temperature unavailable"}}
	}
	return temps, nil
}

func mapProgress(p *bambuPrint) (*driver.Progress, []driver.StatusWarning) {
	if p == nil {
		return nil, []driver.StatusWarning{{Code: "progress_unavailable", Message: "progress unavailable"}}
	}
	percent := rawToInt(p.McPercent)
	if percent == nil {
		return nil, []driver.StatusWarning{{Code: "progress_unavailable", Message: "progress unavailable"}}
	}
	prog := &driver.Progress{Percent: *percent}
	if layer := currentLayer(p); layer != nil {
		v := *layer
		prog.CurrentLayer = &v
	}
	if totalLayers := rawToInt(p.TotalLayerNum); totalLayers != nil {
		v := *totalLayers
		prog.TotalLayers = &v
	}
	return prog, nil
}

func currentLayer(p *bambuPrint) *int {
	if layer := rawToInt(p.LayerNum); layer != nil {
		return layer
	}
	return rawToInt(p.McLayerNum)
}

func mapJob(p *bambuPrint) *driver.Job {
	if p == nil {
		return nil
	}
	var job *driver.Job
	if p.SubtaskName != nil && *p.SubtaskName != "" {
		job = &driver.Job{Name: *p.SubtaskName}
	} else if p.GcodeFile != nil && *p.GcodeFile != "" {
		job = &driver.Job{Name: *p.GcodeFile}
	}
	if job == nil {
		return nil
	}

	// Populate job ID from printer-reported values or generate a synthetic one.
	// LAN-only prints report task_id/subtask_id as "0" or empty.
	id := nonZeroStr(p.SubtaskID)
	if id == "" {
		id = nonZeroStr(p.TaskID)
	}
	if id == "" {
		id = syntheticJobID(job.Name)
	}
	if id != "" {
		job.ID = &id
	}
	return job
}

// nonZeroStr returns the string if non-nil, non-empty, and not "0"; otherwise "".
func nonZeroStr(s *string) string {
	if s == nil {
		return ""
	}
	v := strings.TrimSpace(*s)
	if v == "" || v == "0" || v == "-1" {
		return ""
	}
	return v
}

// syntheticJobID generates a deterministic short ID from a job name using FNV-1a,
// matching the approach used by open-bamboo-networking for LAN-only prints.
func syntheticJobID(name string) string {
	if name == "" {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("lan-%x", h.Sum64())
}

func mapStatusErrors(p *bambuPrint) []driver.StatusError {
	if p == nil {
		return []driver.StatusError{}
	}
	errs := make([]driver.StatusError, 0, len(p.HMS)+1)
	if p.McPrintErrorCode != nil {
		code := strings.TrimSpace(string(*p.McPrintErrorCode))
		if code != "" && !rawIsZero(p.McPrintErrorCode) {
			errs = append(errs, driver.StatusError{
				Code:    "printer_error",
				Message: "printer error: " + code,
			})
		}
	}
	return append(errs, mapHMSErrors(p)...)
}

func mapHMSErrors(p *bambuPrint) []driver.StatusError {
	errs := make([]driver.StatusError, 0, len(p.HMS))
	for _, h := range p.HMS {
		attr := rawToUint32(&h.Attr)
		code := rawToUint32(&h.Code)
		var attrValue, codeValue uint32
		if attr != nil {
			attrValue = *attr
		}
		if code != nil {
			codeValue = *code
		}
		if attrValue != 0 || codeValue != 0 {
			errs = append(errs, driver.StatusError{
				Code:    fmt.Sprintf("hms:%08x:%08x", attrValue, codeValue),
				Message: "hardware error",
			})
		}
	}
	return errs
}

// rawToInt parses a rawValueString as an integer, returning nil if empty or unparseable.
func rawToInt(v *rawValueString) *int {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(string(*v))
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err == nil {
		return &n
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	const maxInt = int(^uint(0) >> 1)
	const minInt = -maxInt - 1
	if f < float64(minInt) || f > float64(maxInt) {
		return nil
	}
	n = int(f)
	if f != float64(n) {
		return nil
	}
	return &n
}

// rawToUint32 parses a rawValueString as a uint32, returning nil if empty or unparseable.
func rawToUint32(v *rawValueString) *uint32 {
	n := rawToInt(v)
	if n == nil || *n < 0 {
		return nil
	}
	u := uint64(*n)
	if u > uint64(^uint32(0)) {
		return nil
	}
	out := uint32(u)
	return &out
}

// rawToFloat parses a rawValueString as a float64, returning nil if empty or unparseable.
func rawToFloat(v *rawValueString) *float64 {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(string(*v))
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

func rawIsZero(v *rawValueString) bool {
	f := rawToFloat(v)
	return f != nil && *f == 0
}

// fanSpeedPercent converts a Bambu fan speed value (string "0"-"15" or "0"-"100")
// to a percentage 0-100. Bambu uses a 0-15 scale for some fan fields.
func fanSpeedPercent(v *rawValueString) *int {
	raw := rawToInt(v)
	if raw == nil {
		return nil
	}
	n := *raw
	if n < 0 {
		n = 0
	}
	// Bambu reports fan speed as a string "0"-"15" mapped to percentage,
	// or sometimes directly as percentage 0-100.
	if n <= 15 {
		pct := n * 100 / 15
		return &pct
	}
	if n > 100 {
		n = 100
	}
	return &n
}

func mapFans(p *bambuPrint) driver.Fans {
	if p == nil {
		return nil
	}
	fans := driver.Fans{}
	if v := fanSpeedPercent(p.CoolingFanSpeed); v != nil {
		fans["partCooling"] = *v
	}
	if v := fanSpeedPercent(p.HeatbreakFanSpeed); v != nil {
		fans["heatbreak"] = *v
	}
	if v := fanSpeedPercent(p.BigFan1Speed); v != nil {
		fans["auxiliary"] = *v
	}
	if v := fanSpeedPercent(p.BigFan2Speed); v != nil {
		fans["chamber"] = *v
	}
	if len(fans) == 0 {
		return nil
	}
	return fans
}

func mapTimeEstimates(p *bambuPrint) *driver.TimeEstimates {
	if p == nil {
		return nil
	}
	remainingMinutes := rawToInt(p.McRemainingTime)
	// H2C uses remain_time instead of mc_remaining_time.
	if remainingMinutes == nil {
		remainingMinutes = rawToInt(p.RemainTime)
	}
	if remainingMinutes == nil {
		return nil
	}
	te := &driver.TimeEstimates{}
	remaining := *remainingMinutes * 60 // minutes -> seconds
	te.RemainingSeconds = &remaining
	return te
}

var bambuSpeedLevels = map[int]string{
	1: "silent",
	2: "standard",
	3: "sport",
	4: "ludicrous",
}

func mapSpeedLevel(p *bambuPrint) *string {
	if p == nil {
		return nil
	}
	level := rawToInt(p.SpdLvl)
	if level == nil {
		return nil
	}
	name, ok := bambuSpeedLevels[*level]
	if !ok {
		s := strconv.Itoa(*level)
		return &s
	}
	return &name
}

func mapWifi(p *bambuPrint) *driver.Wifi {
	if p == nil || p.WifiSignal == nil {
		return nil
	}
	val := rawToInt(p.WifiSignal)
	if val == nil {
		// H2C sends "-69dBm" with unit suffix; strip non-numeric suffix and retry.
		s := strings.TrimSpace(string(*p.WifiSignal))
		s = strings.TrimSuffix(strings.TrimSuffix(s, "dBm"), "dbm")
		if n, err := strconv.Atoi(s); err == nil {
			val = &n
		}
	}
	if val == nil {
		return nil
	}
	return &driver.Wifi{SignalDbm: *val}
}

func mapLights(reports []bambuLightReport) driver.Lights {
	if len(reports) == 0 {
		return nil
	}
	lights := driver.Lights{}
	for _, r := range reports {
		if r.Node != "" && r.Mode != "" {
			lights[r.Node] = r.Mode
		}
	}
	if len(lights) == 0 {
		return nil
	}
	return lights
}

func mapPrintMeta(p *bambuPrint) *driver.PrintMeta {
	if p == nil {
		return nil
	}
	var fileName string
	if p.GcodeFile != nil && *p.GcodeFile != "" {
		fileName = *p.GcodeFile
	} else if p.SubtaskName != nil && *p.SubtaskName != "" {
		fileName = *p.SubtaskName
	}
	if fileName == "" {
		return nil
	}
	meta := &driver.PrintMeta{FileName: fileName}
	if fileSize := rawToInt(p.FileSize); fileSize != nil && *fileSize > 0 {
		v := *fileSize
		meta.FileSize = &v
	}
	if nd := rawToFloat(p.NozzleDiameter); nd != nil && *nd > 0 {
		meta.NozzleDiameter = nd
	}
	if p.BedType != nil {
		bt := mapBedType(p.BedType)
		if bt != "" {
			meta.BedType = &bt
		}
	}
	return meta
}

func mapBedType(v *rawValueString) string {
	if v == nil {
		return ""
	}
	s := strings.TrimSpace(string(*v))
	if n := rawToInt(v); n != nil {
		s = strconv.Itoa(*n)
	}
	switch s {
	case "1":
		return "cool_plate"
	case "2":
		return "engineering_plate"
	case "3":
		return "high_temp_plate"
	case "4":
		return "textured_pei"
	default:
		if s == "" || s == "0" {
			return ""
		}
		return s
	}
}

var bambuStages = map[int]string{
	0:  "printing",
	1:  "auto_bed_leveling",
	2:  "heatbed_preheating",
	3:  "sweeping_xy_mech_mode",
	4:  "changing_filament",
	5:  "m400_pause",
	6:  "filament_runout_pause",
	7:  "heating_hotend",
	8:  "calibrating_extrusion",
	9:  "scanning_bed_surface",
	10: "inspecting_first_layer",
	11: "identifying_build_plate_type",
	14: "cleaning_nozzle_tip",
	15: "checking_extruder_temperature",
	16: "paused_user_input",
	17: "paused_front_cover_falling",
	18: "calibrating_micro_lidar",
	19: "calibrating_extrusion_flow",
	20: "paused_nozzle_temperature_malfunction",
	21: "paused_heat_bed_temperature_malfunction",
}

func mapStage(p *bambuPrint) *string {
	if p == nil {
		return nil
	}
	stage := rawToInt(p.StgCur)
	if stage == nil || *stage < 0 || *stage == 255 {
		return nil
	}
	name, ok := bambuStages[*stage]
	if !ok {
		s := strconv.Itoa(*stage)
		return &s
	}
	return &name
}

func mapTimelapse(p *bambuPrint) *driver.Timelapse {
	if p == nil {
		return nil
	}
	stat := p.TimelapseStat
	// H2C nests timelapse inside an ipcam object.
	if stat == nil && p.Ipcam != nil {
		stat = p.Ipcam.Timelapse
	}
	if stat == nil {
		return nil
	}
	recording := *stat == "enable"
	return &driver.Timelapse{Recording: recording}
}

func mapGcodePosition(p *bambuPrint) *driver.GcodePosition {
	if p == nil {
		return nil
	}
	curLine := rawToInt(p.CurLineNum)
	totalLine := rawToInt(p.TotalLineNum)
	if curLine == nil || totalLine == nil {
		return nil
	}
	return &driver.GcodePosition{
		ZMm:         0, // Z height not reliably available in pushall
		CurrentLine: *curLine,
		TotalLines:  *totalLine,
	}
}

// mapFirmwareVersion extracts firmware version from the report.
// Tries ota_version from print block first, then looks for the "ota" module
// in the info.module array (present in get_version responses and some pushall).
func mapFirmwareVersion(p *bambuPrint, info *bambuInfo) *string {
	if p != nil && p.OtaVersion != nil && *p.OtaVersion != "" {
		return p.OtaVersion
	}
	if info != nil {
		for _, m := range info.Module {
			if strings.EqualFold(m.Name, "ota") && m.Version != "" {
				return &m.Version
			}
		}
		// Fall back to first module with a non-empty version.
		for i := range info.Module {
			if info.Module[i].Version != "" {
				return &info.Module[i].Version
			}
		}
	}
	return nil
}

func mapExtensions(p *bambuPrint) map[string]any {
	if p == nil {
		return nil
	}
	amsData := mapAMS(p.AMS)
	vtUnits := mapVirtualTrays(p)

	ext := &driver.BambuExtension{}

	if amsData != nil || len(vtUnits) > 0 {
		if amsData == nil {
			amsData = &driver.AMSData{}
		}
		amsData.Units = append(amsData.Units, vtUnits...)
		ext.AMS = amsData
	}

	// SD card state from home_flag bits [8:9].
	if s := sdCardState(p.HomeFlag); s != "" {
		ext.SDCardState = &s
	}

	// eMMC support from fun2 capability bitmask bit 17.
	if v := hasEMMC(p.Fun2); v {
		ext.EMMCStorage = &v
	}

	// Printer-reported IP address.
	if p.WifiIP != nil && *p.WifiIP != "" {
		ext.ReportedIP = p.WifiIP
	}

	// Only emit the extension if it carries at least one field.
	if ext.AMS == nil && ext.SDCardState == nil && ext.EMMCStorage == nil && ext.ReportedIP == nil {
		return nil
	}
	return map[string]any{
		"bambu-lan": ext,
	}
}

// sdCardState interprets home_flag bits [8:9] to determine SD card state.
// Returns "" if home_flag is not available.
func sdCardState(homeFlag *rawValueString) string {
	v := rawToInt(homeFlag)
	if v == nil {
		return ""
	}
	bits := (*v >> 8) & 0x3
	switch bits {
	case 0:
		return "none"
	case 1:
		return "normal"
	case 2:
		return "abnormal"
	case 3:
		return "readonly"
	default:
		return ""
	}
}

// hasEMMC checks fun2 capability bitmask for bit 17 (internal eMMC storage).
func hasEMMC(fun2 *string) bool {
	if fun2 == nil || *fun2 == "" {
		return false
	}
	bits, err := strconv.ParseUint(*fun2, 16, 64)
	if err != nil {
		return false
	}
	return bits&(1<<17) != 0
}

func mapAMS(ams *bambuAMS) *driver.AMSData {
	if ams == nil || len(ams.AMS) == 0 {
		return nil
	}
	units := make([]driver.AMSUnit, 0, len(ams.AMS))
	for _, u := range ams.AMS {
		id := 0
		if parsedID := rawToInt(&u.ID); parsedID != nil {
			id = *parsedID
		}
		unit := driver.AMSUnit{
			ID:    id,
			Trays: make([]driver.AMSTray, 0, len(u.Tray)),
		}
		if humidity := rawToInt(&u.Humidity); humidity != nil && *humidity >= 1 && *humidity <= 5 {
			r := amsHumidityRange(*humidity)
			l := amsHumidityLevel(*humidity)
			unit.HumidityRange = &r
			unit.HumidityLevel = &l
		}
		if temp := rawToFloat(&u.Temp); temp != nil && *temp > 0 {
			unit.Temperature = temp
		}
		for _, tray := range u.Tray {
			slot := 0
			if parsedSlot := rawToInt(&tray.ID); parsedSlot != nil {
				slot = *parsedSlot
			}
			dt := driver.AMSTray{Slot: slot}
			if tray.TrayType != nil && *tray.TrayType != "" {
				dt.FilamentType = tray.TrayType
			}
			if tray.TrayColor != nil && *tray.TrayColor != "" {
				dt.Color = tray.TrayColor
			}
			if remain := rawToInt(tray.Remain); remain != nil {
				v := *remain
				dt.RemainingPercent = &v
			}
			if nozzleTempMin := rawToInt(tray.NozzleTempMin); nozzleTempMin != nil {
				v := *nozzleTempMin
				dt.NozzleTempMin = &v
			}
			if nozzleTempMax := rawToInt(tray.NozzleTempMax); nozzleTempMax != nil {
				v := *nozzleTempMax
				dt.NozzleTempMax = &v
			}
			unit.Trays = append(unit.Trays, dt)
		}
		units = append(units, unit)
	}
	if len(units) == 0 {
		return nil
	}
	return &driver.AMSData{Units: units}
}

// mapVirtualTrays converts vt_tray (A1 Mini external spool) and vir_slot
// (H2C virtual slots) into AMSUnit entries. Only non-empty trays (those with
// filament type or color) are included.
func mapVirtualTrays(p *bambuPrint) []driver.AMSUnit {
	var trays []bambuVtTray
	if p.VtTray != nil {
		trays = append(trays, *p.VtTray)
	}
	trays = append(trays, p.VirSlot...)

	var units []driver.AMSUnit
	for _, vt := range trays {
		if !vtTrayHasFilament(&vt) {
			continue
		}
		id := 254
		if parsedID := rawToInt(&vt.ID); parsedID != nil {
			id = *parsedID
		}
		dt := driver.AMSTray{Slot: 0}
		if vt.TrayType != nil && *vt.TrayType != "" {
			dt.FilamentType = vt.TrayType
		}
		if vt.TrayColor != nil && *vt.TrayColor != "" && *vt.TrayColor != "00000000" {
			dt.Color = vt.TrayColor
		}
		if remain := rawToInt(vt.Remain); remain != nil && *remain > 0 {
			v := *remain
			dt.RemainingPercent = &v
		}
		if nozzleTempMin := rawToInt(vt.NozzleTempMin); nozzleTempMin != nil && *nozzleTempMin > 0 {
			v := *nozzleTempMin
			dt.NozzleTempMin = &v
		}
		if nozzleTempMax := rawToInt(vt.NozzleTempMax); nozzleTempMax != nil && *nozzleTempMax > 0 {
			v := *nozzleTempMax
			dt.NozzleTempMax = &v
		}
		units = append(units, driver.AMSUnit{
			ID:    id,
			Trays: []driver.AMSTray{dt},
		})
	}
	return units
}

func vtTrayHasFilament(vt *bambuVtTray) bool {
	if vt.TrayType != nil && *vt.TrayType != "" {
		return true
	}
	if vt.TrayColor != nil && *vt.TrayColor != "" && *vt.TrayColor != "00000000" {
		return true
	}
	return false
}

// amsHumidityRange maps a Bambu humidity index (1-5) to a human-readable range.
func amsHumidityRange(index int) string {
	switch index {
	case 1:
		return "< 10%"
	case 2:
		return "10-20%"
	case 3:
		return "20-30%"
	case 4:
		return "30-40%"
	case 5:
		return "> 40%"
	default:
		return ""
	}
}

// amsHumidityLevel maps a Bambu humidity index (1-5) to a qualitative level.
func amsHumidityLevel(index int) string {
	switch index {
	case 1:
		return "very dry"
	case 2:
		return "dry"
	case 3:
		return "moderate"
	case 4:
		return "slightly humid"
	case 5:
		return "humid"
	default:
		return ""
	}
}

const (
	cameraPortMJPEG    = 6000
	cameraPortH264     = 322
	cameraProbeTimeout = 2 * time.Second

	// cameraAuthSize is the fixed size of the MJPEG camera auth packet.
	cameraAuthSize = 80
	// cameraFrameHeaderSize is the size of each MJPEG frame header.
	cameraFrameHeaderSize = 16
	// cameraMaxFrameSize caps how large a single JPEG frame can be (1 MB).
	cameraMaxFrameSize = 1 << 20
	// cameraUsername is the fixed username for Bambu camera auth.
	cameraUsername = "bblp"
)

// CameraStream opens a live camera stream from the printer.
// It auto-detects the protocol by probing port 322 (RTSPS/H.264) first,
// falling back to port 6000 (MJPEG) if the RTSPS probe fails.
// H-series and X-series printers serve camera only via RTSPS;
// A1/A1 mini family printers refuse port 322 and serve MJPEG on port 6000.
func (d *Driver) CameraStream(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger) (*driver.CameraStreamResult, error) {
	trace := protocoltrace.FromContext(ctx)

	tlsCfg, err := buildTLSConfig(p.Serial, s.TLSFingerprint, p.Insecure)
	if err != nil {
		return nil, err
	}

	// Try port 322 (RTSPS / H.264) first.
	rtspEndpoint := fmt.Sprintf("%s:322", p.Host)
	rtspStart := time.Now()
	rtspTLS := tlsCfg.Clone()
	rtspStream, rtspErr := d.dialRTSPSFn(ctx, rtspTLS, p.Host, s.AccessCode)
	if rtspErr == nil {
		dur := time.Since(rtspStart).Milliseconds()
		trace.Emit(protocoltrace.Event{
			Timestamp:  time.Now().UTC(),
			Driver:     "bambu-lan",
			Operation:  "CameraStream",
			Phase:      "connect",
			Transport:  "rtsps",
			Endpoint:   rtspEndpoint,
			Protocol:   "h264",
			DurationMs: &dur,
		})
		return &driver.CameraStreamResult{
			Format:       driver.CameraFormatH264,
			Stream:       rtspStream,
			Capabilities: d.Capabilities(),
		}, nil
	}
	rtspDur := time.Since(rtspStart).Milliseconds()
	trace.Emit(protocoltrace.Event{
		Timestamp:     time.Now().UTC(),
		Driver:        "bambu-lan",
		Operation:     "CameraStream",
		Phase:         "connect",
		Transport:     "rtsps",
		Endpoint:      rtspEndpoint,
		Protocol:      "h264",
		DurationMs:    &rtspDur,
		ErrorCategory: "connection_error",
	})

	// Fall back to port 6000 (MJPEG) with a short timeout.
	mjpegEndpoint := fmt.Sprintf("%s:%d", p.Host, cameraPortMJPEG)
	mjpegStart := time.Now()
	mjpegCtx, mjpegCancel := context.WithTimeout(ctx, cameraProbeTimeout)
	conn, mjpegErr := d.dialTLS(mjpegCtx, mjpegEndpoint, tlsCfg.Clone())
	mjpegCancel()

	if mjpegErr != nil {
		dur := time.Since(mjpegStart).Milliseconds()
		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     "CameraStream",
			Phase:         "connect",
			Transport:     "tls",
			Endpoint:      mjpegEndpoint,
			Protocol:      "mjpeg",
			DurationMs:    &dur,
			ErrorCategory: "connection_error",
		})
		return nil, apperr.New(4, "camera endpoint unreachable: both ports 322 and 6000 failed")
	}

	if authErr := sendCameraAuth(conn, s.AccessCode); authErr != nil {
		_ = conn.Close()
		dur := time.Since(mjpegStart).Milliseconds()
		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     "CameraStream",
			Phase:         "authenticate",
			Transport:     "tls",
			Endpoint:      mjpegEndpoint,
			Protocol:      "mjpeg",
			DurationMs:    &dur,
			ErrorCategory: "auth_rejected",
		})
		return nil, apperr.Wrap(4, "camera authentication failed", authErr)
	}

	dur := time.Since(mjpegStart).Milliseconds()
	trace.Emit(protocoltrace.Event{
		Timestamp:  time.Now().UTC(),
		Driver:     "bambu-lan",
		Operation:  "CameraStream",
		Phase:      "connect",
		Transport:  "tls",
		Endpoint:   mjpegEndpoint,
		Protocol:   "mjpeg",
		DurationMs: &dur,
	})

	stream := newMJPEGStream(conn)
	return &driver.CameraStreamResult{
		Format:       driver.CameraFormatMJPEG,
		Stream:       stream,
		Capabilities: d.Capabilities(),
	}, nil
}

// sendCameraAuth writes the 80-byte authentication packet to the camera TLS connection.
// Layout:
//
//	[0..3]   LE u32 = 0x40    (payload size)
//	[4..7]   LE u32 = 0x3000  (packet type: auth)
//	[8..15]  zeros
//	[16..47] 32 bytes: username "bblp", NUL-padded
//	[48..79] 32 bytes: access code, NUL-padded
func sendCameraAuth(conn io.Writer, accessCode string) error {
	var pkt [cameraAuthSize]byte
	// Payload size = 0x40 (64), little-endian.
	pkt[0] = 0x40
	// Packet type = 0x3000, little-endian.
	pkt[4] = 0x00
	pkt[5] = 0x30
	// Username at offset 16 (32 bytes, NUL-padded).
	copy(pkt[16:48], cameraUsername)
	// Access code at offset 48 (32 bytes, NUL-padded).
	copy(pkt[48:80], accessCode)
	_, err := conn.Write(pkt[:])
	return err
}

// mjpegStream reads the Bambu proprietary MJPEG frame format and re-emits
// as multipart/x-mixed-replace boundary-delimited JPEG frames suitable for
// direct browser consumption.
type mjpegStream struct {
	conn   io.ReadCloser
	buf    []byte
	closed bool
}

func newMJPEGStream(conn io.ReadCloser) *mjpegStream {
	return &mjpegStream{conn: conn}
}

// Read implements io.Reader. Each call emits bytes from a multipart frame:
// --frame\r\nContent-Type: image/jpeg\r\nContent-Length: N\r\n\r\n<jpeg>\r\n
func (m *mjpegStream) Read(p []byte) (int, error) {
	for len(m.buf) == 0 {
		if m.closed {
			return 0, io.EOF
		}
		frame, err := m.readFrame()
		if err != nil {
			return 0, err
		}
		// Format as multipart part.
		header := fmt.Sprintf("--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(frame))
		m.buf = make([]byte, 0, len(header)+len(frame)+2)
		m.buf = append(m.buf, header...)
		m.buf = append(m.buf, frame...)
		m.buf = append(m.buf, '\r', '\n')
	}
	n := copy(p, m.buf)
	m.buf = m.buf[n:]
	return n, nil
}

// readFrame reads one JPEG frame from the Bambu proprietary protocol.
// Frame format: 16-byte header (payload_size u32le, itrack u32le, flags u32le, pad u32le)
// followed by payload_size bytes of JPEG data.
func (m *mjpegStream) readFrame() ([]byte, error) {
	return readMJPEGFrame(m.conn)
}

func readMJPEGFrame(r io.Reader) ([]byte, error) {
	var hdr [cameraFrameHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	size := uint32(hdr[0]) | uint32(hdr[1])<<8 | uint32(hdr[2])<<16 | uint32(hdr[3])<<24
	if size == 0 || size > cameraMaxFrameSize {
		return nil, fmt.Errorf("invalid MJPEG frame size: %d", size)
	}
	frame := make([]byte, size)
	if _, err := io.ReadFull(r, frame); err != nil {
		return nil, err
	}
	return frame, nil
}

func (m *mjpegStream) Close() error {
	m.closed = true
	return m.conn.Close()
}

func (d *Driver) CaptureFingerprint(ctx context.Context, p driver.ProfileInput) (string, error) {
	trace := protocoltrace.FromContext(ctx)
	endpoint := fmt.Sprintf("%s:8883", p.Host)

	cfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Bambu CA not in OS trust stores; capturing cert for TOFU pin (ADR 0007)
		ServerName:         p.Serial,
	}
	connectStart := time.Now()
	conn, err := d.dialTLS(ctx, endpoint, cfg)
	if err != nil {
		dur := time.Since(connectStart).Milliseconds()
		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     "CaptureFingerprint",
			Phase:         "connect",
			Transport:     "tls",
			Endpoint:      endpoint,
			DurationMs:    &dur,
			ErrorCategory: "connection_error",
		})
		return "", apperr.Wrap(4, "TLS connect failed", err)
	}
	connectDur := time.Since(connectStart).Milliseconds()
	trace.Emit(protocoltrace.Event{
		Timestamp:  time.Now().UTC(),
		Driver:     "bambu-lan",
		Operation:  "CaptureFingerprint",
		Phase:      "connect",
		Transport:  "tls",
		Endpoint:   endpoint,
		DurationMs: &connectDur,
	})
	defer func() { _ = conn.Close() }()
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", apperr.New(4, "TLS handshake completed but no certificate received")
	}
	sum := sha256.Sum256(state.PeerCertificates[0].Raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (d *Driver) Discover(ctx context.Context) ([]driver.DiscoveredPrinter, error) {
	trace := protocoltrace.FromContext(ctx)

	type protoStart struct {
		name  string
		start func() (<-chan *mdnsEntry, error)
	}
	protos := []protoStart{
		{"mDNS", func() (<-chan *mdnsEntry, error) { return d.browse(ctx, bambuMDNSService) }},
		{"SSDP", func() (<-chan *mdnsEntry, error) { return d.browseSSDP(ctx) }},
		{"UDP", func() (<-chan *mdnsEntry, error) { return d.browseUDP(ctx) }},
	}

	var channels []<-chan *mdnsEntry
	var startErrs []string
	for _, p := range protos {
		ch, err := p.start()
		if err != nil {
			startErrs = append(startErrs, fmt.Sprintf("%s: %s", p.name, err))
		} else {
			channels = append(channels, ch)
		}
	}
	if len(channels) == 0 {
		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     "Discover",
			Phase:         "scan",
			Transport:     "multicast",
			ErrorCategory: "all_protocols_failed",
		})
		return nil, apperr.Newf(4, "all discovery protocols failed to start: %s",
			strings.Join(startErrs, "; "))
	}

	scanStart := time.Now()
	merged := fanIn(ctx, channels...)
	seen := make(map[string]struct{})
	result := []driver.DiscoveredPrinter{}
	for e := range merged {
		p := driver.DiscoveredPrinter{
			Host:   e.Host,
			Port:   e.Port,
			Driver: d.Name(),
			Serial: txtValue(e.Text, "sn"),
			Model:  txtValue(e.Text, "dev_model_name"),
			Name:   txtValue(e.Text, "dev_name"),
		}
		key := dedupeKey(p)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			result = append(result, p)
		}
	}
	scanDur := time.Since(scanStart).Milliseconds()
	count := int64(len(result))
	trace.Emit(protocoltrace.Event{
		Timestamp:  time.Now().UTC(),
		Driver:     "bambu-lan",
		Operation:  "Discover",
		Phase:      "scan",
		Transport:  "multicast",
		DurationMs: &scanDur,
		Detail:     map[string]any{"found": count},
	})
	return result, nil
}

func dedupeKey(p driver.DiscoveredPrinter) string {
	if p.Serial != "" {
		return "serial:" + p.Serial
	}
	return "host:" + p.Host
}

func fanIn(ctx context.Context, channels ...<-chan *mdnsEntry) <-chan *mdnsEntry {
	out := make(chan *mdnsEntry)
	var wg sync.WaitGroup
	for _, ch := range channels {
		wg.Add(1)
		go func(c <-chan *mdnsEntry) {
			defer wg.Done()
			for {
				select {
				case e, ok := <-c:
					if !ok {
						return
					}
					select {
					case out <- e:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(ch)
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

// txtValue returns the value for a "key=value" TXT record, or "" if not found.
func txtValue(records []string, key string) string {
	prefix := key + "="
	for _, r := range records {
		if strings.HasPrefix(r, prefix) {
			return r[len(prefix):]
		}
	}
	return ""
}

func randomClientID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "polimero-" + hex.EncodeToString(b)
}
