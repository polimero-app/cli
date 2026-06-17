package cmd_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/polimero-app/cli/cmd"
	"github.com/polimero-app/cli/internal/apperr"
)

func TestNewRoot_HasExpectedCommands(t *testing.T) {
	root := cmd.NewRoot()
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	if !names["printer"] {
		t.Error("expected 'printer' subcommand")
	}
}

func TestNewRoot_HasGlobalFlags(t *testing.T) {
	root := cmd.NewRoot()
	if f := root.PersistentFlags().Lookup("output"); f == nil {
		t.Error("expected --output persistent flag")
	}
	if f := root.PersistentFlags().Lookup("verbose"); f == nil {
		t.Error("expected --verbose persistent flag")
	}
}

func TestNewRoot_UnknownCommand_ReturnsError(t *testing.T) {
	root := cmd.NewRoot()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"nonexistent"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestNewRoot_PrinterStatus_MissingProfile_ReturnsExitCode2(t *testing.T) {
	root := cmd.NewRoot()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"printer", "status"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing profile name")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *apperr.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != 2 {
		t.Errorf("exit code = %d, want 2", exitErr.Code)
	}
}
