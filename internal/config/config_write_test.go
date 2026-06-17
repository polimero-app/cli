package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/polimero-app/cli/internal/config"
)

func makeProfile() config.Profile {
	now := time.Now().UTC().Truncate(time.Second)
	return config.Profile{
		Driver:  "bambu-lan",
		Host:    "192.0.2.10",
		Serial:  "01S09C450100XXX",
		Timeout: "10s",
		Created: now,
		Updated: now,
	}
}

func TestConfigDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	got, err := config.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("ConfigDir() = %q, want %q", got, dir)
	}
}

func TestGetProfile_Found(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	_ = cfg.AddProfile("alpha", makeProfile())
	p, ok := cfg.GetProfile("alpha")
	if !ok {
		t.Fatal("expected profile to be found")
	}
	if p.Driver != "bambu-lan" {
		t.Errorf("Driver = %q, want bambu-lan", p.Driver)
	}
}

func TestGetProfile_NotFound(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	if _, ok := cfg.GetProfile("missing"); ok {
		t.Fatal("expected false for missing profile")
	}
}

func TestAddProfile_Duplicate(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	_ = cfg.AddProfile("dup", makeProfile())
	if err := cfg.AddProfile("dup", makeProfile()); !errors.Is(err, config.ErrProfileAlreadyExists) {
		t.Errorf("expected ErrProfileAlreadyExists, got %v", err)
	}
}

func TestRemoveProfile_Found(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	_ = cfg.AddProfile("toremove", makeProfile())

	removed, err := cfg.RemoveProfile("toremove")
	if err != nil {
		t.Fatal(err)
	}
	if removed.Host != "192.0.2.10" {
		t.Errorf("removed.Host = %q, want 192.0.2.10", removed.Host)
	}
	if _, ok := cfg.GetProfile("toremove"); ok {
		t.Error("profile still present after remove")
	}
}

func TestRemoveProfile_NotFound(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	if _, err := cfg.RemoveProfile("missing"); !errors.Is(err, config.ErrProfileNotFound) {
		t.Errorf("expected ErrProfileNotFound, got %v", err)
	}
}

func TestSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	p := makeProfile()
	_ = cfg.AddProfile("myprinter", p)

	if err := config.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}

	reloaded, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.GetProfile("myprinter")
	if !ok {
		t.Fatal("profile missing after reload")
	}
	if got.Host != p.Host {
		t.Errorf("Host = %q, want %q", got.Host, p.Host)
	}
	if got.Serial != p.Serial {
		t.Errorf("Serial = %q, want %q", got.Serial, p.Serial)
	}
}

func TestSave_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	if err := config.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "polimero.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions = %04o, want 0600", perm)
	}
}

func TestSave_CreatesDirIfAbsent(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "new-subdir")
	cfg, _ := config.Open(dir)
	if err := config.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "polimero.yaml")); err != nil {
		t.Errorf("config file not created: %v", err)
	}
}

func TestSave_RemovePreservesOthers(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	_ = cfg.AddProfile("keep", makeProfile())
	_ = cfg.AddProfile("drop", makeProfile())
	_, _ = cfg.RemoveProfile("drop")
	_ = config.Save(dir, cfg)

	reloaded, _ := config.Open(dir)
	if _, ok := reloaded.GetProfile("keep"); !ok {
		t.Error("kept profile missing after save")
	}
	if _, ok := reloaded.GetProfile("drop"); ok {
		t.Error("dropped profile still present after save")
	}
}

func TestSave_ReadOnlyDir_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	_ = cfg.AddProfile("test", config.Profile{
		Driver:  "bambu-lan",
		Host:    "192.168.1.1",
		Serial:  "SN001",
		Timeout: "10s",
	})

	if err := os.Chmod(dir, 0555); err != nil {
		t.Skipf("cannot make dir read-only: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0755) }()

	err := config.Save(dir, cfg)
	if err == nil {
		t.Error("expected error saving to read-only directory")
	}
}
