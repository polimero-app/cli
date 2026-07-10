package moonraker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
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
