package driver

import (
	"context"
	"log/slog"
)

// Driver defines the interface every printer driver must satisfy.
type Driver interface {
	// Name returns the driver identifier string (e.g. "bambu-lan").
	Name() string

	// Capabilities returns which optional operations this driver supports.
	Capabilities() Capabilities

	// ValidateProfile checks driver-specific profile fields (e.g. serial number format).
	// Returns nil if the profile is valid for this driver.
	ValidateProfile(p ProfileInput) error

	// ConnectCheck verifies that the printer is reachable and credentials are valid.
	// Returns the SHA-256 leaf certificate fingerprint as "sha256:<lowercase-hex>".
	// Returns ("", nil) immediately when p.Insecure is true.
	ConnectCheck(ctx context.Context, p ProfileInput, s SecretsBundle) (fingerprint string, err error)

	// Status fetches the current printer state over the driver protocol.
	Status(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
	) (*StatusResult, error)

	// CaptureFingerprint performs a TLS handshake to the printer and returns
	// the SHA-256 leaf certificate fingerprint as "sha256:<lowercase-hex>".
	CaptureFingerprint(ctx context.Context, p ProfileInput) (fingerprint string, err error)

	// Discover scans the local network for printers using driver-specific discovery
	// protocols (e.g. mDNS). ctx controls the scan duration. Returns a non-nil
	// empty slice when no printers are found. Returns exit code 4 if discovery
	// cannot be started.
	Discover(ctx context.Context) ([]DiscoveredPrinter, error)
}
