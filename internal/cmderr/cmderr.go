// Package cmderr centralizes the error-to-output mapping shared by all
// user-facing commands: stable JSON error codes, sanitized messages,
// envelope/stderr emission, and usage-error handling.
package cmderr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/output"
	"github.com/spf13/cobra"
)

// ExitCode returns the process exit code carried by err, defaulting to 1.
func ExitCode(err error) int {
	var exitErr *apperr.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return 1
}

// IsTimeout reports whether err is an exit-code-4 error caused by a
// timeout or deadline expiry.
func IsTimeout(err error) bool {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timed out") || strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded")
}

// Code maps err to the stable JSON error code for its exit code. When
// classifyTimeout is true, exit-code-4 timeouts map to "timeout" instead
// of "connection_failed".
func Code(err error, classifyTimeout bool) string {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		return "error"
	}
	switch exitErr.Code {
	case 2:
		return "config_error"
	case 3:
		msg := err.Error()
		if strings.Contains(msg, "MQTT authentication") ||
			strings.Contains(msg, "Moonraker authentication") ||
			strings.Contains(msg, "TLS fingerprint mismatch") {
			return "authentication_failed"
		}
		return "secret_not_found"
	case 4:
		if classifyTimeout && IsTimeout(err) {
			return "timeout"
		}
		return "connection_failed"
	case 5:
		return "capability_unsupported"
	default:
		return "error"
	}
}

// CommandMessage sanitizes a driver or resolution error into the generic
// wording shared by control commands (temperature, motion, jobs).
func CommandMessage(err error) string {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch Code(err, true) {
	case "authentication_failed":
		switch {
		case strings.Contains(msg, "MQTT authentication rejected"):
			return "MQTT authentication rejected"
		case strings.Contains(msg, "Moonraker authentication rejected"):
			return "Moonraker authentication rejected"
		case strings.Contains(msg, "TLS fingerprint mismatch"):
			return "TLS fingerprint mismatch"
		default:
			return "authentication or secret error"
		}
	case "secret_not_found":
		return "secret not found"
	case "connection_failed":
		switch {
		case strings.Contains(lower, "cancelled") || strings.Contains(lower, "canceled"):
			return "request cancelled"
		case strings.Contains(msg, "subscription failed"):
			return "command subscription failed"
		case strings.Contains(msg, "publish failed"):
			return "command publish failed"
		case strings.Contains(msg, "connection failed"):
			return "connection failed"
		default:
			return "command failed"
		}
	case "timeout":
		return "command timed out"
	default:
		return msg
	}
}

// Write emits detail in the requested format (JSON envelope on out, or a
// human-readable line on errOut) and returns an empty-message ExitError
// carrying err's exit code.
func Write(out, errOut io.Writer, format output.Format, cmdName string, detail output.ErrDetail, err error) error {
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &detail,
			Meta:  output.Meta{Command: cmdName},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", detail.Message)
	}
	return apperr.New(ExitCode(err), "")
}

// WriteDetail emits a fully caller-specified error (JSON code, message,
// details map) and returns an ExitError with exitCode and msg.
func WriteDetail(out, errOut io.Writer, format output.Format, cmdName string, exitCode int, jsonCode, msg string, details map[string]any) error {
	detail := output.ErrDetail{Code: jsonCode, Message: msg, Details: details}
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &detail,
			Meta:  output.Meta{Command: cmdName},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", msg)
	}
	return apperr.New(exitCode, msg)
}

// WriteUsage resolves the root --output flag and emits a code-2 usage
// error with the "config_error" JSON code. An invalid --output value is
// reported on stderr with exit code 2.
func WriteUsage(cmd *cobra.Command, cmdName, message string) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", fmtErr)
		return apperr.New(2, "")
	}
	usageErr := apperr.New(2, message)
	detail := output.ErrDetail{Code: "config_error", Message: message}
	return Write(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, cmdName, detail, usageErr)
}
