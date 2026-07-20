package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/polimero-app/cli/cmd/camera"
	"github.com/polimero-app/cli/cmd/fans"
	"github.com/polimero-app/cli/cmd/files"
	"github.com/polimero-app/cli/cmd/jobs"
	"github.com/polimero-app/cli/cmd/lights"
	"github.com/polimero-app/cli/cmd/motion"
	"github.com/polimero-app/cli/cmd/printer"
	"github.com/polimero-app/cli/cmd/speed"
	"github.com/polimero-app/cli/cmd/status"
	"github.com/polimero-app/cli/cmd/temperature"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/spf13/cobra"
)

// NewRoot creates and returns a fully wired root command.
// Use this in tests to avoid global state.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "polimero",
		Short:         "CLI for interacting with 3D printers",
		Version:       Version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().String("output", "human", "output format: human or json")
	root.PersistentFlags().BoolP("verbose", "v", false, "show detailed progress output")
	root.AddCommand(printer.Command())
	root.AddCommand(status.Command())
	root.AddCommand(camera.Command())
	root.AddCommand(files.Command())
	root.AddCommand(temperature.Command())
	root.AddCommand(motion.Command())
	root.AddCommand(jobs.Command())
	root.AddCommand(fans.Command())
	root.AddCommand(lights.Command())
	root.AddCommand(speed.Command())
	return root
}

// Execute builds and runs the root command, then exits the process.
// Exit codes come from *apperr.ExitError returned by subcommands.
func Execute() {
	os.Exit(Run(os.Args[1:], os.Stderr))
}

// Run executes the root command with the given arguments and returns the
// process exit code. Errors produced by cobra itself (unknown commands or
// flags, bad flag values) never pass through the per-command output
// helpers, so they are printed here and mapped to exit code 2 per the
// usage-error contract.
func Run(args []string, errOut io.Writer) int {
	root := NewRoot()
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return 0
	}
	var exitErr *apperr.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	_, _ = fmt.Fprintf(errOut, "Error: %s\n", err)
	return 2
}
