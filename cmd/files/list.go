package files

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/devicepath"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/spf13/cobra"
)

func listCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		timeout       string
		insecure      bool
		recursive     bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "list <printer> [<device-path>...]",
		Short: "List files on printer storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return runList(cmd, args[0], args[1:], flags.timeout, flags.insecure, flags.recursive, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	cmd.Flags().BoolVar(&flags.recursive, "recursive", false, "recursively list directory contents")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func runList(cmd *cobra.Command, nameArg string, pathArgs []string, timeoutFlag string, insecureFlag, recursive bool, protocolTrace string, deps Deps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return writeUsageError(cmd, "files list", fmtErr.Error())
	}

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files list", traceErr)
	}
	defer func() { _ = traceCleanup() }()

	rp, err := resolveProfile(traceCtx, cmd, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files list", err)
	}

	if !rp.driver.Capabilities().FileList {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files list",
			apperr.Newf(5, "driver %q does not support file listing", rp.pi.Driver))
	}

	ctx, cancel := context.WithTimeout(traceCtx, rp.timeout)
	defer cancel()

	var tracePath *string
	if protocolTrace != "" {
		tracePath = &protocolTrace
	}

	// If no paths given, list roots or default root.
	if len(pathArgs) == 0 {
		return listDefaultPaths(cmd, ctx, format, rp, recursive, tracePath, deps)
	}

	// Parse and validate all device paths.
	var paths []devicepath.DevicePath
	for _, raw := range pathArgs {
		dp, err := devicepath.Parse(raw)
		if err != nil {
			return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files list", err)
		}
		paths = append(paths, dp)
	}

	start := time.Now()
	var results []pathResult
	for _, dp := range paths {
		lr, err := rp.driver.FileList(ctx, rp.pi, rp.secrets, dp.Root, dp.Path, recursive, deps.Log)
		if err != nil {
			return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files list", err)
		}
		results = append(results, pathResult{devicePath: dp.String(), entries: lr.Entries})
	}
	durationMs := time.Since(start).Milliseconds()

	return writeListSuccess(cmd.OutOrStdout(), format, rp.name, rp.pi.Driver, results, durationMs, rp.driver.Capabilities(), tracePath)
}

func listDefaultPaths(cmd *cobra.Command, ctx context.Context, format output.Format, rp *resolvedProfile, recursive bool, tracePath *string, deps Deps) error {
	start := time.Now()
	roots, err := rp.driver.FileRoots(ctx, rp.pi, rp.secrets, deps.Log)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files list", err)
	}

	if len(roots) != 1 {
		// Multiple roots: show roots table.
		durationMs := time.Since(start).Milliseconds()
		return writeRootsSuccess(cmd.OutOrStdout(), format, rp.name, rp.pi.Driver, roots, durationMs, rp.driver.Capabilities(), tracePath)
	}

	// Single root: list it.
	root := roots[0]
	lr, err := rp.driver.FileList(ctx, rp.pi, rp.secrets, root.Name, "/", recursive, deps.Log)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files list", err)
	}
	durationMs := time.Since(start).Milliseconds()

	results := []pathResult{{devicePath: root.Name + ":/", entries: lr.Entries}}
	return writeListSuccess(cmd.OutOrStdout(), format, rp.name, rp.pi.Driver, results, durationMs, rp.driver.Capabilities(), tracePath)
}

type pathResult struct {
	devicePath string
	entries    []driver.FileEntry
}

func writeListSuccess(w io.Writer, format output.Format, name, driverName string, results []pathResult, durationMs int64, caps driver.Capabilities, tracePath *string) error {
	if format == output.FormatJSON {
		return writeListJSON(w, name, driverName, results, durationMs, caps, tracePath)
	}
	return writeListHuman(w, name, results)
}

func writeListJSON(w io.Writer, name, driverName string, results []pathResult, durationMs int64, caps driver.Capabilities, tracePath *string) error {
	dm := durationMs
	type pathData struct {
		DevicePath string             `json:"devicePath"`
		Entries    []driver.FileEntry `json:"entries"`
	}
	type listData struct {
		Profile      string     `json:"profile"`
		Driver       string     `json:"driver"`
		Paths        []pathData `json:"paths"`
		Warnings     []string   `json:"warnings"`
		Capabilities fileCaps   `json:"capabilities"`
	}
	pd := make([]pathData, 0, len(results))
	for _, r := range results {
		entries := r.entries
		if entries == nil {
			entries = []driver.FileEntry{}
		}
		pd = append(pd, pathData{DevicePath: r.devicePath, Entries: entries})
	}
	return output.WriteEnvelope(w, output.Envelope{
		OK: true,
		Data: listData{
			Profile:      name,
			Driver:       driverName,
			Paths:        pd,
			Warnings:     []string{},
			Capabilities: makeFileCaps(caps),
		},
		Error: nil,
		Meta:  output.Meta{Command: "files list", DurationMs: &dm, ProtocolTracePath: tracePath},
	})
}

func writeListHuman(w io.Writer, name string, results []pathResult) error {
	_, _ = fmt.Fprintf(w, "Printer: %s\n", name)
	for i, r := range results {
		if i > 0 {
			_, _ = fmt.Fprintln(w)
		}
		_, _ = fmt.Fprintf(w, "Path: %s\n\n", devicepath.SanitizeForDisplay(r.devicePath))
		if len(r.entries) == 0 {
			_, _ = fmt.Fprintln(w, "(empty)")
			continue
		}
		_, _ = fmt.Fprintf(w, "%-10s %-9s %-21s %s\n", "TYPE", "SIZE", "MODIFIED", "NAME")
		for _, e := range r.entries {
			entryType := string(e.Type)
			size := "-"
			if e.SizeBytes != nil {
				size = formatSize(*e.SizeBytes)
			}
			modified := "-"
			if e.ModifiedAt != nil {
				modified = formatModifiedTime(*e.ModifiedAt)
			}
			displayName := devicepath.SanitizeForDisplay(e.Name)
			_, _ = fmt.Fprintf(w, "%-10s %-9s %-21s %s\n", entryType, size, modified, displayName)
		}
	}
	return nil
}

func formatModifiedTime(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}
