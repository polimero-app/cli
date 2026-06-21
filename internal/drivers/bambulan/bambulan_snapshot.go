package bambulan

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

const (
	cameraProtocolH264  = "h264"
	cameraProtocolMJPEG = "mjpeg"
)

// CameraSnapshot captures one JPEG-encoded camera frame from the printer.
func (d *Driver) CameraSnapshot(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger) (*driver.CameraSnapshotResult, error) {
	tlsCfg, err := buildTLSConfig(p.Serial, s.TLSFingerprint, p.Insecure)
	if err != nil {
		return nil, err
	}

	captureH264 := d.captureH264Snapshot
	if captureH264 == nil {
		captureH264 = captureRTSPSH264Snapshot
	}

	data, h264Err := captureH264(ctx, tlsCfg.Clone(), p.Host, s.AccessCode)
	if h264Err == nil {
		return &driver.CameraSnapshotResult{
			Data:         data,
			Protocol:     cameraProtocolH264,
			Capabilities: d.Capabilities(),
		}, nil
	}
	if !isConnectionError(h264Err) {
		return nil, h264Err
	}

	data, mjpegErr := d.captureMJPEGSnapshot(ctx, p, s, tlsCfg.Clone())
	if mjpegErr == nil {
		return &driver.CameraSnapshotResult{
			Data:         data,
			Protocol:     cameraProtocolMJPEG,
			Capabilities: d.Capabilities(),
		}, nil
	}
	if ctx.Err() != nil {
		return nil, cameraContextError(ctx.Err())
	}
	return nil, apperr.New(4, "camera endpoint unreachable: both ports 322 and 6000 failed")
}

func isConnectionError(err error) bool {
	var exitErr *apperr.ExitError
	return errors.As(err, &exitErr) && exitErr.Code == 4
}

func (d *Driver) captureMJPEGSnapshot(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, tlsCfg *tls.Config) ([]byte, error) {
	conn, err := d.dialTLS(ctx, fmt.Sprintf("%s:%d", p.Host, cameraPortMJPEG), tlsCfg)
	if err != nil {
		return nil, apperr.Wrap(4, "MJPEG camera endpoint unreachable", err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if authErr := sendCameraAuth(conn, s.AccessCode); authErr != nil {
		return nil, apperr.Wrap(4, "camera authentication failed", authErr)
	}

	frame, err := readMJPEGFrame(conn)
	if err != nil {
		if ctx.Err() != nil || isNetTimeout(err) {
			return nil, cameraContextError(ctx.Err())
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, apperr.New(4, "camera frame capture timed out")
		}
		return nil, apperr.Wrap(1, "invalid MJPEG camera frame", err)
	}
	return frame, nil
}

func cameraContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return apperr.New(4, "camera frame capture cancelled")
	}
	return apperr.New(4, "camera frame capture timed out")
}

// isNetTimeout reports whether err is a network deadline-exceeded error.
// conn's read deadline and ctx's deadline are set to the same instant but
// fire via independent timers, so a deadline-exceeded read can observably
// occur before ctx.Err() has flipped non-nil; checking the error itself
// avoids misclassifying that as a generic decode failure.
func isNetTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
