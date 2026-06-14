package apperr

import "fmt"

// ExitError wraps a command failure with a process exit code.
// Commands return an ExitError to signal the desired os.Exit value.
type ExitError struct {
	Code  int
	Msg   string
	cause error // preserved for errors.Is / errors.As traversal
}

func (e *ExitError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("exit %d", e.Code)
}

// Unwrap returns the underlying cause, enabling errors.Is / errors.As traversal.
func (e *ExitError) Unwrap() error { return e.cause }

// New returns an ExitError with the given exit code and message.
func New(code int, msg string) *ExitError {
	return &ExitError{Code: code, Msg: msg}
}

// Newf returns an ExitError with a formatted message.
func Newf(code int, format string, args ...any) *ExitError {
	return &ExitError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Wrap returns an ExitError that preserves cause in the error chain.
// Use instead of Newf when errors.Is / errors.As on the original error matters.
func Wrap(code int, msg string, cause error) *ExitError {
	return &ExitError{Code: code, Msg: msg, cause: cause}
}
