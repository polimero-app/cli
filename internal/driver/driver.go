package driver

import (
	"context"
	"log/slog"
	"time"
)

// Driver defines the interface every printer driver must satisfy.
type Driver interface {
	// Name returns the driver identifier string (e.g. "bambu-lan").
	Name() string

	// Capabilities returns which optional operations this driver supports.
	Capabilities() Capabilities

	// ConnectCheck verifies that the printer is reachable and credentials are valid.
	// Returns the SHA-256 leaf certificate fingerprint as "sha256:<lowercase-hex>".
	// Returns ("", nil) immediately when insecure is true.
	ConnectCheck(
		ctx context.Context,
		host, serial, accessCode string,
		insecure bool,
		timeout time.Duration,
	) (fingerprint string, err error)

	// Status fetches the current printer state over the driver protocol.
	Status(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
	) (*StatusResult, error)

	CaptureFingerprint(ctx context.Context, host, serial string) (fingerprint string, err error)

	// Discover scans the local network for printers using driver-specific discovery
	// protocols (e.g. mDNS). ctx controls the scan duration. Returns a non-nil
	// empty slice when no printers are found. Returns exit code 4 if discovery
	// cannot be started.
	Discover(ctx context.Context) ([]DiscoveredPrinter, error)
}
