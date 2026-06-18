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
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/eclipse/paho.mqtt.golang/packets"
	zeroconf "github.com/grandcat/zeroconf"
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
	newClient   func(*mqtt.ClientOptions) mqttConn
	dialTLS     func(ctx context.Context, addr string, cfg *tls.Config) (*tls.Conn, error)
	dialRTSPSFn func(tlsCfg *tls.Config, host, accessCode string) (io.ReadCloser, error)
	browse      func(ctx context.Context, service string) (<-chan *mdnsEntry, error)
	browseSSDP  func(ctx context.Context) (<-chan *mdnsEntry, error)
	browseUDP   func(ctx context.Context) (<-chan *mdnsEntry, error)
	dialFTP     ftpDialer
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
		dialRTSPSFn: dialRTSPS,
		browse:      realBrowse,
		browseSSDP:  realBrowseSSDP,
		browseUDP:   realBrowseUDP,
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
	return driver.Capabilities{Status: true, TLSRefresh: true, Discovery: true, CameraStream: true, FileList: true, FileDownload: true, FileUpload: true}
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
	opts.AddBroker(fmt.Sprintf("tls://%s:8883", p.Host))
	opts.SetClientID(randomClientID())
	opts.SetUsername("bblp")
	opts.SetPassword(s.AccessCode)
	opts.SetTLSConfig(tlsCfg)
	opts.SetConnectTimeout(p.Timeout)
	opts.SetAutoReconnect(false)
	opts.SetKeepAlive(60)

	client := d.newClient(opts)
	if err := waitMQTTToken(ctx, client.Connect()); err != nil {
		if isContextDoneErr(err) {
			go client.Disconnect(0)
			return "", apperr.New(4, "connection cancelled")
		}
		return "", classifyMQTTError(err)
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
// Sequence: connect → subscribe report topic → publish pushall → wait for report → parse.
//
// Exit codes on error:
//   - 3: TLS fingerprint mismatch or MQTT auth rejected
//   - 4: network failure, subscribe/publish failure, or context deadline exceeded
func (d *Driver) Status(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	tlsCfg, err := buildTLSConfig(p.Serial, s.TLSFingerprint, p.Insecure)
	if err != nil {
		return nil, err
	}

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

	if err := waitMQTTToken(ctx, client.Connect()); err != nil {
		if isContextDoneErr(err) {
			go client.Disconnect(0)
			return nil, statusContextError(err)
		}
		return nil, classifyStatusError(err)
	}
	defer client.Disconnect(250)

	ch := make(chan []byte, 8)
	reportTopic := fmt.Sprintf("device/%s/report", p.Serial)
	requestTopic := fmt.Sprintf("device/%s/request", p.Serial)

	subToken := client.Subscribe(reportTopic, 0, func(_ mqtt.Client, msg mqtt.Message) {
		payload := make([]byte, len(msg.Payload()))
		copy(payload, msg.Payload())
		select {
		case ch <- payload:
		default:
		}
	})
	if err := waitMQTTToken(ctx, subToken); err != nil {
		if isContextDoneErr(err) {
			return nil, statusContextError(err)
		}
		return nil, apperr.Wrap(4, "status subscription failed", err)
	}

	const pushall = `{"pushing":{"sequence_id":"1","command":"pushall","version":1,"push_target":1}}`
	pubToken := client.Publish(requestTopic, 0, false, pushall)
	if err := waitMQTTToken(ctx, pubToken); err != nil {
		if isContextDoneErr(err) {
			return nil, statusContextError(err)
		}
		return nil, apperr.Wrap(4, "status request failed", err)
	}

	for {
		select {
		case data := <-ch:
			if isPushallReport(data) {
				return parseReport(data)
			}
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil, apperr.New(4, "status check cancelled")
			}
			return nil, apperr.New(4, "status check timed out")
		}
	}
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

func statusContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return apperr.New(4, "status check cancelled")
	}
	return apperr.New(4, "status check timed out")
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
}

type bambuLightReport struct {
	Node string `json:"node"`
	Mode string `json:"mode"`
}

type bambuPrint struct {
	GcodeState         *string         `json:"gcode_state"`
	NozzleTemper       *float64        `json:"nozzle_temper"`
	NozzleTargetTemper *float64        `json:"nozzle_target_temper"`
	BedTemper          *float64        `json:"bed_temper"`
	BedTargetTemper    *float64        `json:"bed_target_temper"`
	ChamberTemper      *float64        `json:"chamber_temper"`
	SubtaskName        *string         `json:"subtask_name"`
	GcodeFile          *string         `json:"gcode_file"`
	McPercent          *int            `json:"mc_percent"`
	LayerNum           *int            `json:"layer_num"`
	McLayerNum         *int            `json:"mc_layer_num"`
	TotalLayerNum      *int            `json:"total_layer_num"`
	McPrintErrorCode   *rawValueString `json:"mc_print_error_code"`
	HMS                []bambuHMS      `json:"hms"`

	// Extended fields for --detailed status.
	BigFan1Speed    *rawValueString `json:"big_fan1_speed"`
	BigFan2Speed    *rawValueString `json:"big_fan2_speed"`
	CoolingFanSpeed *rawValueString `json:"cooling_fan_speed"`
	HeatbreakFanSpeed *rawValueString `json:"heatbreak_fan_speed"`

	McRemainingTime *int    `json:"mc_remaining_time"`
	PrintedTime     *int    `json:"mc_print_line_number"` // not used; see below
	SpdLvl          *int    `json:"spd_lvl"`
	SpdMag          *int    `json:"spd_mag"`
	WifiSignal      *rawValueString `json:"wifi_signal"`
	GcodeFilePreparePercent *rawValueString `json:"gcode_file_prepare_percent"`

	// Stg type varies across families: int on X1/P1/A1, array on H2C.
	// Not used in mappings (mapStage uses StgCur); accept any JSON value.
	Stg    json.RawMessage `json:"stg"`
	StgCur *int            `json:"stg_cur"`

	NozzleDiameter    *rawValueString `json:"nozzle_diameter"`
	TotalLayerCount   *int            `json:"total_layer_num_bak"` // not used; total_layer_num preferred
	FileSize          *int            `json:"file_size"`           // not present in all FW; treat as optional

	TimelapseStat *string `json:"ipcam_record_timelapse"`

	// Time fields.
	PrintRealTime *int `json:"mc_print_real_time"` // not always present
	PrepareTime   *int `json:"mc_prepare_time"`    // not always present

	// AMS data (nested inside print in the pushall response).
	AMS *bambuAMS `json:"ams"`

	// Z position.
	ZOffset *float64 `json:"z_offset"` // not the current Z; see below

	// G-code line tracking.
	CurLineNum   *rawValueString `json:"cur_line_num"`
	TotalLineNum *rawValueString `json:"total_line_num"`

	// BedType (plate type identifier).
	BedType *rawValueString `json:"bed_type"`

	// LightsReport is nested inside "print" on some families (H2C).
	// On X1/P1/A1 it appears at the top level of the report instead.
	LightsReport []bambuLightReport `json:"lights_report"`
}

type bambuAMS struct {
	AMS          []bambuAMSUnit `json:"ams"`
	AMSExistBits *rawValueString `json:"ams_exist_bits"`
	TrayNow      *rawValueString `json:"tray_now"`
}

type bambuAMSUnit struct {
	ID        rawValueString `json:"id"`
	Humidity  rawValueString `json:"humidity"`
	Temp      rawValueString `json:"temp"`
	Tray      []bambuAMSTray `json:"tray"`
}

type bambuAMSTray struct {
	ID             rawValueString `json:"id"`
	TrayType       *string        `json:"tray_type"`
	TrayColor      *string        `json:"tray_color"`
	Remain         *int           `json:"remain"`
	NozzleTempMin  *int           `json:"nozzle_temp_min"`
	NozzleTempMax  *int           `json:"nozzle_temp_max"`
}

type bambuHMS struct {
	Attr uint32 `json:"attr"`
	Code uint32 `json:"code"`
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
// printer families (e.g. H2C sends "stg" as an array instead of an int).
func parseReport(data []byte) (*driver.StatusResult, error) {
	var rep bambuReport
	if err := json.Unmarshal(data, &rep); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return nil, apperr.Wrap(4, "invalid status report", err)
		}
	}
	p := rep.Print
	state := "unknown"
	warnings := []driver.StatusWarning{}
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
	result.Extensions = mapExtensions(p)

	return result, nil
}

func mapTemperatures(p *bambuPrint) (*driver.Temperatures, []driver.StatusWarning) {
	if p == nil {
		return nil, []driver.StatusWarning{{Code: "temperature_data_unavailable", Message: "temperature data unavailable"}}
	}
	temps := &driver.Temperatures{}
	if p.NozzleTemper != nil {
		temps.Nozzle = &driver.Temperature{CurrentCelsius: *p.NozzleTemper}
		if p.NozzleTargetTemper != nil && *p.NozzleTargetTemper > 0 {
			t := *p.NozzleTargetTemper
			temps.Nozzle.TargetCelsius = &t
		}
	}
	if p.BedTemper != nil {
		temps.Bed = &driver.Temperature{CurrentCelsius: *p.BedTemper}
		if p.BedTargetTemper != nil && *p.BedTargetTemper > 0 {
			t := *p.BedTargetTemper
			temps.Bed.TargetCelsius = &t
		}
	}
	if p.ChamberTemper != nil && *p.ChamberTemper > 0 {
		temps.Chamber = &driver.Temperature{CurrentCelsius: *p.ChamberTemper}
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
	if p == nil || p.McPercent == nil {
		return nil, []driver.StatusWarning{{Code: "progress_unavailable", Message: "progress unavailable"}}
	}
	prog := &driver.Progress{Percent: *p.McPercent}
	if layer := currentLayer(p); layer != nil {
		v := *layer
		prog.CurrentLayer = &v
	}
	if p.TotalLayerNum != nil {
		v := *p.TotalLayerNum
		prog.TotalLayers = &v
	}
	return prog, nil
}

func currentLayer(p *bambuPrint) *int {
	if p.LayerNum != nil {
		return p.LayerNum
	}
	return p.McLayerNum
}

func mapJob(p *bambuPrint) *driver.Job {
	if p == nil {
		return nil
	}
	if p.SubtaskName != nil && *p.SubtaskName != "" {
		return &driver.Job{Name: *p.SubtaskName}
	}
	if p.GcodeFile != nil && *p.GcodeFile != "" {
		return &driver.Job{Name: *p.GcodeFile}
	}
	return nil
}

func mapStatusErrors(p *bambuPrint) []driver.StatusError {
	if p == nil {
		return []driver.StatusError{}
	}
	errs := make([]driver.StatusError, 0, len(p.HMS)+1)
	if p.McPrintErrorCode != nil {
		code := strings.TrimSpace(string(*p.McPrintErrorCode))
		if code != "" && code != "0" {
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
		if h.Attr != 0 || h.Code != 0 {
			errs = append(errs, driver.StatusError{
				Code:    fmt.Sprintf("hms:%08x:%08x", h.Attr, h.Code),
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
	if err != nil {
		return nil
	}
	return &n
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
	if p == nil || p.McRemainingTime == nil {
		return nil
	}
	te := &driver.TimeEstimates{}
	remaining := *p.McRemainingTime * 60 // minutes → seconds
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
	if p == nil || p.SpdLvl == nil {
		return nil
	}
	name, ok := bambuSpeedLevels[*p.SpdLvl]
	if !ok {
		s := strconv.Itoa(*p.SpdLvl)
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
	if p.FileSize != nil && *p.FileSize > 0 {
		v := *p.FileSize
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
	if p == nil || p.StgCur == nil || *p.StgCur < 0 {
		return nil
	}
	name, ok := bambuStages[*p.StgCur]
	if !ok {
		s := strconv.Itoa(*p.StgCur)
		return &s
	}
	return &name
}

func mapTimelapse(p *bambuPrint) *driver.Timelapse {
	if p == nil || p.TimelapseStat == nil {
		return nil
	}
	recording := *p.TimelapseStat == "enable"
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

func mapExtensions(p *bambuPrint) map[string]any {
	if p == nil || p.AMS == nil {
		return nil
	}
	amsData := mapAMS(p.AMS)
	if amsData == nil {
		return nil
	}
	return map[string]any{
		"bambu-lan": &driver.BambuExtension{AMS: amsData},
	}
}

func mapAMS(ams *bambuAMS) *driver.AMSData {
	if ams == nil || len(ams.AMS) == 0 {
		return nil
	}
	units := make([]driver.AMSUnit, 0, len(ams.AMS))
	for _, u := range ams.AMS {
		id, _ := strconv.Atoi(string(u.ID))
		unit := driver.AMSUnit{
			ID:    id,
			Trays: make([]driver.AMSTray, 0, len(u.Tray)),
		}
		if h, err := strconv.Atoi(string(u.Humidity)); err == nil && h >= 1 && h <= 5 {
			r := amsHumidityRange(h)
			l := amsHumidityLevel(h)
			unit.HumidityRange = &r
			unit.HumidityLevel = &l
		}
		if t, err := strconv.ParseFloat(string(u.Temp), 64); err == nil && t > 0 {
			unit.Temperature = &t
		}
		for _, tray := range u.Tray {
			slot, _ := strconv.Atoi(string(tray.ID))
			dt := driver.AMSTray{Slot: slot}
			if tray.TrayType != nil && *tray.TrayType != "" {
				dt.FilamentType = tray.TrayType
			}
			if tray.TrayColor != nil && *tray.TrayColor != "" {
				dt.Color = tray.TrayColor
			}
			if tray.Remain != nil {
				v := *tray.Remain
				dt.RemainingPercent = &v
			}
			if tray.NozzleTempMin != nil {
				v := *tray.NozzleTempMin
				dt.NozzleTempMin = &v
			}
			if tray.NozzleTempMax != nil {
				v := *tray.NozzleTempMax
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
	tlsCfg, err := buildTLSConfig(p.Serial, s.TLSFingerprint, p.Insecure)
	if err != nil {
		return nil, err
	}

	// Try port 322 (RTSPS / H.264) first.
	rtspTLS := tlsCfg.Clone()
	rtspStream, rtspErr := d.dialRTSPSFn(rtspTLS, p.Host, s.AccessCode)
	if rtspErr == nil {
		return &driver.CameraStreamResult{
			Format:       driver.CameraFormatH264,
			Stream:       rtspStream,
			Capabilities: d.Capabilities(),
		}, nil
	}

	// Fall back to port 6000 (MJPEG) with a short timeout.
	mjpegCtx, mjpegCancel := context.WithTimeout(ctx, cameraProbeTimeout)
	conn, mjpegErr := d.dialTLS(mjpegCtx, fmt.Sprintf("%s:%d", p.Host, cameraPortMJPEG), tlsCfg.Clone())
	mjpegCancel()

	if mjpegErr != nil {
		return nil, apperr.New(4, "camera endpoint unreachable: both ports 322 and 6000 failed")
	}

	if authErr := sendCameraAuth(conn, s.AccessCode); authErr != nil {
		_ = conn.Close()
		return nil, apperr.Wrap(4, "camera authentication failed", authErr)
	}

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
	var hdr [cameraFrameHeaderSize]byte
	if _, err := io.ReadFull(m.conn, hdr[:]); err != nil {
		return nil, err
	}
	size := uint32(hdr[0]) | uint32(hdr[1])<<8 | uint32(hdr[2])<<16 | uint32(hdr[3])<<24
	if size == 0 || size > cameraMaxFrameSize {
		return nil, fmt.Errorf("invalid MJPEG frame size: %d", size)
	}
	frame := make([]byte, size)
	if _, err := io.ReadFull(m.conn, frame); err != nil {
		return nil, err
	}
	return frame, nil
}

func (m *mjpegStream) Close() error {
	m.closed = true
	return m.conn.Close()
}

func (d *Driver) CaptureFingerprint(ctx context.Context, p driver.ProfileInput) (string, error) {
	cfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Bambu CA not in OS trust stores; capturing cert for TOFU pin (ADR 0007)
		ServerName:         p.Serial,
	}
	conn, err := d.dialTLS(ctx, fmt.Sprintf("%s:8883", p.Host), cfg)
	if err != nil {
		return "", apperr.Wrap(4, "TLS connect failed", err)
	}
	defer func() { _ = conn.Close() }()
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", apperr.New(4, "TLS handshake completed but no certificate received")
	}
	sum := sha256.Sum256(state.PeerCertificates[0].Raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (d *Driver) Discover(ctx context.Context) ([]driver.DiscoveredPrinter, error) {
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
		return nil, apperr.Newf(4, "all discovery protocols failed to start: %s",
			strings.Join(startErrs, "; "))
	}

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
