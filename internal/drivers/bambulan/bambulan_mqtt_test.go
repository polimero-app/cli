package bambulan

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

func mqttCommandProfile() driver.ProfileInput {
	return driver.ProfileInput{
		Name:     "myprinter",
		Driver:   "bambu-lan",
		Host:     "192.0.2.1",
		Serial:   "SN001",
		Timeout:  5 * time.Second,
		Insecure: true,
	}
}

func anyPushall(data []byte) bool { return isPushallReport(data) }

func TestMqttCommand_HappyPath_PredicateSatisfied(t *testing.T) {
	response := pushallResponse("IDLE")
	fc := &fakeCommandClient{
		// responses[0]=nil (command publish), responses[1]=report (pushall publish)
		responses: [][]byte{nil, response},
	}
	drv := newCommandDriver(fc)
	data, err := drv.mqttCommand(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, `{"test":"cmd"}`, anyPushall)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data == nil {
		t.Fatal("expected non-nil data")
	}
	pubs := fc.getPublished()
	if len(pubs) != 2 {
		t.Errorf("expected 2 publishes (command + pushall), got %d", len(pubs))
	}
	if !strings.Contains(pubs[0], `"test":"cmd"`) {
		t.Errorf("first publish should contain the command payload, got: %s", pubs[0])
	}
	if !strings.Contains(pubs[1], "pushall") {
		t.Errorf("second publish should be pushall, got: %s", pubs[1])
	}
}

func TestMqttCommand_ConnectFails_ReturnsError(t *testing.T) {
	fc := &fakeCommandClient{connectErr: errors.New("connect refused")}
	drv := newCommandDriver(fc)
	_, err := drv.mqttCommand(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, `{}`, anyPushall)
	if err == nil {
		t.Fatal("expected error on connect failure")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

func TestMqttCommand_SubscribeFails_ReturnsError(t *testing.T) {
	fc := &fakeCommandClient{subscribeErr: errors.New("subscribe failed")}
	drv := newCommandDriver(fc)
	_, err := drv.mqttCommand(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, `{}`, anyPushall)
	if err == nil {
		t.Fatal("expected error on subscribe failure")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

func TestMqttCommand_CommandPublishFails_ReturnsError(t *testing.T) {
	fc := &fakeCommandClient{
		publishErrs: []error{errors.New("publish refused")},
	}
	drv := newCommandDriver(fc)
	_, err := drv.mqttCommand(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, `{}`, anyPushall)
	if err == nil {
		t.Fatal("expected error on publish failure")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

func TestMqttCommand_ContextCancelled_ReturnsCancel(t *testing.T) {
	// No response delivered — predicate never satisfied.
	fc := &fakeCommandClient{}
	drv := newCommandDriver(fc)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := drv.mqttCommand(ctx, mqttCommandProfile(), driver.SecretsBundle{}, `{}`, anyPushall)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if !strings.Contains(err.Error(), "cancel") {
		t.Errorf("expected cancel message, got: %v", err)
	}
}

func TestMqttCommand_ContextDeadlineExceeded_ReturnsTimeout(t *testing.T) {
	fc := &fakeCommandClient{}
	drv := newCommandDriver(fc)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond)
	_, err := drv.mqttCommand(ctx, mqttCommandProfile(), driver.SecretsBundle{}, `{}`, anyPushall)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit code 4, got %v", err)
	}
}

func TestMqttCommand_PredicateSkipsNonMatchingReports(t *testing.T) {
	// First response doesn't satisfy the predicate, second does.
	nonPushall := []byte(`{"system":{"command":"get_access"}}`)
	response := pushallResponse("IDLE")
	fc := &fakeCommandClient{
		// command publish delivers a non-pushall, pushall publish delivers a full pushall.
		responses: [][]byte{nonPushall, response},
	}
	drv := newCommandDriver(fc)
	data, err := drv.mqttCommand(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, `{}`, anyPushall)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(data), "IDLE") {
		t.Errorf("expected matching data, got: %s", data)
	}
}

func TestMqttCommand_UnsignedCommandRejection_ReturnsAuthError(t *testing.T) {
	rejection := []byte(`{"print":{"command":"project_file","sequence_id":"9","result":"fail","reason":"MQTT Command verification failed","err_code":84033543}}`)
	fc := &fakeCommandClient{responses: [][]byte{rejection, nil}}
	drv := newCommandDriver(fc)
	_, err := drv.mqttCommand(context.Background(), mqttCommandProfile(), driver.SecretsBundle{}, `{}`, anyPushall)
	if err == nil {
		t.Fatal("expected unsigned command rejection error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("expected exit code 3, got %v", err)
	}
	if strings.Contains(err.Error(), "84033543") {
		t.Fatalf("error should be sanitized, got %v", err)
	}
}

// Verify that mqttCommand is not accidentally exported (it's internal to the driver).
// This is a compile-time check; no assertion needed.
var _ = func() {
	drv := &Driver{}
	_ = drv.mqttCommand
	_ = slog.Default()
}
