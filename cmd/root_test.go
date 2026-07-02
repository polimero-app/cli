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

func TestRun_UnknownCommand_PrintsErrorAndExits2(t *testing.T) {
	errOut := &bytes.Buffer{}
	code := cmd.Run([]string{"nonexistent"}, errOut)
	if code != 2 {
		t.Errorf("expected exit code 2, got %d", code)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Errorf("expected unknown-command message on stderr, got %q", errOut.String())
	}
}

func TestRun_UnknownFlag_PrintsErrorAndExits2(t *testing.T) {
	errOut := &bytes.Buffer{}
	code := cmd.Run([]string{"--bogus-flag"}, errOut)
	if code != 2 {
		t.Errorf("expected exit code 2, got %d", code)
	}
	if !strings.Contains(errOut.String(), "bogus-flag") {
		t.Errorf("expected flag error message on stderr, got %q", errOut.String())
	}
}

func TestRun_InvalidFlagValue_PrintsErrorAndExits2(t *testing.T) {
	errOut := &bytes.Buffer{}
	code := cmd.Run([]string{"--verbose=notabool"}, errOut)
	if code != 2 {
		t.Errorf("expected exit code 2, got %d", code)
	}
	if errOut.Len() == 0 {
		t.Error("expected an error message on stderr")
	}
}

func TestRun_Help_Exits0(t *testing.T) {
	errOut := &bytes.Buffer{}
	if code := cmd.Run([]string{"--help"}, errOut); code != 0 {
		t.Errorf("expected exit code 0 for --help, got %d", code)
	}
	if errOut.Len() != 0 {
		t.Errorf("expected no stderr output for --help, got %q", errOut.String())
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
