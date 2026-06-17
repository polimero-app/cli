package cmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/polimero-app/cli/cmd"
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
	if !names["status"] {
		t.Error("expected 'status' subcommand")
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

func TestNewRoot_Status_NoArgs_ShowsHelp(t *testing.T) {
	root := cmd.NewRoot()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"status"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error (help), got %v", err)
	}
	if !strings.Contains(buf.String(), "status <name>") {
		t.Errorf("expected usage in help output:\n%s", buf.String())
	}
}
