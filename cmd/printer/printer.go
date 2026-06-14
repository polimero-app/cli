package printer

import (
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

// Command returns the "printer" subcommand with all its children attached.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "printer",
		Short: "Manage 3D printer profiles",
	}
	cmd.AddCommand(listCommand())
	cmd.AddCommand(AddCommandWithDeps(AddDeps{
		KC:        keychain.NewReal(),
		Prompter:  tty.NewReal(),
		GetDriver: drivers.Get,
	}))
	return cmd
}
