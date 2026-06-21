package camera

import (
	"log/slog"

	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/spf13/cobra"
)

// Deps holds injectable dependencies for the camera commands.
type Deps struct {
	KC        keychain.Keychain
	GetDriver func(string) (driver.Driver, bool)
	Log       *slog.Logger
}

// Command returns the top-level "camera" cobra command group.
func Command() *cobra.Command {
	return CommandWithDeps(Deps{
		KC:        keychain.NewReal(),
		GetDriver: drivers.Get,
		Log:       slog.Default(),
	})
}

// CommandWithDeps constructs the "camera" cobra command group with injected dependencies.
func CommandWithDeps(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "camera",
		Short: "Camera operations on a named printer",
	}
	cmd.AddCommand(streamCommandWithDeps(deps))
	cmd.AddCommand(snapshotCommandWithDeps(deps))
	return cmd
}
