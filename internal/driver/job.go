package driver

import (
	"context"
	"log/slog"
)

// JobDriver extends Driver with job control operations.
// Drivers that support any job capability implement this interface.
type JobDriver interface {
	Driver

	// JobStart starts a print job from a file already on printer storage.
	// devicePath is a validated, normalized device path (e.g. "sdcard:/models/cube.3mf").
	// The driver blocks until it confirms the resulting state is "printing".
	JobStart(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
		devicePath string,
		opts JobStartOptions,
	) (JobActionResult, error)

	// JobPause pauses the active print job.
	// The driver blocks until it confirms the resulting state is "paused".
	JobPause(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
	) (JobActionResult, error)

	// JobResume resumes a paused print job.
	// The driver blocks until it confirms the resulting state is "printing".
	JobResume(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
	) (JobActionResult, error)

	// JobCancel cancels the active or paused print job.
	// The driver blocks until it confirms the resulting state is "idle".
	JobCancel(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
	) (JobActionResult, error)
}
