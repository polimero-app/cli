package lights

import (
	"log/slog"

	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

// Deps holds injectable dependencies for the lights commands.
type Deps struct {
	KC        keychain.Keychain
	GetDriver func(string) (driver.Driver, bool)
	Log       *slog.Logger
	Prompter  tty.Prompter
}

// Command returns the top-level "lights" cobra command group.
func Command() *cobra.Command {
	return CommandWithDeps(Deps{
		KC:        keychain.NewReal(),
		GetDriver: drivers.Get,
		Log:       slog.Default(),
		Prompter:  tty.NewReal(),
	})
}

// CommandWithDeps constructs the "lights" cobra command group with injected dependencies.
func CommandWithDeps(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lights",
		Short: "Light control operations on a named printer",
	}
	cmd.AddCommand(setCommandWithDeps(deps))
	return cmd
}
