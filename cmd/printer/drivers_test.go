package printer_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/polimero-app/cli/cmd/printer"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/spf13/cobra"
)

func runDrivers(t *testing.T, list func() []drivers.Info, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(printer.DriversCommandWithDeps(list))
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"drivers"}, args...))
	err := root.Execute()
	return buf.String(), err
}

func TestDrivers_Human(t *testing.T) {
	out, err := runDrivers(t, func() []drivers.Info {
		return []drivers.Info{{Name: "bambu-lan", Description: "Bambu Lab printers over LAN mode"}}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "DRIVER") || !strings.Contains(out, "DESCRIPTION") {
		t.Fatalf("missing header:\n%s", out)
	}
	if !strings.Contains(out, "bambu-lan") {
		t.Fatalf("missing bambu-lan:\n%s", out)
	}
	if !strings.Contains(out, "Bambu Lab printers over LAN mode") {
		t.Fatalf("missing description:\n%s", out)
	}
}

func TestDrivers_WiredUnderPrinter(t *testing.T) {
	out, err := runDriversFromRoot(t, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "bambu-lan") {
		t.Fatalf("missing bambu-lan from wired command:\n%s", out)
	}
}

func TestDrivers_Human_AlphabeticalOrder(t *testing.T) {
	out, err := runDrivers(t, func() []drivers.Info {
		return []drivers.Info{
			{Name: "moonraker", Description: "Moonraker-compatible Klipper printers"},
			{Name: "bambu-lan", Description: "Bambu Lab printers over LAN mode"},
		}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	idxBambu := strings.Index(out, "bambu-lan")
	idxMoonraker := strings.Index(out, "moonraker")
	if idxBambu < 0 || idxMoonraker < 0 {
		t.Fatalf("drivers not found:\n%s", out)
	}
	if idxBambu > idxMoonraker {
		t.Fatalf("drivers should be sorted alphabetically:\n%s", out)
	}
}

func runDriversFromRoot(t *testing.T, configDir string, args ...string) (string, error) {
	t.Helper()
	root := testRoot(t, configDir)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"printer", "drivers"}, args...))
	err := root.Execute()
	return buf.String(), err
}

func TestDrivers_JSON(t *testing.T) {
	out, err := runDrivers(t, func() []drivers.Info {
		return []drivers.Info{{Name: "bambu-lan", Description: "Bambu Lab printers over LAN mode"}}
	}, "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	if env["ok"] != true {
		t.Fatalf("ok = %v, want true", env["ok"])
	}
	data := env["data"].(map[string]any)
	drivers := data["drivers"].([]any)
	if len(drivers) != 1 {
		t.Fatalf("drivers length = %d, want 1", len(drivers))
	}
	first := drivers[0].(map[string]any)
	if first["name"] != "bambu-lan" {
		t.Fatalf("driver name = %v, want bambu-lan", first["name"])
	}
	if first["description"] != "Bambu Lab printers over LAN mode" {
		t.Fatalf("driver description = %v, want Bambu Lab printers over LAN mode", first["description"])
	}
	if env["error"] != nil {
		t.Fatalf("error = %v, want nil", env["error"])
	}
	meta := env["meta"].(map[string]any)
	if meta["command"] != "printer drivers" {
		t.Fatalf("meta.command = %v, want printer drivers", meta["command"])
	}
}

func TestDrivers_InvalidOutputFormat(t *testing.T) {
	_, err := runDrivers(t, func() []drivers.Info {
		return []drivers.Info{{Name: "bambu-lan", Description: "Bambu Lab printers over LAN mode"}}
	}, "--output", "xml")
	if err == nil {
		t.Fatal("expected error")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *apperr.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("exit code = %d, want 2", exitErr.Code)
	}
}
