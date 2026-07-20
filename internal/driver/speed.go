package driver

import (
	"context"
	"log/slog"
)

// SpeedControlResult carries the acknowledged state of the speed profile.
type SpeedControlResult struct {
	SpeedProfile string
	Warnings     []StatusWarning
	Capabilities Capabilities
}

// SpeedDriver extends Driver with speed control operations.
// Drivers that support SpeedControl implement this interface.
type SpeedDriver interface {
	Driver

	// SpeedSet sets the active print speed profile.
	// The driver blocks, bounded by ctx, until the printer acknowledges the profile.
	SpeedSet(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
		profile string,
	) (SpeedControlResult, error)
}
