package bambulan

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
	"github.com/pion/rtp"
)

// rtspStream implements io.ReadCloser, emitting an MPEG-TS byte stream
// containing H.264 video from an RTSPS source via gortsplib.
//
// Architecture: RTP callback → MPEG-TS mux → channel → Read().
// The channel decouples the callback (must never block) from the reader
// (may block waiting for an HTTP client). No intermediate pipe or pump.
type rtspStream struct {
	client *gortsplib.Client
	dataCh chan []byte
	buf    []byte // leftover bytes from a partial Read
	err    error  // sticky error from closeWithError
	once   sync.Once
}

// connectRTSPSH264 dials a Bambu RTSPS camera endpoint, performs DESCRIBE,
// finds its H.264 track, creates that track's RTP decoder, and performs
// SETUP — in that order, matching what every caller of this function did
// before it existed. On success, the caller must close the returned client
// (directly or via defer) once done, and must register OnPacketRTP before
// calling client.Play(nil). On error, client is nil and any partially-started
// connection has already been closed.
func connectRTSPSH264(tlsCfg *tls.Config, host, accessCode string, timeout time.Duration) (
	client *gortsplib.Client,
	medi *description.Media,
	forma *format.H264,
	rtpDec *rtph264.Decoder,
	err error,
) {
	rtspURL := fmt.Sprintf("rtsps://%s:%s@%s:%d/streaming/live/1",
		cameraUsername, accessCode, host, cameraPortH264)

	u, err := base.ParseURL(rtspURL)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("invalid RTSP URL: %w", err)
	}

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
		return nil, nil, nil, nil, fmt.Errorf("RTSP connect: %w", err)
	}

	desc, _, err := c.Describe(u)
	if err != nil {
		c.Close()
		return nil, nil, nil, nil, fmt.Errorf("RTSP DESCRIBE: %w", err)
	}

	medi = desc.FindFormat(&forma)
	if medi == nil {
		c.Close()
		return nil, nil, nil, nil, errors.New("no H.264 track found in RTSP stream")
	}

	rtpDec, err = forma.CreateDecoder()
	if err != nil {
		c.Close()
		return nil, nil, nil, nil, fmt.Errorf("create H.264 RTP decoder: %w", err)
	}

	if _, err := c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
		c.Close()
		return nil, nil, nil, nil, fmt.Errorf("RTSP SETUP: %w", err)
	}

	return c, medi, forma, rtpDec, nil
}

// dialRTSPS connects to a Bambu RTSPS camera endpoint and returns an
// io.ReadCloser that emits an MPEG-TS stream containing H.264 video.
func dialRTSPS(ctx context.Context, tlsCfg *tls.Config, host, accessCode string) (io.ReadCloser, error) {
	timeout := rtspTimeout(ctx)
	c, medi, forma, rtpDec, err := connectRTSPSH264(tlsCfg, host, accessCode, timeout)
	if err != nil {
		return nil, err
	}

	dataCh := make(chan []byte, 256)
	s := &rtspStream{
		client: c,
		dataCh: dataCh,
	}

	// MPEG-TS muxer writes to an intermediate buffer per callback invocation.
	var tsBuf bytes.Buffer
	track := &mpegts.Track{Codec: &codecs.H264{}}
	tsWriter := &mpegts.Writer{
		W:      &tsBuf,
		Tracks: []*mpegts.Track{track},
	}
	if initErr := tsWriter.Initialize(); initErr != nil {
		c.Close()
		return nil, fmt.Errorf("MPEG-TS init: %w", initErr)
	}

	var ptsOffset int64
	var ptsInited bool

	c.OnPacketRTP(medi, forma, func(pkt *rtp.Packet) {
		au, decErr := rtpDec.Decode(pkt)
		if decErr != nil {
			if !errors.Is(decErr, rtph264.ErrNonStartingPacketAndNoPrevious) &&
				!errors.Is(decErr, rtph264.ErrMorePacketsNeeded) {
				s.closeWithError(fmt.Errorf("RTP decode: %w", decErr))
			}
			return
		}

		// Use RTP timestamp directly as PTS (both use 90kHz clock).
		rtpTS := int64(pkt.Timestamp)
		if !ptsInited {
			ptsOffset = rtpTS
			ptsInited = true
		}
		pts := rtpTS - ptsOffset

		tsBuf.Reset()
		if writeErr := tsWriter.WriteH264(track, pts, pts, au); writeErr != nil {
			s.closeWithError(fmt.Errorf("MPEG-TS write: %w", writeErr))
			return
		}

		if tsBuf.Len() > 0 {
			data := make([]byte, tsBuf.Len())
			copy(data, tsBuf.Bytes())
			// Non-blocking send: drop frame if channel is full (reader too slow).
			select {
			case dataCh <- data:
			default:
			}
		}
	})

	_, err = c.Play(nil)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("RTSP PLAY: %w", err)
	}

	// Monitor for fatal errors from the RTSP client.
	go func() {
		waitErr := c.Wait()
		if waitErr != nil {
			s.closeWithError(waitErr)
		} else {
			s.once.Do(func() {
				s.client.Close()
				close(s.dataCh)
			})
		}
	}()

	return s, nil
}

func (s *rtspStream) Read(p []byte) (int, error) {
	// Return leftover from a previous read first.
	if len(s.buf) > 0 {
		n := copy(p, s.buf)
		s.buf = s.buf[n:]
		return n, nil
	}

	data, ok := <-s.dataCh
	if !ok {
		if s.err != nil {
			return 0, s.err
		}
		return 0, io.EOF
	}

	n := copy(p, data)
	if n < len(data) {
		s.buf = data[n:]
	}
	return n, nil
}

func (s *rtspStream) Close() error {
	s.once.Do(func() {
		s.client.Close()
		close(s.dataCh)
	})
	return nil
}

func (s *rtspStream) closeWithError(err error) {
	s.once.Do(func() {
		s.err = err
		s.client.Close()
		close(s.dataCh)
	})
}

// Ensure rtspStream satisfies io.ReadCloser at compile time.
var _ io.ReadCloser = (*rtspStream)(nil)
