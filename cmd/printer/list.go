package printer

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/output"
	"github.com/spf13/cobra"
)

func listCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured printer profiles",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}
}

func runList(cmd *cobra.Command, _ []string) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, err := output.ParseFormat(formatStr)
	if err != nil {
		return apperr.New(2, err.Error())
	}

	cfg, loadErr := config.Load()
	if loadErr != nil {
		return listWriteError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, loadErr)
	}

	profiles := cfg.SortedProfiles()

	if format == output.FormatJSON {
		return output.WriteEnvelope(cmd.OutOrStdout(), output.Envelope{
			OK:    true,
			Data:  map[string]any{"profiles": toJSONProfiles(profiles)},
			Error: nil,
			Meta:  output.Meta{Command: "printer list"},
		})
	}

	return listWriteHuman(cmd.OutOrStdout(), profiles)
}

// jsonProfile is the per-profile shape in the JSON response.
type jsonProfile struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Host     string `json:"host"`
	Serial   string `json:"serial,omitempty"`
	Timeout  string `json:"timeout"`
	Insecure bool   `json:"insecure"`
}

func toJSONProfiles(profiles []config.Profile) []jsonProfile {
	out := make([]jsonProfile, len(profiles))
	for i, p := range profiles {
		out[i] = jsonProfile{
			Name:     p.Name,
			Driver:   p.Driver,
			Host:     p.Host,
			Serial:   p.Serial,
			Timeout:  p.Timeout,
			Insecure: p.Insecure,
		}
	}
	return out
}

func listWriteHuman(w io.Writer, profiles []config.Profile) error {
	if len(profiles) == 0 {
		_, err := fmt.Fprintln(w, "No printer profiles configured.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDRIVER\tHOST\tSERIAL\tTIMEOUT\tINSECURE")
	for _, p := range profiles {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%v\n", p.Name, p.Driver, p.Host, p.Serial, p.Timeout, p.Insecure)
	}
	return tw.Flush()
}

func listWriteError(out, errOut io.Writer, format output.Format, err error) error {
	code := listExitCode(err)
	msg := sanitizeConfigErr(err)
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &output.ErrDetail{Code: "config_error", Message: msg},
			Meta:  output.Meta{Command: "printer list"},
		})
	} else {
		fmt.Fprintf(errOut, "Error: %s\n", msg)
	}
	return apperr.New(code, "")
}

func listExitCode(err error) int {
	if errors.Is(err, config.ErrUnsupportedVersion) || errors.Is(err, config.ErrMalformed) {
		return 2
	}
	return 1
}

func sanitizeConfigErr(err error) string {
	switch {
	case errors.Is(err, config.ErrUnsupportedVersion):
		return "unsupported config schema version"
	case errors.Is(err, config.ErrMalformed):
		return "config file is malformed"
	default:
		return "failed to read config"
	}
}
