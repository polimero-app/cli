package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/polimero-app/cli/internal/config"
)

const twoProfileYAML = `version: 1
profiles:
  garage-x1c:
    driver: bambu-lan
    host: 192.0.2.10
    serial: 01S09C450100ABC
    timeout: 10s
    insecure: false
    created: 2026-06-13T10:00:00Z
    updated: 2026-06-13T10:00:00Z
  attic-p1s:
    driver: bambu-lan
    host: 192.0.2.11
    serial: 01P00C450100XYZ
    timeout: 15s
    insecure: true
    created: 2026-06-13T11:00:00Z
    updated: 2026-06-13T11:00:00Z
`

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "polimero.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestOpen_MissingFile(t *testing.T) {
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error for missing file: %v", err)
	}
	if profiles := cfg.SortedProfiles(); len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestOpen_MissingDir(t *testing.T) {
	cfg, err := config.Open(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error for missing dir: %v", err)
	}
	if profiles := cfg.SortedProfiles(); len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestOpen_EmptyProfiles(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "version: 1\nprofiles: {}\n")

	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profiles := cfg.SortedProfiles(); len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestOpen_ValidProfiles(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, twoProfileYAML)

	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	profiles := cfg.SortedProfiles()
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}
	// SortedProfiles must return alphabetical order.
	if profiles[0].Name != "attic-p1s" {
		t.Errorf("profiles[0].Name = %q, want attic-p1s", profiles[0].Name)
	}
	if profiles[1].Name != "garage-x1c" {
		t.Errorf("profiles[1].Name = %q, want garage-x1c", profiles[1].Name)
	}
	if profiles[1].Driver != "bambu-lan" {
		t.Errorf("Driver = %q, want bambu-lan", profiles[1].Driver)
	}
	if profiles[1].Host != "192.0.2.10" {
		t.Errorf("Host = %q, want 192.0.2.10", profiles[1].Host)
	}
	if profiles[1].Serial != "01S09C450100ABC" {
		t.Errorf("Serial = %q, want 01S09C450100ABC", profiles[1].Serial)
	}
	if profiles[1].Insecure != false {
		t.Error("Insecure = true, want false")
	}
	if profiles[0].Insecure != true {
		t.Error("attic-p1s Insecure = false, want true")
	}
}

func TestOpen_SerialOptional(t *testing.T) {
	// Profiles without serial (future drivers) should load fine.
	dir := t.TempDir()
	writeConfig(t, dir, `version: 1
profiles:
  no-serial:
    driver: some-other-driver
    host: 192.0.2.20
    timeout: 10s
    insecure: false
    created: 2026-06-13T10:00:00Z
    updated: 2026-06-13T10:00:00Z
`)
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	profiles := cfg.SortedProfiles()
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	if profiles[0].Serial != "" {
		t.Errorf("expected empty serial, got %q", profiles[0].Serial)
	}
}

func TestOpen_Timestamps(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, twoProfileYAML)

	cfg, _ := config.Open(dir)
	profiles := cfg.SortedProfiles()

	want := time.Date(2026, 6, 13, 11, 0, 0, 0, time.UTC)
	if !profiles[0].Created.Equal(want) {
		t.Errorf("attic-p1s Created = %v, want %v", profiles[0].Created, want)
	}
}

func TestOpen_UnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "version: 2\nprofiles: {}\n")

	_, err := config.Open(dir)
	if !errors.Is(err, config.ErrUnsupportedVersion) {
		t.Errorf("expected ErrUnsupportedVersion, got %v", err)
	}
}

func TestOpen_Malformed(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "version: 1\nprofiles:\n  - bad\n  yaml: [unclosed\n")

	_, err := config.Open(dir)
	if !errors.Is(err, config.ErrMalformed) {
		t.Errorf("expected ErrMalformed, got %v", err)
	}
}

func TestOpen_VersionZero(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "profiles: {}\n") // no version field → defaults to 0

	_, err := config.Open(dir)
	if !errors.Is(err, config.ErrUnsupportedVersion) {
		t.Errorf("expected ErrUnsupportedVersion for missing version, got %v", err)
	}
}

func TestLoad_EnvVarOverride(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, twoProfileYAML)
	t.Setenv("POLIMERO_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.SortedProfiles()) != 2 {
		t.Errorf("expected 2 profiles via Load(), got %d", len(cfg.SortedProfiles()))
	}
}
