package printer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/spf13/cobra"
)

// DiscoverDeps holds injectable dependencies for the printer discover command.
type DiscoverDeps struct {
	AllDrivers func() []driver.Driver
	GetDriver  func(string) (driver.Driver, bool)
}

func discoverCommand() *cobra.Command {
	return DiscoverCommandWithDeps(DiscoverDeps{
		AllDrivers: drivers.All,
		GetDriver:  drivers.Get,
	})
}

// DiscoverCommandWithDeps constructs the "discover" cobra command with injected dependencies.
func DiscoverCommandWithDeps(deps DiscoverDeps) *cobra.Command {
	var flags struct {
		driverName    string
		timeout       string
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Scan the local network for printers (mDNS, SSDP, UDP broadcast)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDiscover(cmd, flags.driverName, flags.timeout, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().StringVar(&flags.driverName, "driver", "", "restrict discovery to a specific driver")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "5s", "scan duration (default 5s)")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func runDiscover(cmd *cobra.Command, driverFlag, timeoutFlag, protocolTrace string, deps DiscoverDeps) (retErr error) {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return writeDiscoverError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, traceErr)
	}
	defer protocoltrace.Finish(traceCleanup, cmd.ErrOrStderr(), &retErr)

	timeout, err := time.ParseDuration(timeoutFlag)
	if err != nil {
		return writeDiscoverError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format,
			apperr.Newf(2, "invalid --timeout %q: %s", timeoutFlag, err))
	}
	if timeout <= 0 {
		return writeDiscoverError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format,
			apperr.New(2, "--timeout must be greater than zero"))
	}

	verboseFlag, _ := cmd.Root().PersistentFlags().GetBool("verbose")
	verbose := verboseFlag && format == output.FormatHuman

	drvs, resolveErr := resolveDiscoveryDrivers(driverFlag, deps)
	if resolveErr != nil {
		return writeDiscoverError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, resolveErr)
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return writeDiscoverError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format,
			apperr.Newf(1, "cannot resolve config directory: %s", err))
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return writeDiscoverError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format,
			apperr.Newf(2, "cannot load config: %s", err))
	}

	ctx, cancel := context.WithTimeout(traceCtx, timeout)
	defer cancel()

	output.Verbose(cmd.OutOrStdout(), verbose, fmt.Sprintf("Scanning local network for printers (timeout: %s)...", timeout))

	start := time.Now()
	type driverResult struct {
		printers []driver.DiscoveredPrinter
		err      error
	}
	results := make(chan driverResult, len(drvs))
	for _, drv := range drvs {
		go func(d driver.Driver) {
			printers, discErr := d.Discover(ctx)
			results <- driverResult{printers: printers, err: discErr}
		}(drv)
	}

	var found []discoverResult
	for range drvs {
		res := <-results
		if res.err != nil {
			return writeDiscoverError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, res.err)
		}
		for _, p := range res.printers {
			found = append(found, discoverResult{
				DiscoveredPrinter: p,
				ConfiguredAs:      findConfiguredProfile(cfg, p.Serial),
			})
		}
	}
	durationMs := time.Since(start).Milliseconds()

	return writeDiscoverSuccess(cmd.OutOrStdout(), format, found, durationMs)
}

type discoverResult struct {
	driver.DiscoveredPrinter
	ConfiguredAs string // profile name; empty if not configured
}

func resolveDiscoveryDrivers(driverFlag string, deps DiscoverDeps) ([]driver.Driver, error) {
	if driverFlag != "" {
		drv, ok := deps.GetDriver(driverFlag)
		if !ok {
			return nil, apperr.Newf(2, "unknown driver %q", driverFlag)
		}
		if !drv.Capabilities().Discovery {
			return nil, apperr.Newf(5, "driver %q does not support discovery", driverFlag)
		}
		return []driver.Driver{drv}, nil
	}
	var drvs []driver.Driver
	for _, drv := range deps.AllDrivers() {
		if drv.Capabilities().Discovery {
			drvs = append(drvs, drv)
		}
	}
	return drvs, nil
}

func findConfiguredProfile(cfg *config.Config, serial string) string {
	if serial == "" {
		return ""
	}
	for _, p := range cfg.SortedProfiles() {
		if p.Serial == serial {
			return p.Name
		}
	}
	return ""
}

func writeDiscoverSuccess(w io.Writer, format output.Format, found []discoverResult, durationMs int64) error {
	if format == output.FormatJSON {
		type printerJSON struct {
			Driver       string  `json:"driver"`
			Host         string  `json:"host"`
			Serial       string  `json:"serial"`
			Model        string  `json:"model"`
			Name         string  `json:"name"`
			ConfiguredAs *string `json:"configuredAs"`
		}
		printers := make([]printerJSON, 0, len(found))
		for _, r := range found {
			pj := printerJSON{
				Driver: r.Driver,
				Host:   r.Host,
				Serial: r.Serial,
				Model:  r.Model,
				Name:   r.Name,
			}
			if r.ConfiguredAs != "" {
				pj.ConfiguredAs = &r.ConfiguredAs
			}
			printers = append(printers, pj)
		}
		dm := durationMs
		return output.WriteEnvelope(w, output.Envelope{
			OK: true,
			Data: map[string]any{
				"printers": printers,
				"count":    len(printers),
			},
			Error: nil,
			Meta:  output.Meta{Command: "printer discover", DurationMs: &dm},
		})
	}

	if len(found) == 0 {
		_, err := fmt.Fprintf(w, "No printers found on the local network (%.1fs).\n",
			float64(durationMs)/1000)
		return err
	}

	_, err := fmt.Fprintf(w, "Discovered %d printer(s) on the local network (%.1fs):\n\n",
		len(found), float64(durationMs)/1000)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "  %-20s %-20s %-8s %-18s %s\n", "NAME", "SERIAL", "MODEL", "HOST", "CONFIGURED")
	if err != nil {
		return err
	}
	for _, r := range found {
		configured := "-"
		if r.ConfiguredAs != "" {
			configured = sanitizeHumanField(r.ConfiguredAs)
		}
		_, err = fmt.Fprintf(w, "  %-20s %-20s %-8s %-18s %s\n",
			sanitizeHumanField(r.Name),
			sanitizeHumanField(r.Serial),
			sanitizeHumanField(r.Model),
			sanitizeHumanField(r.Host),
			configured)
		if err != nil {
			return err
		}
	}
	return nil
}

func sanitizeHumanField(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, s)
}

func writeDiscoverError(out, errOut io.Writer, format output.Format, err error) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:   false,
			Data: nil,
			Error: &output.ErrDetail{
				Code:    discoverErrorCode(err),
				Message: discoverErrorMessage(err),
			},
			Meta: output.Meta{Command: "printer discover"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", discoverErrorMessage(err))
	}
	return apperr.New(code, "")
}

func discoverErrorMessage(err error) string {
	if discoverErrorCode(err) == "connection_failed" {
		if strings.Contains(err.Error(), "mDNS browse failed") {
			return "mDNS browse failed"
		}
		return "printer discovery failed"
	}
	return err.Error()
}

func discoverErrorCode(err error) string {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		return "error"
	}
	switch exitErr.Code {
	case 2:
		return "config_error"
	case 4:
		return "connection_failed"
	case 5:
		return "capability_unsupported"
	default:
		return "error"
	}
}
