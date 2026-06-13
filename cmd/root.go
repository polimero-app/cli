package cmd

import (
	"errors"
	"os"

	"github.com/polimero-app/cli/cmd/printer"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/spf13/cobra"
)

// NewRoot creates and returns a fully wired root command.
// Use this in tests to avoid global state.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "polimero",
		Short:         "CLI for interacting with 3D printers",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().String("output", "human", "output format: human or json")
	root.AddCommand(printer.Command())
	return root
}

// Execute builds and runs the root command, then exits the process.
// Exit codes come from *apperr.ExitError returned by subcommands.
func Execute() {
	root := NewRoot()
	if err := root.Execute(); err != nil {
		var exitErr *apperr.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		os.Exit(1)
	}
}
