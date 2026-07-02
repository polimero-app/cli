package files

import (
	"context"
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

func uploadCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		timeout       string
		insecure      bool
		overwrite     bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "upload <printer> <local-path> <device-path>",
		Short: "Upload a file to printer storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return writeUsageError(cmd, "files upload", "profile name is required")
			}
			if len(args) < 2 {
				return writeUsageError(cmd, "files upload", "local path is required")
			}
			if len(args) < 3 {
				return writeUsageError(cmd, "files upload", "device path is required")
			}
			if len(args) > 3 {
				return writeUsageError(cmd, "files upload", fmt.Sprintf("expected exactly three arguments, got %d", len(args)))
			}
			return runUpload(cmd, args[0], args[1], args[2], flags.timeout, flags.insecure, flags.overwrite, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	cmd.Flags().BoolVar(&flags.overwrite, "overwrite", false, "allow overwriting existing device file")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func runUpload(cmd *cobra.Command, nameArg, localPathArg, devicePathArg, timeoutFlag string, insecureFlag, overwriteFlag bool, protocolTrace string, deps Deps) (retErr error) {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return writeUsageError(cmd, "files upload", fmtErr.Error())
	}

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files upload", traceErr)
	}
	defer protocoltrace.Finish(traceCleanup, cmd.ErrOrStderr(), &retErr)

	// Validate local path.
	info, err := os.Stat(localPathArg)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files upload",
			apperr.Newf(2, "local source does not exist: %s", localPathArg))
	}
	if info.IsDir() {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files upload",
			apperr.New(2, "cannot upload a directory; specify a regular file"))
	}
	if !info.Mode().IsRegular() {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files upload",
			apperr.New(2, "local source is not a regular file"))
	}

	// Parse and validate device path.
	dp, err := devicepath.Parse(devicePathArg)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files upload", err)
	}

	// If device path ends with "/" or is a root, use local base name.
	destPath := dp.Path
	if dp.IsDir() || dp.Path == "/" {
		baseName := filepath.Base(localPathArg)
		if destPath == "/" {
			destPath = "/" + baseName
		} else {
			destPath = destPath + baseName
		}
	}

	rp, err := resolveProfile(traceCtx, cmd, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files upload", err)
	}

	if !rp.driver.Capabilities().FileUpload {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files upload",
			apperr.Newf(5, "driver %q does not support file upload", rp.pi.Driver))
	}

	// Open local file.
	f, err := os.Open(localPathArg)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files upload",
			apperr.Newf(1, "cannot open local file: %s", err))
	}
	defer func() { _ = f.Close() }()

	ctx, cancel := context.WithTimeout(traceCtx, rp.timeout)
	defer cancel()

	start := time.Now()
	result, err := rp.driver.FileUpload(ctx, rp.pi, rp.secrets, dp.Root, destPath, f, info.Size(), overwriteFlag, deps.Log)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, "files upload", err)
	}

	var tracePath *string
	if protocolTrace != "" {
		tracePath = &protocolTrace
	}
	destDevicePath := dp.Root + ":" + destPath
	return writeUploadSuccess(cmd.OutOrStdout(), format, rp.name, rp.pi.Driver, localPathArg, destDevicePath, result, durationMs, rp.driver.Capabilities(), tracePath)
}

func writeUploadSuccess(w io.Writer, format output.Format, name, driverName, source, dest string, result *driver.FileTransferResult, durationMs int64, caps driver.Capabilities, tracePath *string) error {
	if format == output.FormatJSON {
		return writeUploadJSON(w, name, driverName, source, dest, result, durationMs, caps, tracePath)
	}
	return writeUploadHuman(w, source, dest, result)
}

func writeUploadJSON(w io.Writer, name, driverName, source, dest string, result *driver.FileTransferResult, durationMs int64, caps driver.Capabilities, tracePath *string) error {
	dm := durationMs
	type uploadData struct {
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
		Data: uploadData{
			Profile:          name,
			Driver:           driverName,
			Source:           source,
			Destination:      dest,
			BytesTransferred: result.BytesTransferred,
			Warnings:         []string{},
			Capabilities:     fileCaps{FileUpload: caps.FileUpload},
		},
		Error: nil,
		Meta:  output.Meta{Command: "files upload", DurationMs: &dm, ProtocolTracePath: tracePath},
	})
}

func writeUploadHuman(w io.Writer, source, dest string, result *driver.FileTransferResult) error {
	sizeStr := ""
	if result.BytesTransferred != nil {
		sizeStr = fmt.Sprintf(" (%s)", formatSize(*result.BytesTransferred))
	}
	_, _ = fmt.Fprintf(w, "Uploaded %s to %s%s.\n", source, devicepath.SanitizeForDisplay(dest), sizeStr)
	return nil
}
