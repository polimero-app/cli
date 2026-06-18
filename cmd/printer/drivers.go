package printer

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/output"
	"github.com/spf13/cobra"
)

func driversCommand() *cobra.Command {
	return DriversCommandWithDeps(drivers.List)
}

// DriversCommandWithDeps constructs the "drivers" cobra command with injected dependencies.
func DriversCommandWithDeps(list func() []drivers.Info) *cobra.Command {
	return &cobra.Command{
		Use:   "drivers",
		Short: "List available printer drivers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDrivers(cmd, list)
		},
	}
}

func runDrivers(cmd *cobra.Command, list func() []drivers.Info) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, err := output.ParseFormat(formatStr)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", err)
		return apperr.New(2, "")
	}

	driverInfos := sortedDriverInfos(list())
	if format == output.FormatJSON {
		return output.WriteEnvelope(cmd.OutOrStdout(), output.Envelope{
			OK:    true,
			Data:  map[string]any{"drivers": toJSONDrivers(driverInfos)},
			Error: nil,
			Meta:  output.Meta{Command: "printer drivers"},
		})
	}
	return driversWriteHuman(cmd.OutOrStdout(), driverInfos)
}

func sortedDriverInfos(infos []drivers.Info) []drivers.Info {
	out := append([]drivers.Info(nil), infos...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

type jsonDriver struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func toJSONDrivers(infos []drivers.Info) []jsonDriver {
	out := make([]jsonDriver, len(infos))
	for i, info := range infos {
		out[i] = jsonDriver{Name: info.Name, Description: info.Description}
	}
	return out
}

func driversWriteHuman(w io.Writer, infos []drivers.Info) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "DRIVER\tDESCRIPTION")
	for _, info := range infos {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", info.Name, info.Description)
	}
	return tw.Flush()
}
