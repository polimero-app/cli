package files

import (
	"log/slog"

	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/spf13/cobra"
)

// Deps holds injectable dependencies for the files commands.
type Deps struct {
	KC        keychain.Keychain
	GetDriver func(string) (driver.Driver, bool)
	Log       *slog.Logger
}

// Command returns the top-level "files" cobra command group.
func Command() *cobra.Command {
	return CommandWithDeps(Deps{
		KC:        keychain.NewReal(),
		GetDriver: drivers.Get,
		Log:       slog.Default(),
	})
}

// CommandWithDeps constructs the "files" cobra command group with injected dependencies.
func CommandWithDeps(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files",
		Short: "File operations on a named printer",
	}
	cmd.AddCommand(rootsCommandWithDeps(deps))
	cmd.AddCommand(listCommandWithDeps(deps))
	cmd.AddCommand(downloadCommandWithDeps(deps))
	cmd.AddCommand(uploadCommandWithDeps(deps))
	return cmd
}
