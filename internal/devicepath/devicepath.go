package devicepath

import (
	"fmt"
	"strings"

	"github.com/polimero-app/cli/internal/apperr"
)

const maxDevicePathBytes = 1024

// DevicePath represents a validated, normalized device path.
type DevicePath struct {
	Root string // e.g. "sdcard"
	Path string // e.g. "/models/cube.3mf" (always starts with /)
}

// String returns the canonical device path representation.
func (dp DevicePath) String() string {
	return dp.Root + ":" + dp.Path
}

// BaseName returns the final path component.
func (dp DevicePath) BaseName() string {
	if dp.Path == "/" {
		return ""
	}
	idx := strings.LastIndex(dp.Path, "/")
	if idx < 0 {
		return dp.Path
	}
	return dp.Path[idx+1:]
}

// IsDir returns true if the path ends with a trailing slash.
// Note: after normalization only "/" itself retains a trailing slash.
func (dp DevicePath) IsDir() bool {
	return dp.Path == "/" || strings.HasSuffix(dp.Path, "/")
}

// Parse validates and normalizes a device path string.
// Format: <root>:/<path>
func Parse(raw string) (DevicePath, error) {
	if raw == "" {
		return DevicePath{}, apperr.New(2, "device path is required")
	}

	// Check for control characters and NUL bytes.
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x20 || raw[i] == 0x7f {
			return DevicePath{}, apperr.New(2, "device path contains invalid control characters")
		}
	}

	// Split root from path at first ":"
	colonIdx := strings.Index(raw, ":")
	if colonIdx < 0 {
		return DevicePath{}, apperr.Newf(2, "invalid device path %q: missing root separator ':'", raw)
	}

	root := raw[:colonIdx]
	rest := raw[colonIdx+1:]

	if err := validateRoot(root); err != nil {
		return DevicePath{}, err
	}

	if !strings.HasPrefix(rest, "/") {
		return DevicePath{}, apperr.Newf(2, "invalid device path %q: path must start with '/'", raw)
	}

	normalized, err := normalizePath(rest)
	if err != nil {
		return DevicePath{}, err
	}

	dp := DevicePath{Root: root, Path: normalized}

	if len(dp.String()) > maxDevicePathBytes {
		return DevicePath{}, apperr.Newf(2, "device path exceeds maximum length of %d bytes", maxDevicePathBytes)
	}

	return dp, nil
}

// validateRoot checks that the root name uses only allowed characters.
func validateRoot(root string) error {
	if root == "" {
		return apperr.New(2, "device path root name is empty")
	}

	// Must start with ASCII letter or digit.
	first := root[0]
	if !isASCIILetterOrDigit(first) {
		return apperr.Newf(2, "invalid root name %q: must start with an ASCII letter or digit", root)
	}

	for i := 1; i < len(root); i++ {
		c := root[i]
		if !isASCIILetterOrDigit(c) && c != '.' && c != '_' && c != '-' {
			return apperr.Newf(2, "invalid root name %q: contains invalid character %q", root, string(c))
		}
	}
	return nil
}

// normalizePath normalizes a path by resolving empty and "." segments,
// and rejecting ".." segments.
func normalizePath(path string) (string, error) {
	if path == "/" {
		return "/", nil
	}

	// Track trailing slash for upload directory detection.
	trailingSlash := strings.HasSuffix(path, "/")

	segments := strings.Split(path, "/")
	var normalized []string

	for _, seg := range segments {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			return "", apperr.New(2, "device path contains '..' traversal component")
		}
		normalized = append(normalized, seg)
	}

	if len(normalized) == 0 {
		return "/", nil
	}

	result := "/" + strings.Join(normalized, "/")
	if trailingSlash {
		result += "/"
	}
	return result, nil
}

func isASCIILetterOrDigit(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// ValidateRootName checks that a root name string is valid according to the
// device path contract. It is used by drivers to validate root parameters.
func ValidateRootName(root string) error {
	return validateRoot(root)
}

// SanitizeForDisplay replaces control characters in a device path string
// with the Unicode replacement character for safe terminal output.
func SanitizeForDisplay(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			b.WriteRune('\ufffd')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FormatSize formats a byte count as a human-readable size string.
func FormatSize(bytes int64) string {
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
