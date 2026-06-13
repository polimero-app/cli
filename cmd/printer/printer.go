package printer

import "github.com/spf13/cobra"

// Command returns the "printer" subcommand with all its children attached.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "printer",
		Short: "Manage 3D printer profiles",
	}
	cmd.AddCommand(listCommand())
	return cmd
}
