package files

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/spf13/cobra"
)

func rootsCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		timeout       string
		insecure      bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "roots <printer>",
		Short: "List storage roots available on a printer",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if len(args) > 1 {
				return writeUsageError(cmd, "files roots", fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runRoots(cmd, args[0], flags.timeout, flags.insecure, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func runRoots(cmd *cobra.Command, nameArg, timeoutFlag string, insecureFlag bool, protocolTrace string, deps Deps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return writeUsageError(cmd, "files roots", fmtErr.Error())
	}

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files roots", traceErr)
	}
	defer func() { _ = traceCleanup() }()

	rp, err := resolveProfile(traceCtx, cmd, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files roots", err)
	}

	if !rp.driver.Capabilities().FileList {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files roots",
			apperr.Newf(5, "driver %q does not support file listing", rp.pi.Driver))
	}

	ctx, cancel := context.WithTimeout(traceCtx, rp.timeout)
	defer cancel()

	start := time.Now()
	roots, err := rp.driver.FileRoots(ctx, rp.pi, rp.secrets, deps.Log)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files roots", err)
	}

	var tracePath *string
	if protocolTrace != "" {
		tracePath = &protocolTrace
	}
	return writeRootsSuccess(cmd.OutOrStdout(), format, rp.name, rp.pi.Driver, roots, durationMs, rp.driver.Capabilities(), tracePath)
}

func writeRootsSuccess(w io.Writer, format output.Format, name, driverName string, roots []driver.FileRoot, durationMs int64, caps driver.Capabilities, tracePath *string) error {
	if format == output.FormatJSON {
		return writeRootsJSON(w, name, driverName, roots, durationMs, caps, tracePath)
	}
	return writeRootsHuman(w, name, roots)
}

func writeRootsJSON(w io.Writer, name, driverName string, roots []driver.FileRoot, durationMs int64, caps driver.Capabilities, tracePath *string) error {
	dm := durationMs
	type rootsData struct {
		Profile      string            `json:"profile"`
		Driver       string            `json:"driver"`
		Roots        []driver.FileRoot `json:"roots"`
		Warnings     []string          `json:"warnings"`
		Capabilities fileCaps          `json:"capabilities"`
	}
	return output.WriteEnvelope(w, output.Envelope{
		OK: true,
		Data: rootsData{
			Profile:      name,
			Driver:       driverName,
			Roots:        roots,
			Warnings:     []string{},
			Capabilities: makeFileCaps(caps),
		},
		Error: nil,
		Meta:  output.Meta{Command: "files roots", DurationMs: &dm, ProtocolTracePath: tracePath},
	})
}

func writeRootsHuman(w io.Writer, name string, roots []driver.FileRoot) error {
	_, _ = fmt.Fprintf(w, "Printer: %s\n\n", name)
	_, _ = fmt.Fprintf(w, "%-8s %-9s %-10s %-10s %s\n", "ROOT", "WRITABLE", "FREE", "CAPACITY", "DESCRIPTION")
	for _, r := range roots {
		writable := "false"
		if r.Writable {
			writable = "true"
		}
		free := "-"
		if r.FreeBytes != nil {
			free = formatSize(*r.FreeBytes)
		}
		capacity := "-"
		if r.CapacityBytes != nil {
			capacity = formatSize(*r.CapacityBytes)
		}
		_, _ = fmt.Fprintf(w, "%-8s %-9s %-10s %-10s %s\n", r.Name, writable, free, capacity, r.Description)
	}
	return nil
}

// fileCaps is the JSON representation of file capabilities.
type fileCaps struct {
	FileList     bool `json:"fileList"`
	FileDownload bool `json:"fileDownload,omitempty"`
	FileUpload   bool `json:"fileUpload,omitempty"`
}

func makeFileCaps(caps driver.Capabilities) fileCaps {
	return fileCaps{
		FileList:     caps.FileList,
		FileDownload: caps.FileDownload,
		FileUpload:   caps.FileUpload,
	}
}

func formatSize(bytes int64) string {
	const (
		kib = 1024
		mib = 1024 * 1024
		gib = 1024 * 1024 * 1024
	)
	switch {
	case bytes >= gib:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/float64(gib))
	case bytes >= mib:
		return fmt.Sprintf("%.1f MiB", float64(bytes)/float64(mib))
	case bytes >= kib:
		return fmt.Sprintf("%.1f KiB", float64(bytes)/float64(kib))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
