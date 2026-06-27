package driver

import (
	"context"
	"log/slog"
)

// TemperatureDriver extends Driver with temperature control operations.
// Drivers that declare TemperatureWrite: true implement this interface.
type TemperatureDriver interface {
	Driver

	// TemperatureSet sets heater targets on the printer.
	// The driver blocks, bounded by ctx, until the printer acknowledges the
	// new target value(s) — not until the current temperature reaches target.
	TemperatureSet(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
		targets TemperatureTargets,
	) (TemperatureResult, error)
}
