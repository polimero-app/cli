package protocoltrace

import "time"

// Event is a single protocol trace diagnostic record.
// Each event is written as one JSON Lines entry to the trace file.
type Event struct {
	// Timestamp is the UTC time when this event was emitted.
	Timestamp time.Time `json:"timestamp"`

	// Command is the CLI command that initiated this trace (e.g. "status").
	Command string `json:"command,omitempty"`

	// Driver is the driver name handling this operation (e.g. "bambu-lan").
	Driver string `json:"driver,omitempty"`

	// Operation is the high-level driver operation (e.g. "Status", "FileList").
	Operation string `json:"operation,omitempty"`

	// Phase is the protocol phase within an operation (e.g. "connect", "subscribe", "parse").
	Phase string `json:"phase,omitempty"`

	// Transport is the transport protocol name (e.g. "mqtt", "ftps", "rtsp").
	Transport string `json:"transport,omitempty"`

	// Endpoint is a sanitized endpoint identifier (host:port, no secrets).
	Endpoint string `json:"endpoint,omitempty"`

	// ByteCount is the number of bytes sent or received in this phase.
	ByteCount *int64 `json:"byteCount,omitempty"`

	// DurationMs is the elapsed time in milliseconds for this phase.
	DurationMs *int64 `json:"durationMs,omitempty"`

	// Protocol is a selected sub-protocol name (e.g. "mqttv3.1.1", "ftps-explicit").
	Protocol string `json:"protocol,omitempty"`

	// Capability records a capability decision (e.g. "cameraStream: true").
	Capability string `json:"capability,omitempty"`

	// Keys is a response key inventory for parser debugging.
	Keys []string `json:"keys,omitempty"`

	// Warning describes a parser or protocol warning (type mismatch, fallback, etc.).
	Warning string `json:"warning,omitempty"`

	// ErrorCategory is a sanitized error classification (never raw error text).
	ErrorCategory string `json:"errorCategory,omitempty"`

	// Detail holds additional safe scalar metadata.
	Detail map[string]any `json:"detail,omitempty"`
}
