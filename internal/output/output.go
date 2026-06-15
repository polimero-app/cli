package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// Format controls how command results are rendered.
type Format string

const (
	FormatHuman Format = "human"
	FormatJSON  Format = "json"
)

// ParseFormat validates s and returns the corresponding Format.
// Returns an error for any value other than "human" or "json".
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case FormatHuman, FormatJSON:
		return Format(s), nil
	}
	return "", fmt.Errorf("invalid output format %q: must be human or json", s)
}

// Envelope is the stable JSON response structure shared by all commands.
// Every JSON response — success or error — uses this shape.
type Envelope struct {
	OK    bool       `json:"ok"`
	Data  any        `json:"data"`
	Error *ErrDetail `json:"error"`
	Meta  Meta       `json:"meta"`
}

// ErrDetail describes a failure in the JSON envelope.
type ErrDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Meta carries metadata about the command invocation.
// DurationMs is only set for commands that make network calls.
type Meta struct {
	Command    string `json:"command"`
	DurationMs *int64 `json:"durationMs,omitempty"`
}

// WriteEnvelope encodes env as indented JSON followed by a newline to w.
func WriteEnvelope(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}

// Verbose writes msg followed by a newline to w if verbose is true.
// If verbose is false, it does nothing.
func Verbose(w io.Writer, verbose bool, msg string) {
	if !verbose {
		return
	}
	_, _ = fmt.Fprintln(w, msg)
}
