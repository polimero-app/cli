package driver

import (
	"context"
	"time"
)

// Driver defines the interface every printer driver must satisfy.
// Plan 2 exposes Name and ConnectCheck only; Status and Capabilities are added in Plan 3.
type Driver interface {
	// Name returns the driver identifier string (e.g. "bambu-lan").
	Name() string

	// ConnectCheck verifies that the printer is reachable and credentials are valid.
	// Returns the SHA-256 leaf certificate fingerprint as "sha256:<lowercase-hex>".
	// Returns ("", nil) immediately when insecure is true.
	ConnectCheck(
		ctx context.Context,
		host, serial, accessCode string,
		insecure bool,
		timeout time.Duration,
	) (fingerprint string, err error)
}
