package driver

import (
	"context"
	"log/slog"
)

// MotionDriver extends Driver with motion control operations.
// Drivers that declare MotionControl: true implement this interface.
type MotionDriver interface {
	Driver

	// MotionHome homes the specified axes.
	// An empty axes slice means home all axes.
	// The driver blocks until it confirms the motion has physically finished.
	MotionHome(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
		axes []Axis,
	) (MotionResult, error)

	// MotionJog moves the toolhead relative to its current position.
	// The driver blocks until it confirms the motion has physically finished.
	MotionJog(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
		delta JogDelta,
	) (MotionResult, error)
}
