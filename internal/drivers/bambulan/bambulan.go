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
	"log/slog"
	"strings"
	"sync"

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
	newClient  func(*mqtt.ClientOptions) mqttConn
	dialTLS    func(ctx context.Context, addr string, cfg *tls.Config) (*tls.Conn, error)
	browse     func(ctx context.Context, service string) (<-chan *mdnsEntry, error)
	browseSSDP func(ctx context.Context) (<-chan *mdnsEntry, error)
	browseUDP  func(ctx context.Context) (<-chan *mdnsEntry, error)
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
		browse:     realBrowse,
		browseSSDP: realBrowseSSDP,
		browseUDP:  realBrowseUDP,
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
	return driver.Capabilities{Status: true, TLSRefresh: true, Discovery: true}
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
func isPushallReport(data []byte) bool {
	var rep bambuReport
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
	Print *bambuPrint `json:"print"`
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
func parseReport(data []byte) (*driver.StatusResult, error) {
	var rep bambuReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, apperr.Wrap(4, "invalid status report", err)
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
