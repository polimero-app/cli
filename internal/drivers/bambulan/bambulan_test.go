package bambulan_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/drivers/bambulan"
)

func TestName(t *testing.T) {
	if got := bambulan.New().Name(); got != "bambu-lan" {
		t.Errorf("Name() = %q, want %q", got, "bambu-lan")
	}
}

func TestConnectCheck_Insecure_NoConnection(t *testing.T) {
	drv := bambulan.New()
	fp, err := drv.ConnectCheck(context.Background(), "127.0.0.1", "SN123", "code", true, 5*time.Second)
	if err != nil {
		t.Fatalf("insecure ConnectCheck returned error: %v", err)
	}
	if fp != "" {
		t.Errorf("expected empty fingerprint for insecure mode, got %q", fp)
	}
}

func TestConnectCheck_UnreachableHost_ExitsCode4(t *testing.T) {
	// 192.0.2.1 is TEST-NET-1 (RFC 5737), guaranteed unreachable on any network.
	drv := bambulan.New()
	_, err := drv.ConnectCheck(context.Background(), "192.0.2.1", "SN123", "code", false, 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected error connecting to unreachable host")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *apperr.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != 4 {
		t.Errorf("exit code = %d, want 4 (network error)", exitErr.Code)
	}
}

func TestCapabilities_StatusTrue(t *testing.T) {
	caps := bambulan.New().Capabilities()
	if !caps.Status {
		t.Error("Capabilities().Status should be true for bambu-lan driver")
	}
}
