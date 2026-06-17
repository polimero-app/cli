package bambulan_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers/bambulan"
)

func TestName(t *testing.T) {
	if got := bambulan.New().Name(); got != "bambu-lan" {
		t.Errorf("Name() = %q, want %q", got, "bambu-lan")
	}
}

func TestConnectCheck_Insecure_NoConnection(t *testing.T) {
	drv := bambulan.New()
	pi := driver.ProfileInput{Host: "127.0.0.1", Serial: "SN123", Timeout: 5 * time.Second, Insecure: true}
	fp, err := drv.ConnectCheck(context.Background(), pi, driver.SecretsBundle{AccessCode: "code"})
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
	pi := driver.ProfileInput{Host: "192.0.2.1", Serial: "SN123", Timeout: 500 * time.Millisecond}
	_, err := drv.ConnectCheck(context.Background(), pi, driver.SecretsBundle{AccessCode: "code"})
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

func TestValidateProfile_RequiresSerial(t *testing.T) {
	drv := bambulan.New()
	pi := driver.ProfileInput{Host: "192.168.1.1"}
	if err := drv.ValidateProfile(pi); err == nil {
		t.Error("expected error for empty serial, got nil")
	}
}

func TestValidateProfile_AcceptsValidSerial(t *testing.T) {
	drv := bambulan.New()
	pi := driver.ProfileInput{Host: "192.168.1.1", Serial: "01S09C450100XXX"}
	if err := drv.ValidateProfile(pi); err != nil {
		t.Errorf("expected nil for valid serial, got: %v", err)
	}
}

func TestCapabilities_StatusTrue(t *testing.T) {
	caps := bambulan.New().Capabilities()
	if !caps.Status {
		t.Error("Capabilities().Status should be true for bambu-lan driver")
	}
}

func TestCapabilities_TLSRefreshTrue(t *testing.T) {
	caps := bambulan.New().Capabilities()
	if !caps.TLSRefresh {
		t.Error("Capabilities().TLSRefresh should be true for bambu-lan driver")
	}
}

func TestCapabilities_DiscoveryTrue(t *testing.T) {
	d := bambulan.New()
	if !d.Capabilities().Discovery {
		t.Error("expected Capabilities().Discovery to be true")
	}
}
