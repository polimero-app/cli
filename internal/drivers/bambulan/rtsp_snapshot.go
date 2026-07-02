package bambulan

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"image/jpeg"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/pion/rtp"
	"github.com/polimero-app/cli/internal/apperr"
)

// captureRTSPSH264Snapshot connects to a Bambu RTSPS camera endpoint and
// returns one JPEG-encoded frame.
//
// The OnPacketRTP callback below only depacketizes RTP into access units and
// forwards them through auCh; it never touches frameDec. All cgo decode work
// runs on this function's own goroutine inside decodeH264SnapshotLoop, so
// frameDec is never accessed concurrently with its own teardown, regardless
// of how long the underlying RTSP client keeps delivering packets after an
// answer has already been found.
func captureRTSPSH264Snapshot(ctx context.Context, tlsCfg *tls.Config, host, accessCode string) ([]byte, error) {
	timeout := rtspTimeout(ctx)
	c, medi, forma, rtpDec, err := connectRTSPSH264(tlsCfg, host, accessCode, cameraPortH264, timeout)
	if err != nil {
		return nil, apperr.Wrap(4, "RTSPS camera endpoint unreachable", err)
	}
	defer c.Close()

	frameDec, err := newH264FrameDecoder()
	if err != nil {
		return nil, apperr.Wrap(1, "H.264 frame decoder setup failed", err)
	}
	defer frameDec.close()

	auCh := make(chan [][]byte, 64)
	errCh := make(chan error, 1)

	c.OnPacketRTP(medi, forma, func(pkt *rtp.Packet) {
		au, decErr := rtpDec.Decode(pkt)
		if decErr != nil {
			if !errors.Is(decErr, rtph264.ErrNonStartingPacketAndNoPrevious) &&
				!errors.Is(decErr, rtph264.ErrMorePacketsNeeded) {
				select {
				case errCh <- apperr.Wrap(1, "H.264 RTP decode failed", decErr):
				default:
				}
			}
			return
		}
		select {
		case auCh <- cloneAU(au):
		default:
			// Consumer is still busy with an earlier access unit; drop this
			// one rather than block the RTP read loop.
		}
	})

	if _, err := c.Play(nil); err != nil {
		return nil, apperr.Wrap(4, "RTSPS camera play failed", err)
	}

	go func() {
		waitErr := c.Wait()
		if waitErr == nil {
			waitErr = errors.New("stream ended")
		}
		select {
		case errCh <- apperr.Wrap(4, "RTSPS camera stream ended before snapshot", waitErr):
		default:
		}
	}()

	params := newH264ParameterSets(forma.SPS, forma.PPS)
	return decodeH264SnapshotLoop(ctx, frameDec, &params, auCh, errCh)
}

// decodeH264SnapshotLoop consumes access units from auCh, starting decode at
// the first one that carries a keyframe plus both parameter sets, and
// continuing to feed later access units until frameDec produces an image.
// It runs entirely on the calling goroutine: frameDec is therefore never
// touched by any other goroutine, including the RTP callback that feeds
// auCh/errCh.
func decodeH264SnapshotLoop(
	ctx context.Context,
	frameDec *h264FrameDecoder,
	params *h264ParameterSets,
	auCh <-chan [][]byte,
	errCh <-chan error,
) ([]byte, error) {
	decoderStarted := false
	for {
		select {
		case au := <-auCh:
			decodeAU := au
			if !decoderStarted {
				preparedAU, ok := prepareH264SnapshotStartAU(au, params)
				if !ok {
					continue
				}
				decodeAU = preparedAU
				decoderStarted = true
			} else {
				params.updateFromAU(au)
			}

			img, decErr := frameDec.decode(decodeAU)
			if decErr != nil {
				return nil, apperr.Wrap(1, "H.264 frame decode failed", decErr)
			}
			if img == nil {
				continue
			}

			var buf bytes.Buffer
			if encErr := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); encErr != nil {
				return nil, apperr.Wrap(1, "JPEG encoding failed", encErr)
			}
			return buf.Bytes(), nil
		case err := <-errCh:
			return nil, err
		case <-ctx.Done():
			return nil, cameraContextError(ctx.Err())
		}
	}
}

// cloneAU deep-copies each NALU in au. gortsplib may reuse the buffer backing
// an access unit's bytes for a later RTP packet, so anything crossing into
// auCh — read by a different goroutine, at an unknown later time — must be
// copied first.
func cloneAU(au [][]byte) [][]byte {
	cloned := make([][]byte, len(au))
	for i, nalu := range au {
		cloned[i] = cloneH264NALU(nalu)
	}
	return cloned
}

type h264ParameterSets struct {
	sps []byte
	pps []byte
}

func newH264ParameterSets(sps, pps []byte) h264ParameterSets {
	return h264ParameterSets{
		sps: cloneH264NALU(sps),
		pps: cloneH264NALU(pps),
	}
}

func cloneH264NALU(nalu []byte) []byte {
	if len(nalu) == 0 {
		return nil
	}
	return append([]byte(nil), nalu...)
}

func (p *h264ParameterSets) updateFromAU(au [][]byte) bool {
	hasIDR := false
	for _, nalu := range au {
		if len(nalu) == 0 {
			continue
		}
		switch h264.NALUType(nalu[0] & 0x1F) {
		case h264.NALUTypeSPS:
			p.sps = cloneH264NALU(nalu)
		case h264.NALUTypePPS:
			p.pps = cloneH264NALU(nalu)
		case h264.NALUTypeIDR:
			hasIDR = true
		}
	}
	return hasIDR
}

func prepareH264SnapshotStartAU(au [][]byte, params *h264ParameterSets) ([][]byte, bool) {
	if !params.updateFromAU(au) {
		return nil, false
	}
	if len(params.sps) == 0 || len(params.pps) == 0 {
		return nil, false
	}

	prepared := make([][]byte, 0, len(au)+2)
	prepared = append(prepared, params.sps, params.pps)
	for _, nalu := range au {
		if len(nalu) == 0 {
			continue
		}
		switch h264.NALUType(nalu[0] & 0x1F) {
		case h264.NALUTypeSPS, h264.NALUTypePPS:
			continue
		default:
			prepared = append(prepared, nalu)
		}
	}
	return prepared, true
}

func rtspTimeout(ctx context.Context) time.Duration {
	const fallback = 10 * time.Second
	deadline, ok := ctx.Deadline()
	if !ok {
		return fallback
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return time.Nanosecond
	}
	if remaining < fallback {
		return remaining
	}
	return fallback
}
