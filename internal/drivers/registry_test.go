package drivers_test

import (
	"testing"

	"github.com/polimero-app/cli/internal/drivers"
)

func TestGet_KnownDriver(t *testing.T) {
	d, ok := drivers.Get("bambu-lan")
	if !ok {
		t.Fatal("expected bambu-lan in registry")
	}
	if d.Name() != "bambu-lan" {
		t.Errorf("d.Name() = %q, want bambu-lan", d.Name())
	}
}

func TestGet_UnknownDriver(t *testing.T) {
	if _, ok := drivers.Get("nonexistent"); ok {
		t.Fatal("expected false for unknown driver")
	}
}

func TestNames_ContainsBambuLan(t *testing.T) {
	for _, n := range drivers.Names() {
		if n == "bambu-lan" {
			return
		}
	}
	t.Error("bambu-lan missing from Names()")
}
