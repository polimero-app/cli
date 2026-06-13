package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/polimero-app/cli/internal/output"
)

func TestParseFormat(t *testing.T) {
	tests := []struct {
		input   string
		want    output.Format
		wantErr bool
	}{
		{"human", output.FormatHuman, false},
		{"json", output.FormatJSON, false},
		{"xml", "", true},
		{"JSON", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := output.ParseFormat(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseFormat(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("ParseFormat(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWriteEnvelope_Success(t *testing.T) {
	var buf bytes.Buffer
	err := output.WriteEnvelope(&buf, output.Envelope{
		OK:    true,
		Data:  map[string]any{"profiles": []any{}},
		Error: nil,
		Meta:  output.Meta{Command: "printer list"},
	})
	if err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	var got map[string]any
	if jsonErr := json.Unmarshal(buf.Bytes(), &got); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jsonErr, buf.String())
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want true", got["ok"])
	}
	if got["error"] != nil {
		t.Errorf("error = %v, want null", got["error"])
	}
	meta, ok := got["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is not an object")
	}
	if meta["command"] != "printer list" {
		t.Errorf("meta.command = %v, want printer list", meta["command"])
	}
	if _, hasDuration := meta["durationMs"]; hasDuration {
		t.Error("meta.durationMs should be absent for non-network commands")
	}
}

func TestWriteEnvelope_Error(t *testing.T) {
	var buf bytes.Buffer
	err := output.WriteEnvelope(&buf, output.Envelope{
		OK:   false,
		Data: nil,
		Error: &output.ErrDetail{
			Code:    "config_error",
			Message: "failed to load config",
			Details: map[string]any{"path": "/home/user/.config/polimero/polimero.yaml"},
		},
		Meta: output.Meta{Command: "printer list"},
	})
	if err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	if !strings.Contains(buf.String(), "config_error") {
		t.Errorf("expected code in output:\n%s", buf.String())
	}

	var got map[string]any
	if jsonErr := json.Unmarshal(buf.Bytes(), &got); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v", jsonErr)
	}
	if got["ok"] != false {
		t.Errorf("ok = %v, want false", got["ok"])
	}
	if got["data"] != nil {
		t.Errorf("data = %v, want null", got["data"])
	}
}

func TestWriteEnvelope_WithDuration(t *testing.T) {
	dur := int64(148)
	var buf bytes.Buffer
	output.WriteEnvelope(&buf, output.Envelope{ //nolint:errcheck
		OK:   true,
		Data: map[string]any{},
		Meta: output.Meta{Command: "printer status", DurationMs: &dur},
	})

	var got map[string]any
	json.Unmarshal(buf.Bytes(), &got) //nolint:errcheck
	meta := got["meta"].(map[string]any)
	if meta["durationMs"] != float64(148) {
		t.Errorf("meta.durationMs = %v, want 148", meta["durationMs"])
	}
}
