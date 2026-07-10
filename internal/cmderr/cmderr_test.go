package cmderr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/cmderr"
	"github.com/polimero-app/cli/internal/output"
	"github.com/spf13/cobra"
)

func TestExitCode(t *testing.T) {
	if got := cmderr.ExitCode(errors.New("plain")); got != 1 {
		t.Errorf("ExitCode(plain) = %d, want 1", got)
	}
	if got := cmderr.ExitCode(apperr.New(4, "net")); got != 4 {
		t.Errorf("ExitCode(apperr 4) = %d, want 4", got)
	}
}

func TestCode(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		classifyTimeout bool
		want            string
	}{
		{"plain error", errors.New("x"), false, "error"},
		{"config", apperr.New(2, "bad flag"), false, "config_error"},
		{"secret missing", apperr.New(3, "access code not found"), false, "secret_not_found"},
		{"tls mismatch", apperr.New(3, "TLS fingerprint mismatch"), false, "authentication_failed"},
		{"mqtt auth", apperr.New(3, "MQTT authentication rejected"), false, "authentication_failed"},
		{"moonraker auth", apperr.New(3, "Moonraker authentication rejected"), false, "authentication_failed"},
		{"network", apperr.New(4, "connection failed"), false, "connection_failed"},
		{"timeout unclassified", apperr.New(4, "request timed out"), false, "connection_failed"},
		{"timeout classified", apperr.New(4, "request timed out"), true, "timeout"},
		{"unsupported", apperr.New(5, "no capability"), false, "capability_unsupported"},
		{"general", apperr.New(1, "boom"), false, "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cmderr.Code(tt.err, tt.classifyTimeout); got != tt.want {
				t.Errorf("Code() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsTimeout(t *testing.T) {
	if cmderr.IsTimeout(apperr.New(3, "timed out")) {
		t.Error("IsTimeout should require exit code 4")
	}
	if !cmderr.IsTimeout(apperr.New(4, "request timed out")) {
		t.Error("IsTimeout should match 'timed out' message")
	}
	if !cmderr.IsTimeout(apperr.Wrap(4, "failed", context.DeadlineExceeded)) {
		t.Error("IsTimeout should match wrapped DeadlineExceeded")
	}
	if cmderr.IsTimeout(apperr.New(4, "connection refused")) {
		t.Error("IsTimeout should not match plain network error")
	}
}

func TestCommandMessage(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{apperr.New(3, "MQTT authentication rejected by printer: TLS fingerprint mismatch"), "MQTT authentication rejected"},
		{apperr.New(3, "Moonraker authentication rejected"), "Moonraker authentication rejected"},
		{apperr.New(3, "TLS fingerprint mismatch"), "TLS fingerprint mismatch"},
		{apperr.New(3, "access code not found"), "secret not found"},
		{apperr.New(4, "command was cancelled"), "request cancelled"},
		{apperr.New(4, "command subscription failed: broker"), "command subscription failed"},
		{apperr.New(4, "command publish failed: broker"), "command publish failed"},
		{apperr.New(4, "connection failed: refused"), "connection failed"},
		{apperr.New(4, "weird transport thing"), "command failed"},
		{apperr.New(4, "request timed out"), "command timed out"},
		{apperr.New(2, "bad flag"), "bad flag"},
	}
	for _, tt := range tests {
		if got := cmderr.CommandMessage(tt.err); got != tt.want {
			t.Errorf("CommandMessage(%q) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

func TestWrite_JSONEnvelope(t *testing.T) {
	var out, errOut bytes.Buffer
	err := apperr.New(4, "connection failed")
	detail := output.ErrDetail{Code: "connection_failed", Message: "connection failed"}
	ret := cmderr.Write(&out, &errOut, output.FormatJSON, "status", detail, err)

	var exitErr *apperr.ExitError
	if !errors.As(ret, &exitErr) || exitErr.Code != 4 || exitErr.Msg != "" {
		t.Errorf("Write returned %v, want empty-message ExitError code 4", ret)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal(out.Bytes(), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out.String())
	}
	if env["ok"] != false {
		t.Error("envelope ok should be false")
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "connection_failed" || errObj["message"] != "connection failed" {
		t.Errorf("error object = %v", errObj)
	}
	if env["meta"].(map[string]any)["command"] != "status" {
		t.Errorf("meta.command = %v, want status", env["meta"])
	}
	if errOut.Len() != 0 {
		t.Errorf("JSON mode should not write to errOut, got %q", errOut.String())
	}
}

func TestWrite_HumanStderr(t *testing.T) {
	var out, errOut bytes.Buffer
	err := apperr.New(2, "bad thing")
	detail := output.ErrDetail{Code: "config_error", Message: "bad thing"}
	ret := cmderr.Write(&out, &errOut, output.FormatHuman, "files list", detail, err)

	if cmderr.ExitCode(ret) != 2 {
		t.Errorf("exit code = %d, want 2", cmderr.ExitCode(ret))
	}
	if out.Len() != 0 {
		t.Errorf("human mode should not write to out, got %q", out.String())
	}
	if got := errOut.String(); got != "Error: bad thing\n" {
		t.Errorf("errOut = %q", got)
	}
}

func TestWriteDetail(t *testing.T) {
	var out, errOut bytes.Buffer
	ret := cmderr.WriteDetail(&out, &errOut, output.FormatJSON, "temperature set", 2,
		"value_out_of_range", "nozzle out of range", map[string]any{"value": "NaN"})

	var exitErr *apperr.ExitError
	if !errors.As(ret, &exitErr) || exitErr.Code != 2 || exitErr.Msg != "nozzle out of range" {
		t.Errorf("WriteDetail returned %v", ret)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal(out.Bytes(), &env); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "value_out_of_range" {
		t.Errorf("code = %v", errObj["code"])
	}
	if errObj["details"].(map[string]any)["value"] != "NaN" {
		t.Errorf("details = %v", errObj["details"])
	}
}

func newTestCmd(outputVal string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("output", outputVal, "")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	return root, &out, &errOut
}

func TestWriteUsage_Human(t *testing.T) {
	cmd, out, errOut := newTestCmd("human")
	ret := cmderr.WriteUsage(cmd, "jobs start", "expected exactly one profile name")
	if cmderr.ExitCode(ret) != 2 {
		t.Errorf("exit code = %d, want 2", cmderr.ExitCode(ret))
	}
	if !strings.Contains(errOut.String(), "expected exactly one profile name") {
		t.Errorf("errOut = %q", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("out = %q, want empty", out.String())
	}
}

func TestWriteUsage_JSON(t *testing.T) {
	cmd, out, _ := newTestCmd("json")
	ret := cmderr.WriteUsage(cmd, "jobs start", "bad usage")
	if cmderr.ExitCode(ret) != 2 {
		t.Errorf("exit code = %d, want 2", cmderr.ExitCode(ret))
	}
	var env map[string]any
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if env["error"].(map[string]any)["code"] != "config_error" {
		t.Errorf("error = %v", env["error"])
	}
}

func TestWriteUsage_InvalidFormat(t *testing.T) {
	cmd, out, errOut := newTestCmd("yaml")
	ret := cmderr.WriteUsage(cmd, "status", "msg")
	if cmderr.ExitCode(ret) != 2 {
		t.Errorf("exit code = %d, want 2", cmderr.ExitCode(ret))
	}
	if !strings.Contains(errOut.String(), "Error:") {
		t.Errorf("errOut = %q, want format error", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("out = %q, want empty", out.String())
	}
}
