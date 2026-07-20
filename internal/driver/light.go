package driver

import (
	"context"
	"log/slog"
)

type LightState string

const (
	LightStateOn  LightState = "on"
	LightStateOff LightState = "off"
)

// LightTarget carries the arguments for setting a light.
type LightTarget struct {
	Light string     // canonical driver-supported light key (e.g., "chamber")
	State LightState // LightStateOn or LightStateOff
}

// LightControlResult carries the acknowledged state of the light.
type LightControlResult struct {
	Light        string
	State        LightState
	Warnings     []StatusWarning
	Capabilities Capabilities
}

// LightDriver extends Driver with light control operations.
// Drivers that support LightControl implement this interface.
type LightDriver interface {
	Driver

	// LightSet sets light state on the printer.
	// The driver blocks, bounded by ctx, until the printer acknowledges the state.
	LightSet(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
		target LightTarget,
	) (LightControlResult, error)
}
