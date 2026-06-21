package camera

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/profile"
	"github.com/spf13/cobra"
)

const commandSnapshot = "camera snapshot"

func snapshotCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		to        string
		overwrite bool
		timeout   string
		insecure  bool
	}

	cmd := &cobra.Command{
		Use:   "snapshot <name>",
		Short: "Capture one still image from a printer camera",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if len(args) > 1 {
				return writeUsageError(cmd, commandSnapshot, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runSnapshot(cmd, args[0], flags.to, flags.overwrite, flags.timeout, flags.insecure, deps)
		},
	}
	cmd.Flags().StringVar(&flags.to, "to", "", "destination file path or directory")
	cmd.Flags().BoolVar(&flags.overwrite, "overwrite", false, "replace an existing destination file")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	return cmd
}

func runSnapshot(cmd *cobra.Command, nameArg, toFlag string, overwrite bool, timeoutFlag string, insecureFlag bool, deps Deps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return writeUsageError(cmd, commandSnapshot, fmtErr.Error())
	}

	name, err := normalizedProfileName(nameArg)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSnapshot, err)
	}
	dest, err := resolveSnapshotDestination(toFlag, name, time.Now())
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSnapshot, err)
	}
	// Checked again inside writeSnapshotFile after the capture completes, in
	// case the destination is created during the (possibly multi-second)
	// network round trip; this early check only avoids paying for that round
	// trip when the destination is already known to be invalid.
	if err := validateSnapshotDestination(dest, overwrite); err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSnapshot, err)
	}

	start := time.Now()
	result, driverName, err := captureSnapshot(cmd, name, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSnapshot, err)
	}
	if err := writeSnapshotFile(dest, result.Data, overwrite); err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandSnapshot, err)
	}

	return writeSnapshotSuccess(cmd.OutOrStdout(), format, name, driverName, dest, time.Since(start).Milliseconds(), result)
}

func normalizedProfileName(nameArg string) (string, error) {
	name := strings.ToLower(nameArg)
	if err := profile.ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}

func captureSnapshot(cmd *cobra.Command, nameArg, timeoutFlag string, insecureFlag bool, deps Deps) (*driver.CameraSnapshotResult, string, error) {
	rp, err := resolveProfile(cmd.Context(), nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return nil, "", err
	}
	if !rp.driver.Capabilities().CameraSnapshot {
		return nil, "", apperr.Newf(5, "driver %q does not support camera snapshot", rp.input.Driver)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), rp.timeout)
	defer cancel()

	result, err := rp.driver.CameraSnapshot(ctx, rp.input, rp.secrets, deps.Log)
	if err != nil {
		return nil, "", err
	}
	if result == nil {
		return nil, "", apperr.New(1, "driver returned nil camera snapshot result")
	}
	return result, rp.input.Driver, nil
}

func resolveSnapshotDestination(toFlag, name string, now time.Time) (string, error) {
	autoName := fmt.Sprintf("%s-%s.jpg", name, now.Local().Format("2006-01-02T15-04-05"))
	if toFlag == "" {
		return "." + string(os.PathSeparator) + autoName, nil
	}

	info, err := os.Stat(toFlag)
	if err == nil {
		if info.IsDir() {
			return filepath.Join(toFlag, autoName), nil
		}
		return toFlag, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return toFlag, nil
	}
	return "", apperr.Wrap(2, "cannot inspect destination path", err)
}

func writeSnapshotFile(dest string, data []byte, overwrite bool) error {
	if len(data) == 0 {
		return apperr.New(1, "camera snapshot returned empty image data")
	}
	if err := validateSnapshotDestination(dest, overwrite); err != nil {
		return err
	}

	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".polimero-snapshot-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return apperr.Wrap(2, "destination directory is not writable", err)
		}
		return apperr.Wrap(1, "cannot create snapshot file", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return apperr.Wrap(1, "cannot write snapshot file", err)
	}
	if err := tmp.Close(); err != nil {
		return apperr.Wrap(1, "cannot close snapshot file", err)
	}
	if err := commitSnapshotFile(tmpName, dest, overwrite); err != nil {
		return err
	}
	committed = true
	return nil
}

// commitSnapshotFile moves tmpName into place at dest. When overwrite is
// false, it tries Link+Remove first: hard-link creation fails atomically
// with os.ErrExist if dest already exists, which Rename alone does not
// guarantee — rename(2) silently replaces an existing dest on POSIX
// regardless of any earlier existence check, so a file created at dest
// between validateSnapshotDestination and this call would otherwise be
// silently clobbered even with --overwrite not set. Filesystems that don't
// support hard links (e.g. FAT/exFAT, some FUSE/network mounts) fall back
// to a stat-then-rename, which narrows but does not fully close that TOCTOU
// window — best effort on those filesystems only, matching the spec's
// "atomically rename it into place where the OS supports atomic rename."
func commitSnapshotFile(tmpName, dest string, overwrite bool) error {
	if overwrite {
		return renameSnapshotFile(tmpName, dest)
	}

	if err := os.Link(tmpName, dest); err == nil {
		_ = os.Remove(tmpName)
		return nil
	} else if errors.Is(err, os.ErrExist) {
		return apperr.Newf(2, "destination file %q already exists", dest)
	}

	if _, statErr := os.Stat(dest); statErr == nil {
		return apperr.Newf(2, "destination file %q already exists", dest)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return apperr.Wrap(2, "cannot inspect destination path", statErr)
	}
	return renameSnapshotFile(tmpName, dest)
}

func renameSnapshotFile(tmpName, dest string) error {
	if err := os.Rename(tmpName, dest); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return apperr.Wrap(2, "destination directory is not writable", err)
		}
		return apperr.Wrap(1, "cannot move snapshot file into place", err)
	}
	return nil
}

func validateSnapshotDestination(dest string, overwrite bool) error {
	parent := filepath.Dir(dest)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return apperr.Newf(2, "destination directory %q does not exist", parent)
		}
		return apperr.Wrap(2, "cannot inspect destination directory", err)
	}
	if !parentInfo.IsDir() {
		return apperr.Newf(2, "destination directory %q is not a directory", parent)
	}

	if info, err := os.Stat(dest); err == nil {
		if info.IsDir() {
			return apperr.Newf(2, "destination path %q is a directory", dest)
		}
		if !overwrite {
			return apperr.Newf(2, "destination file %q already exists", dest)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return apperr.Wrap(2, "cannot inspect destination path", err)
	}
	return nil
}

func writeSnapshotSuccess(w io.Writer, format output.Format, name, driverName, path string, durationMs int64, result *driver.CameraSnapshotResult) error {
	if format == output.FormatJSON {
		return writeSnapshotJSONSuccess(w, name, driverName, path, durationMs, result)
	}
	_, _ = fmt.Fprintf(w, "Snapshot saved to %s (%s).\n", path, formatByteSize(int64(len(result.Data))))
	return nil
}

func writeSnapshotJSONSuccess(w io.Writer, name, driverName, path string, durationMs int64, result *driver.CameraSnapshotResult) error {
	dm := durationMs
	type snapshotData struct {
		Profile   string `json:"profile"`
		Driver    string `json:"driver"`
		Path      string `json:"path"`
		SizeBytes int    `json:"sizeBytes"`
		Protocol  string `json:"protocol"`
	}
	return output.WriteEnvelope(w, output.Envelope{
		OK: true,
		Data: snapshotData{
			Profile:   name,
			Driver:    driverName,
			Path:      path,
			SizeBytes: len(result.Data),
			Protocol:  result.Protocol,
		},
		Error: nil,
		Meta:  output.Meta{Command: commandSnapshot, DurationMs: &dm},
	})
}

func formatByteSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	for _, suffix := range []string{"KiB", "MiB", "GiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f TiB", value/unit)
}
