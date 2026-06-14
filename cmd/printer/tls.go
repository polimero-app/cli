package printer

import "github.com/spf13/cobra"

func tlsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tls",
		Short: "Manage TLS settings for a printer profile",
	}
	cmd.AddCommand(tlsRefreshCommand())
	return cmd
}
