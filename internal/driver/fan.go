package driver

import (
	"context"
	"log/slog"
)

// FanTarget carries the arguments for setting a fan.
type FanTarget struct {
	Fan          string // canonical driver-supported fan key
	SpeedPercent int    // 0-100, where 0 means off
}

// FanControlResult carries the acknowledged state of the fan.
type FanControlResult struct {
	Fan          string // canonical fan key acknowledged by the printer
	SpeedPercent int
	Warnings     []StatusWarning
	Capabilities Capabilities
}

// FanDriver extends Driver with fan control operations.
// Drivers that support FanControl implement this interface.
type FanDriver interface {
	Driver

	// FanSet sets fan speed on the printer.
	// The driver blocks, bounded by ctx, until the printer acknowledges the speed.
	FanSet(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
		target FanTarget,
	) (FanControlResult, error)
}
