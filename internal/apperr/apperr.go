package apperr

import "fmt"

// ExitError wraps a command failure with a process exit code.
// Commands return an ExitError to signal the desired os.Exit value.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("exit %d", e.Code)
}

// New returns an ExitError with the given exit code and message.
func New(code int, msg string) *ExitError {
	return &ExitError{Code: code, Msg: msg}
}

// Newf returns an ExitError with a formatted message.
func Newf(code int, format string, args ...any) *ExitError {
	return &ExitError{Code: code, Msg: fmt.Sprintf(format, args...)}
}
