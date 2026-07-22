package camera

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/polimero-app/cli/internal/h264decode"
)

const mjpegBoundary = "frame"

// mjpegTranscodeHandler reads an H.264 Annex-B stream, decodes each access
// unit into a frame, JPEG-encodes it, and writes it as a multipart MJPEG
// response. Single-client only, same as streamHandler.
func mjpegTranscodeHandler(stream io.Reader) http.Handler {
	var active atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !active.CompareAndSwap(false, true) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "stream already has an active client", http.StatusServiceUnavailable)
			return
		}
		defer active.Store(false)

		dec, err := h264decode.New()
		if err != nil {
			http.Error(w, "transcoder initialization failed", http.StatusInternalServerError)
			return
		}
		defer dec.Close()

		contentType := fmt.Sprintf("multipart/x-mixed-replace; boundary=%s", mjpegBoundary)
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		scanner := newAnnexBScanner(stream)
		var jpegBuf bytes.Buffer

		for {
			au, scanErr := scanner.nextAU()
			if scanErr != nil {
				return
			}
			if len(au) == 0 {
				continue
			}

			img, decErr := dec.Decode(au)
			if decErr != nil || img == nil {
				continue
			}

			jpegBuf.Reset()
			if err := jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: 80}); err != nil {
				continue
			}

			// Write multipart boundary + JPEG frame.
			header := fmt.Sprintf("\r\n--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", mjpegBoundary, jpegBuf.Len())
			if _, err := io.WriteString(w, header); err != nil {
				return
			}
			if _, err := w.Write(jpegBuf.Bytes()); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
}

// annexBScanner splits an H.264 Annex-B byte stream into access units.
// Each AU is a slice of NALUs. AUs are delimited by Access Unit Delimiter
// NALUs or by IDR/non-IDR boundaries following parameter sets.
//
// ponytail: simple approach — each start-code-delimited NALU that is an
// IDR or non-IDR is treated as a complete AU (with any preceding SPS/PPS
// prepended). Good enough for Bambu streams which send one slice per AU.
// If multi-slice AUs matter, upgrade to proper AU delimiting.
type annexBScanner struct {
	reader io.Reader
	buf    []byte
	pos    int
	eof    bool

	// Pending parameter sets to prepend to next slice.
	sps []byte
	pps []byte
}

func newAnnexBScanner(r io.Reader) *annexBScanner {
	return &annexBScanner{
		reader: r,
		buf:    make([]byte, 0, 256*1024),
	}
}

// nextAU returns the next access unit as a slice of NALUs.
// Returns nil, io.EOF on stream end.
func (s *annexBScanner) nextAU() ([][]byte, error) {
	for {
		nalu, err := s.nextNALU()
		if err != nil {
			return nil, err
		}
		if len(nalu) == 0 {
			continue
		}

		naluType := nalu[0] & 0x1F
		switch naluType {
		case 7: // SPS
			s.sps = append([]byte(nil), nalu...)
		case 8: // PPS
			s.pps = append([]byte(nil), nalu...)
		case 5, 1: // IDR, non-IDR — this is a picture slice, emit as AU
			au := make([][]byte, 0, 3)
			if len(s.sps) > 0 && naluType == 5 {
				au = append(au, s.sps, s.pps)
			}
			au = append(au, nalu)
			return au, nil
		}
		// SEI, AUD, filler — skip
	}
}

// nextNALU reads the next NALU from the Annex-B stream.
func (s *annexBScanner) nextNALU() ([]byte, error) {
	// Ensure we have data to scan.
	if err := s.fill(); err != nil {
		return nil, err
	}

	// Find start of NALU (skip to first start code).
	start := s.findStartCode(s.pos)
	if start < 0 {
		if s.eof {
			return nil, io.EOF
		}
		// Discard data before next fill.
		s.compact(s.pos)
		return nil, s.fill()
	}

	// Skip the start code itself (3 or 4 bytes).
	naluStart := start
	if naluStart+4 <= len(s.buf) && s.buf[naluStart] == 0 && s.buf[naluStart+1] == 0 && s.buf[naluStart+2] == 0 && s.buf[naluStart+3] == 1 {
		naluStart += 4
	} else {
		naluStart += 3
	}

	// Find end (next start code or EOF).
	for {
		end := s.findStartCode(naluStart)
		if end >= 0 {
			s.pos = end
			return append([]byte(nil), s.buf[naluStart:end]...), nil
		}
		if s.eof {
			nalu := append([]byte(nil), s.buf[naluStart:]...)
			s.pos = len(s.buf)
			if len(nalu) == 0 {
				return nil, io.EOF
			}
			return nalu, nil
		}
		// Need more data.
		s.compact(start)
		naluStart -= start
		if err := s.fillMore(); err != nil && !s.eof {
			return nil, err
		}
	}
}

func (s *annexBScanner) findStartCode(from int) int {
	for i := from; i+3 <= len(s.buf); i++ {
		if s.buf[i] == 0 && s.buf[i+1] == 0 {
			if s.buf[i+2] == 1 {
				return i
			}
			if i+3 < len(s.buf) && s.buf[i+2] == 0 && s.buf[i+3] == 1 {
				return i
			}
		}
	}
	return -1
}

func (s *annexBScanner) compact(from int) {
	n := copy(s.buf, s.buf[from:])
	s.buf = s.buf[:n]
	s.pos = 0
}

func (s *annexBScanner) fill() error {
	if len(s.buf) > s.pos && s.findStartCode(s.pos) >= 0 {
		return nil
	}
	return s.fillMore()
}

func (s *annexBScanner) fillMore() error {
	if s.eof {
		if len(s.buf) > s.pos {
			return nil
		}
		return io.EOF
	}
	tmp := make([]byte, 32*1024)
	n, err := s.reader.Read(tmp)
	if n > 0 {
		s.buf = append(s.buf, tmp[:n]...)
	}
	if err != nil {
		s.eof = true
		if err != io.EOF {
			return err
		}
	}
	return nil
}
