package bambulan

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"image/jpeg"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/pion/rtp"
	"github.com/polimero-app/cli/internal/apperr"
)

type h264CaptureResult struct {
	data []byte
	err  error
}

func captureRTSPSH264Snapshot(ctx context.Context, tlsCfg *tls.Config, host, accessCode string) ([]byte, error) {
	rtspURL := fmt.Sprintf("rtsps://%s:%s@%s:%d/streaming/live/1",
		cameraUsername, accessCode, host, cameraPortH264)

	u, err := base.ParseURL(rtspURL)
	if err != nil {
		return nil, apperr.Wrap(4, "invalid RTSPS camera URL", err)
	}

	timeout := rtspTimeout(ctx)
	proto := gortsplib.ProtocolTCP
	c := &gortsplib.Client{
		Scheme:       u.Scheme,
		Host:         u.Host,
		TLSConfig:    tlsCfg,
		Protocol:     &proto,
		UserAgent:    "polimero/1.0",
		ReadTimeout:  timeout,
		WriteTimeout: timeout,
	}

	if err := c.Start(); err != nil {
		return nil, apperr.Wrap(4, "RTSPS camera endpoint unreachable", err)
	}
	defer c.Close()

	desc, _, err := c.Describe(u)
	if err != nil {
		return nil, apperr.Wrap(4, "RTSPS camera describe failed", err)
	}

	var forma *format.H264
	medi := desc.FindFormat(&forma)
	if medi == nil {
		return nil, apperr.New(4, "RTSPS camera endpoint did not expose H.264 video")
	}

	rtpDec, err := forma.CreateDecoder()
	if err != nil {
		return nil, apperr.Wrap(1, "H.264 RTP decoder setup failed", err)
	}

	frameDec, err := newH264FrameDecoder()
	if err != nil {
		return nil, apperr.Wrap(1, "H.264 frame decoder setup failed", err)
	}
	defer frameDec.close()

	params := newH264ParameterSets(forma.SPS, forma.PPS)

	if _, err := c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
		return nil, apperr.Wrap(4, "RTSPS camera setup failed", err)
	}

	resultCh := make(chan h264CaptureResult, 1)
	sendResult := func(result h264CaptureResult) {
		select {
		case resultCh <- result:
		default:
		}
	}

	decoderStarted := false
	c.OnPacketRTP(medi, forma, func(pkt *rtp.Packet) {
		if ctx.Err() != nil {
			return
		}

		au, decErr := rtpDec.Decode(pkt)
		if decErr != nil {
			if !errors.Is(decErr, rtph264.ErrNonStartingPacketAndNoPrevious) &&
				!errors.Is(decErr, rtph264.ErrMorePacketsNeeded) {
				sendResult(h264CaptureResult{err: apperr.Wrap(1, "H.264 RTP decode failed", decErr)})
			}
			return
		}

		decodeAU := au
		if !decoderStarted {
			preparedAU, ok := prepareH264SnapshotStartAU(au, &params)
			if !ok {
				return
			}
			decodeAU = preparedAU
			decoderStarted = true
		} else {
			params.updateFromAU(au)
		}

		img, decErr := frameDec.decode(decodeAU)
		if decErr != nil {
			sendResult(h264CaptureResult{err: apperr.Wrap(1, "H.264 frame decode failed", decErr)})
			return
		}
		if img == nil {
			return
		}

		var buf bytes.Buffer
		if encErr := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); encErr != nil {
			sendResult(h264CaptureResult{err: apperr.Wrap(1, "JPEG encoding failed", encErr)})
			return
		}

		sendResult(h264CaptureResult{data: append([]byte(nil), buf.Bytes()...)})
	})

	if _, err := c.Play(nil); err != nil {
		return nil, apperr.Wrap(4, "RTSPS camera play failed", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- c.Wait()
	}()

	select {
	case result := <-resultCh:
		if result.err != nil {
			return nil, result.err
		}
		return result.data, nil
	case err := <-waitCh:
		if err != nil {
			return nil, apperr.Wrap(4, "RTSPS camera stream ended before snapshot", err)
		}
		return nil, apperr.New(4, "RTSPS camera stream ended before snapshot")
	case <-ctx.Done():
		c.Close()
		return nil, cameraContextError(ctx.Err())
	}
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
