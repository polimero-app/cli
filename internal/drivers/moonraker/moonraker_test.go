package moonraker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/protocoltrace"
)

func testProfile(host string) driver.ProfileInput {
	return driver.ProfileInput{
		Name:    "mk1",
		Driver:  "moonraker",
		Host:    host,
		Timeout: 0,
	}
}

func testSecrets() driver.SecretsBundle {
	return driver.SecretsBundle{AccessCode: "secret-key"}
}

type captureTraceSink struct {
	mu     sync.Mutex
	events []protocoltrace.Event
}

func (s *captureTraceSink) Emit(e protocoltrace.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *captureTraceSink) Events() []protocoltrace.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]protocoltrace.Event, len(s.events))
	copy(out, s.events)
	return out
}

func TestConnectCheck_UsesAPIKey(t *testing.T) {
	var gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-Api-Key")
		if r.URL.Path != "/server/info" {
			t.Fatalf("path = %s, want /server/info", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"result":{"klippy_connected":true}}`))
	}))
	defer srv.Close()

	drv := New()
	fp, err := drv.ConnectCheck(context.Background(), testProfile(srv.URL), testSecrets())
	if err != nil {
		t.Fatalf("ConnectCheck error: %v", err)
	}
	if fp != "" {
		t.Fatalf("fingerprint = %q, want empty", fp)
	}
	if gotAPIKey != "secret-key" {
		t.Fatalf("X-Api-Key = %q, want secret-key", gotAPIKey)
	}
}

func TestConnectCheck_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"unauthorized"}}`))
	}))
	defer srv.Close()

	drv := New()
	_, err := drv.ConnectCheck(context.Background(), testProfile(srv.URL), testSecrets())
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr *apperr.ExitError
	if !strings.Contains(err.Error(), "Moonraker authentication rejected") {
		t.Fatalf("error = %v, want auth rejection", err)
	}
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("exit code = %v, want 3", err)
	}
}

func TestStatus_MapsCoreFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/printer/objects/query" {
			t.Fatalf("path = %s, want /printer/objects/query", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"result": {
				"status": {
					"print_stats": {"state":"printing","filename":"cube.gcode"},
					"virtual_sdcard": {"progress": 0.42},
					"extruder": {"temperature": 212.5, "target": 220},
					"heater_bed": {"temperature": 59.5, "target": 60}
				}
			}
		}`))
	}))
	defer srv.Close()

	drv := New()
	res, err := drv.Status(context.Background(), testProfile(srv.URL), testSecrets(), nil)
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if res.State != "printing" {
		t.Fatalf("state = %q, want printing", res.State)
	}
	if res.Job == nil || res.Job.Name != "cube.gcode" {
		t.Fatalf("job = %+v, want cube.gcode", res.Job)
	}
	if res.Progress == nil || res.Progress.Percent != 42 {
		t.Fatalf("progress = %+v, want 42%%", res.Progress)
	}
	if res.Temperatures == nil || res.Temperatures.Nozzle == nil || res.Temperatures.Bed == nil {
		t.Fatalf("temperatures not mapped: %+v", res.Temperatures)
	}
}

func TestStatus_EmitsProtocolTrace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"status":{"print_stats":{"state":"idle"}}}}`))
	}))
	defer srv.Close()

	drv := New()
	sink := &captureTraceSink{}
	ctx := protocoltrace.WithSink(context.Background(), sink)
	_, err := drv.Status(ctx, testProfile(srv.URL), testSecrets(), nil)
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	events := sink.Events()
	if len(events) == 0 {
		t.Fatal("expected protocol trace event")
	}
	last := events[len(events)-1]
	if last.Driver != "moonraker" || last.Operation != "Status" || last.Transport != "http" {
		t.Fatalf("unexpected trace event: %+v", last)
	}
	if last.ErrorCategory != "" {
		t.Fatalf("unexpected trace error category: %q", last.ErrorCategory)
	}
}

func TestFileDownload_StreamsBody(t *testing.T) {
	const payload = "gcode-bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/server/files/gcodes/models/cube.gcode" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()

	drv := New()
	var dst strings.Builder
	out, err := drv.FileDownload(context.Background(), testProfile(srv.URL), testSecrets(), "gcodes", "/models/cube.gcode", &dst, nil)
	if err != nil {
		t.Fatalf("FileDownload error: %v", err)
	}
	if dst.String() != payload {
		t.Fatalf("downloaded payload = %q, want %q", dst.String(), payload)
	}
	if out == nil || out.BytesTransferred == nil || *out.BytesTransferred != int64(len(payload)) {
		t.Fatalf("bytes transferred = %+v", out)
	}
}

func TestFileDownload_EmitsProtocolTrace(t *testing.T) {
	const payload = "gcode-bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()

	drv := New()
	var dst strings.Builder
	sink := &captureTraceSink{}
	ctx := protocoltrace.WithSink(context.Background(), sink)
	_, err := drv.FileDownload(ctx, testProfile(srv.URL), testSecrets(), "gcodes", "/cube.gcode", &dst, nil)
	if err != nil {
		t.Fatalf("FileDownload error: %v", err)
	}
	events := sink.Events()
	if len(events) == 0 {
		t.Fatal("expected protocol trace event")
	}
	last := events[len(events)-1]
	if last.Operation != "FileDownload" || last.ByteCount == nil || *last.ByteCount != int64(len(payload)) {
		t.Fatalf("unexpected trace event: %+v", last)
	}
}

func TestRequest_DoesNotFollowRedirect(t *testing.T) {
	redirectTargetHit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stolen" {
			redirectTargetHit = true
			_, _ = w.Write([]byte(`{"result":{}}`))
			return
		}
		http.Redirect(w, r, "/stolen", http.StatusFound)
	}))
	defer srv.Close()

	drv := New()
	_, err := drv.ConnectCheck(context.Background(), testProfile(srv.URL), testSecrets())
	if err == nil {
		t.Fatal("expected error for redirect response")
	}
	if redirectTargetHit {
		t.Fatal("client followed redirect; X-Api-Key could leak to redirect target")
	}
	if strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("error leaks API key: %v", err)
	}
}

func TestRequestJSON_RejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		big := strings.Repeat(" ", maxJSONResponseBytes)
		_, _ = io.WriteString(w, `{"result":{}}`+big)
	}))
	defer srv.Close()

	drv := New()
	_, err := drv.ConnectCheck(context.Background(), testProfile(srv.URL), testSecrets())
	if err == nil {
		t.Fatal("expected error for oversized response")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %v, want response-too-large", err)
	}
}

func TestNormalizeBaseURL_SchemeValidation(t *testing.T) {
	cases := []struct {
		host    string
		want    string
		wantErr bool
	}{
		{host: "printer.local", want: "http://printer.local:7125"},
		{host: "http://printer.local:7125", want: "http://printer.local:7125"},
		{host: "https://printer.local:443", want: "https://printer.local:443"},
		{host: "ftp://printer.local", wantErr: true},
		{host: "file:///etc/passwd", wantErr: true},
	}
	for _, tc := range cases {
		got, err := normalizeBaseURL(tc.host)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("normalizeBaseURL(%q) = %q, want error", tc.host, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("normalizeBaseURL(%q) error: %v", tc.host, err)
		}
		if got != tc.want {
			t.Fatalf("normalizeBaseURL(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

// Auxiliary control tests

// auxServer serves gcode-script and objects-query endpoints, capturing scripts
// and answering object queries with the given status JSON.
func auxServer(t *testing.T, statusJSON string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var scripts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/printer/gcode/script":
			mu.Lock()
			scripts = append(scripts, r.URL.Query().Get("script"))
			mu.Unlock()
			_, _ = w.Write([]byte(`{"result":{}}`))
		case "/printer/objects/query":
			_, _ = w.Write([]byte(`{"result":{"status":{` + statusJSON + `}}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(scripts))
		copy(out, scripts)
		return out
	}
}

func auxShortCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

func TestFanSet_PartCooling_EchoConfirms(t *testing.T) {
	srv, scripts := auxServer(t, `"fan":{"speed":0.6}`)
	defer srv.Close()

	drv := New()
	target := driver.FanTarget{Fan: "partCooling", SpeedPercent: 60}
	result, err := drv.FanSet(context.Background(), testProfile(srv.URL), testSecrets(), nil, target)
	if err != nil {
		t.Fatalf("FanSet error: %v", err)
	}

	if result.Fan != "partCooling" || result.SpeedPercent != 60 {
		t.Errorf("result = %+v, want partCooling/60", result)
	}
	got := scripts()
	if len(got) != 1 || got[0] != "M106 S153" {
		t.Errorf("scripts = %v, want [M106 S153]", got)
	}
}

func TestFanSet_NoEcho_TimesOut(t *testing.T) {
	srv, _ := auxServer(t, `"fan":{"speed":0.0}`)
	defer srv.Close()

	drv := New()
	target := driver.FanTarget{Fan: "partCooling", SpeedPercent: 60}
	_, err := drv.FanSet(auxShortCtx(t), testProfile(srv.URL), testSecrets(), nil, target)
	if err == nil {
		t.Fatal("expected timeout error")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("exit code = %v, want 4", err)
	}
}

func TestFanSet_AuxiliaryAndChamber_Unsupported(t *testing.T) {
	// Stock Klipper M106 has no fan index; only partCooling is portable.
	srv, scripts := auxServer(t, `"fan":{"speed":0.0}`)
	defer srv.Close()

	drv := New()
	for _, fan := range []string{"auxiliary", "chamber", "unknown"} {
		target := driver.FanTarget{Fan: fan, SpeedPercent: 50}
		_, err := drv.FanSet(context.Background(), testProfile(srv.URL), testSecrets(), nil, target)
		if err == nil {
			t.Fatalf("FanSet(%s): expected error", fan)
		}
		var exitErr *apperr.ExitError
		if !errors.As(err, &exitErr) || exitErr.Code != 5 {
			t.Errorf("FanSet(%s): exit code = %v, want 5", fan, err)
		}
	}
	if got := scripts(); len(got) != 0 {
		t.Errorf("scripts = %v, want none", got)
	}
}

func TestLightControl_NotSupported(t *testing.T) {
	drv := New()
	if drv.Capabilities().LightControl {
		t.Error("LightControl capability must be false for moonraker")
	}
	if _, ok := any(drv).(driver.LightDriver); ok {
		t.Error("moonraker driver must not implement LightDriver")
	}
}

func TestSpeedSet_AllProfiles_EchoConfirms(t *testing.T) {
	cases := []struct {
		profile string
		percent int
		factor  string
	}{
		{"silent", 20, "0.2"},
		{"standard", 100, "1.0"},
		{"sport", 150, "1.5"},
		{"ludicrous", 300, "3.0"},
	}

	for _, tc := range cases {
		srv, scripts := auxServer(t, `"gcode_move":{"speed_factor":`+tc.factor+`}`)

		drv := New()
		result, err := drv.SpeedSet(context.Background(), testProfile(srv.URL), testSecrets(), nil, tc.profile)
		if err != nil {
			t.Fatalf("SpeedSet(%s) error: %v", tc.profile, err)
		}

		if result.SpeedProfile != tc.profile {
			t.Errorf("profile = %q, want %q", result.SpeedProfile, tc.profile)
		}

		expected := fmt.Sprintf("M220 S%d", tc.percent)
		if got := scripts(); len(got) != 1 || got[0] != expected {
			t.Errorf("SpeedSet(%s) scripts = %v, want [%s]", tc.profile, got, expected)
		}

		srv.Close()
	}
}

func TestSpeedSet_NoEcho_TimesOut(t *testing.T) {
	srv, _ := auxServer(t, `"gcode_move":{"speed_factor":1.0}`)
	defer srv.Close()

	drv := New()
	_, err := drv.SpeedSet(auxShortCtx(t), testProfile(srv.URL), testSecrets(), nil, "sport")
	if err == nil {
		t.Fatal("expected timeout error")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("exit code = %v, want 4", err)
	}
}

func TestSpeedSet_UnsupportedProfile(t *testing.T) {
	srv, scripts := auxServer(t, `"gcode_move":{"speed_factor":1.0}`)
	defer srv.Close()

	drv := New()
	_, err := drv.SpeedSet(context.Background(), testProfile(srv.URL), testSecrets(), nil, "unknown")
	if err == nil {
		t.Fatal("expected error for unsupported profile")
	}

	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Errorf("exit code = %v, want 5", err)
	}
	if got := scripts(); len(got) != 0 {
		t.Errorf("scripts = %v, want none", got)
	}
}

func TestSpeedProfileToPercent_AllProfiles(t *testing.T) {
	cases := []struct {
		profile string
		percent int
		ok      bool
	}{
		{"silent", 20, true},
		{"standard", 100, true},
		{"sport", 150, true},
		{"ludicrous", 300, true},
		{"unknown", 0, false},
	}

	for _, tc := range cases {
		percent, ok := speedProfileToPercent(tc.profile)
		if ok != tc.ok {
			t.Errorf("speedProfileToPercent(%q): got ok=%v, want %v", tc.profile, ok, tc.ok)
		}
		if ok && percent != tc.percent {
			t.Errorf("speedProfileToPercent(%q): got %d, want %d", tc.profile, percent, tc.percent)
		}
	}
}
