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
	newClient func(*mqtt.ClientOptions) mqttConn
	dialTLS   func(ctx context.Context, addr string, cfg *tls.Config) (*tls.Conn, error)
	browse    func(ctx context.Context, service string) (<-chan *mdnsEntry, error)
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
		browse: realBrowse,
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
			out <- &mdnsEntry{Host: host, Port: e.Port, Text: e.Text}
		}
	}()
	return out, nil
}

func (d *Driver) Name() string { return "bambu-lan" }

// Capabilities returns the bambu-lan driver's supported operations.
func (d *Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{Status: true, TLSRefresh: true, Discovery: true}
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

	tlsCfg, err := buildTLSConfig(serial, "", false) // capture mode: no fingerprint to check yet
	if err != nil {
		return "", apperr.Newf(4, "TLS config: %s", err)
	}

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
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, apperr.New(4, "status check cancelled")
		}
		return nil, apperr.New(4, "status check timed out")
	}
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
		return apperr.Wrap(3, err.Error(), err)
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
	temps := &driver.Temperatures{
		Nozzle: &driver.Temperature{CurrentCelsius: p.NozzleTemper},
		Bed:    &driver.Temperature{CurrentCelsius: p.BedTemper},
	}
	if p.NozzleTargetTemper > 0 {
		t := p.NozzleTargetTemper
		temps.Nozzle.TargetCelsius = &t
	}
	if p.BedTargetTemper > 0 {
		t := p.BedTargetTemper
		temps.Bed.TargetCelsius = &t
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

func (d *Driver) CaptureFingerprint(ctx context.Context, host, serial string) (string, error) {
	cfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Bambu CA not in OS trust stores; capturing cert for TOFU pin (ADR 0007)
		ServerName:         serial,
	}
	conn, err := d.dialTLS(ctx, fmt.Sprintf("%s:8883", host), cfg)
	if err != nil {
		return "", apperr.Newf(4, "TLS connect failed: %s", err)
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
	entries, err := d.browse(ctx, bambuMDNSService)
	if err != nil {
		return nil, apperr.Newf(4, "mDNS browse failed: %s", err)
	}
	result := []driver.DiscoveredPrinter{}
	for e := range entries {
		result = append(result, driver.DiscoveredPrinter{
			Host:   e.Host,
			Port:   e.Port,
			Driver: d.Name(),
			Serial: txtValue(e.Text, "sn"),
			Model:  txtValue(e.Text, "dev_model_name"),
			Name:   txtValue(e.Text, "dev_name"),
		})
	}
	return result, nil
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
