package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/devicepath"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/spf13/cobra"
)

func downloadCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		timeout       string
		insecure      bool
		to            string
		overwrite     bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "download <printer> <device-path>",
		Short: "Download a file from printer storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return writeUsageError(cmd, "files download", "profile name is required")
			}
			if len(args) < 2 {
				return writeUsageError(cmd, "files download", "device path is required")
			}
			if len(args) > 2 {
				return writeUsageError(cmd, "files download", fmt.Sprintf("expected exactly two arguments, got %d", len(args)))
			}
			return runDownload(cmd, args[0], args[1], flags.to, flags.timeout, flags.insecure, flags.overwrite, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	cmd.Flags().StringVar(&flags.to, "to", "", "destination file or directory path")
	cmd.Flags().BoolVar(&flags.overwrite, "overwrite", false, "allow overwriting existing destination file")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func runDownload(cmd *cobra.Command, nameArg, pathArg, toFlag, timeoutFlag string, insecureFlag, overwriteFlag bool, protocolTrace string, deps Deps) (retErr error) {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return writeUsageError(cmd, "files download", fmtErr.Error())
	}

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files download", traceErr)
	}
	defer protocoltrace.Finish(traceCleanup, cmd.ErrOrStderr(), &retErr)

	dp, err := devicepath.Parse(pathArg)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files download", err)
	}

	if dp.BaseName() == "" {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files download",
			apperr.New(2, "cannot download a directory; specify a file path"))
	}

	rp, err := resolveProfile(traceCtx, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files download", err)
	}

	if !rp.driver.Capabilities().FileDownload {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files download",
			apperr.Newf(5, "driver %q does not support file download", rp.pi.Driver))
	}

	// Determine local destination.
	localPath, err := resolveDownloadDest(toFlag, dp.BaseName())
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files download", err)
	}

	// Check overwrite.
	if !overwriteFlag {
		if _, statErr := os.Stat(localPath); statErr == nil {
			return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files download",
				apperr.Newf(2, "destination file already exists: %s (use --overwrite to replace)", localPath))
		}
	}

	ctx, cancel := context.WithTimeout(traceCtx, rp.timeout)
	defer cancel()

	// Write to temp file then rename for atomic write.
	dir := filepath.Dir(localPath)
	tmpFile, err := os.CreateTemp(dir, ".polimero-download-*")
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files download",
			apperr.Newf(1, "cannot create temporary file: %s", err))
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath) // clean up on failure
	}()

	start := time.Now()
	result, err := rp.driver.FileDownload(ctx, rp.pi, rp.secrets, dp.Root, dp.Path, tmpFile, deps.Log)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files download", err)
	}

	_ = tmpFile.Close()
	if err := commitDownloadFile(tmpPath, localPath, overwriteFlag); err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files download", err)
	}

	var tracePath *string
	if protocolTrace != "" {
		tracePath = &protocolTrace
	}
	return writeDownloadSuccess(cmd.OutOrStdout(), format, rp.name, rp.pi.Driver, dp.String(), localPath, result, durationMs, rp.driver.Capabilities(), tracePath)
}

// commitDownloadFile moves tmpPath into place at dest. When overwrite is
// false, it tries Link+Remove first: hard-link creation fails atomically
// with os.ErrExist if dest already exists, closing the TOCTOU window that
// a bare Rename leaves open (rename(2) silently replaces an existing dest).
// Filesystems without hard-link support fall back to stat-then-rename,
// which narrows but cannot fully close that window — best effort there.
func commitDownloadFile(tmpPath, dest string, overwrite bool) error {
	if overwrite {
		return renameDownloadFile(tmpPath, dest)
	}

	if err := os.Link(tmpPath, dest); err == nil {
		_ = os.Remove(tmpPath)
		return nil
	} else if errors.Is(err, os.ErrExist) {
		return apperr.Newf(2, "destination file already exists: %s (use --overwrite to replace)", dest)
	}

	if _, statErr := os.Stat(dest); statErr == nil {
		return apperr.Newf(2, "destination file already exists: %s (use --overwrite to replace)", dest)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return apperr.Wrap(2, "cannot inspect destination path", statErr)
	}
	return renameDownloadFile(tmpPath, dest)
}

func renameDownloadFile(tmpPath, dest string) error {
	if err := os.Rename(tmpPath, dest); err != nil {
		return apperr.Newf(1, "cannot move downloaded file to destination: %s", err)
	}
	return nil
}

func resolveDownloadDest(toFlag, baseName string) (string, error) {
	if toFlag == "" {
		return filepath.Join(".", baseName), nil
	}

	info, err := os.Stat(toFlag)
	if err == nil && info.IsDir() {
		return filepath.Join(toFlag, baseName), nil
	}

	// toFlag is a file path (may not exist yet).
	dir := filepath.Dir(toFlag)
	dirInfo, err := os.Stat(dir)
	if err != nil || !dirInfo.IsDir() {
		return "", apperr.Newf(2, "destination directory does not exist: %s", dir)
	}
	return toFlag, nil
}

func writeDownloadSuccess(w io.Writer, format output.Format, name, driverName, source, dest string, result *driver.FileTransferResult, durationMs int64, caps driver.Capabilities, tracePath *string) error {
	if format == output.FormatJSON {
		return writeDownloadJSON(w, name, driverName, source, dest, result, durationMs, caps, tracePath)
	}
	return writeDownloadHuman(w, source, dest, result)
}

func writeDownloadJSON(w io.Writer, name, driverName, source, dest string, result *driver.FileTransferResult, durationMs int64, caps driver.Capabilities, tracePath *string) error {
	dm := durationMs
	type downloadData struct {
		Profile          string   `json:"profile"`
		Driver           string   `json:"driver"`
		Source           string   `json:"source"`
		Destination      string   `json:"destination"`
		BytesTransferred *int64   `json:"bytesTransferred"`
		Warnings         []string `json:"warnings"`
		Capabilities     fileCaps `json:"capabilities"`
	}
	return output.WriteEnvelope(w, output.Envelope{
		OK: true,
		Data: downloadData{
			Profile:          name,
			Driver:           driverName,
			Source:           source,
			Destination:      dest,
			BytesTransferred: result.BytesTransferred,
			Warnings:         []string{},
			Capabilities:     fileCaps{FileDownload: caps.FileDownload},
		},
		Error: nil,
		Meta:  output.Meta{Command: "files download", DurationMs: &dm, ProtocolTracePath: tracePath},
	})
}

func writeDownloadHuman(w io.Writer, source, dest string, result *driver.FileTransferResult) error {
	sizeStr := ""
	if result.BytesTransferred != nil {
		sizeStr = fmt.Sprintf(" (%s)", formatSize(*result.BytesTransferred))
	}
	_, _ = fmt.Fprintf(w, "Downloaded %s to %s%s.\n",
		devicepath.SanitizeForDisplay(source), dest, sizeStr)
	return nil
}
