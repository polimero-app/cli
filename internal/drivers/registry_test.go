package drivers_test

import (
	"slices"
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

func TestNames_ContainsMoonraker(t *testing.T) {
	for _, n := range drivers.Names() {
		if n == "moonraker" {
			return
		}
	}
	t.Error("moonraker missing from Names()")
}

func TestNames_Sorted(t *testing.T) {
	names := drivers.Names()
	if !slices.IsSorted(names) {
		t.Fatalf("Names() = %v, want sorted", names)
	}
}

func TestList_IncludesDescriptions(t *testing.T) {
	infos := drivers.List()
	var foundMoonraker bool
	for _, info := range infos {
		if info.Name == "bambu-lan" {
			if info.Description == "" {
				t.Fatal("bambu-lan description is empty")
			}
		}
		if info.Name == "moonraker" {
			if info.Description == "" {
				t.Fatal("moonraker description is empty")
			}
			foundMoonraker = true
		}
	}
	for _, info := range infos {
		if info.Name == "bambu-lan" && foundMoonraker {
			return
		}
	}
	t.Fatal("driver list missing expected entries")
}
