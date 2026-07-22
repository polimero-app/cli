package camera

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnnexBScanner_ParsesNALUs(t *testing.T) {
	// Build an Annex-B stream: SPS + PPS + IDR (each with 4-byte start code).
	sps := []byte{0x67, 0x42, 0xc0, 0x0a} // NALU type 7
	pps := []byte{0x68, 0xcb, 0x83}       // NALU type 8
	idr := []byte{0x65, 0x88, 0x84}       // NALU type 5

	var buf bytes.Buffer
	for _, nalu := range [][]byte{sps, pps, idr} {
		buf.Write([]byte{0, 0, 0, 1})
		buf.Write(nalu)
	}

	scanner := newAnnexBScanner(&buf)
	au, err := scanner.nextAU()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should get SPS + PPS prepended to IDR slice.
	if len(au) != 3 {
		t.Fatalf("expected 3 NALUs in AU, got %d", len(au))
	}
	if au[0][0]&0x1F != 7 {
		t.Errorf("first NALU should be SPS (type 7), got type %d", au[0][0]&0x1F)
	}
	if au[1][0]&0x1F != 8 {
		t.Errorf("second NALU should be PPS (type 8), got type %d", au[1][0]&0x1F)
	}
	if au[2][0]&0x1F != 5 {
		t.Errorf("third NALU should be IDR (type 5), got type %d", au[2][0]&0x1F)
	}
}

func TestAnnexBScanner_ThreeByteStartCode(t *testing.T) {
	// 3-byte start code: 00 00 01
	nonIDR := []byte{0x41, 0x01, 0x02} // NALU type 1 (non-IDR)
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 1})
	buf.Write(nonIDR)

	scanner := newAnnexBScanner(&buf)
	au, err := scanner.nextAU()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(au) != 1 {
		t.Fatalf("expected 1 NALU, got %d", len(au))
	}
	if au[0][0]&0x1F != 1 {
		t.Errorf("expected non-IDR (type 1), got type %d", au[0][0]&0x1F)
	}
}

func TestAnnexBScanner_MultipleAUs(t *testing.T) {
	// Two non-IDR slices = two AUs.
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 1})
	buf.Write([]byte{0x41, 0xaa, 0xbb}) // non-IDR 1
	buf.Write([]byte{0, 0, 0, 1})
	buf.Write([]byte{0x41, 0xcc, 0xdd}) // non-IDR 2

	scanner := newAnnexBScanner(&buf)

	au1, err := scanner.nextAU()
	if err != nil {
		t.Fatalf("AU1 error: %v", err)
	}
	if len(au1) != 1 || au1[0][1] != 0xaa {
		t.Errorf("AU1 unexpected: %v", au1)
	}

	au2, err := scanner.nextAU()
	if err != nil {
		t.Fatalf("AU2 error: %v", err)
	}
	if len(au2) != 1 || au2[0][1] != 0xcc {
		t.Errorf("AU2 unexpected: %v", au2)
	}
}

func TestAnnexBScanner_EOF(t *testing.T) {
	scanner := newAnnexBScanner(strings.NewReader(""))
	_, err := scanner.nextAU()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestAnnexBScanner_SkipsSEI(t *testing.T) {
	// SEI (type 6) followed by non-IDR (type 1) — should skip SEI, return non-IDR.
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 1})
	buf.Write([]byte{0x06, 0x01, 0x02}) // SEI
	buf.Write([]byte{0, 0, 0, 1})
	buf.Write([]byte{0x41, 0x11, 0x22}) // non-IDR

	scanner := newAnnexBScanner(&buf)
	au, err := scanner.nextAU()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(au) != 1 || au[0][0]&0x1F != 1 {
		t.Errorf("expected non-IDR AU, got %v", au)
	}
}

func TestAnnexBScanner_IDRPrependsSPSPPS(t *testing.T) {
	// SPS, PPS, then IDR — scanner should prepend stored SPS+PPS to IDR AU.
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 1})
	buf.Write([]byte{0x67, 0x01}) // SPS
	buf.Write([]byte{0, 0, 0, 1})
	buf.Write([]byte{0x68, 0x02}) // PPS
	buf.Write([]byte{0, 0, 0, 1})
	buf.Write([]byte{0x65, 0x03}) // IDR

	scanner := newAnnexBScanner(&buf)
	au, err := scanner.nextAU()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(au) != 3 {
		t.Fatalf("expected 3 NALUs (SPS+PPS+IDR), got %d", len(au))
	}
}

func TestAnnexBScanner_NonIDRDoesNotPrependSPSPPS(t *testing.T) {
	// SPS, PPS, then non-IDR — non-IDR doesn't get SPS/PPS prepended.
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 1})
	buf.Write([]byte{0x67, 0x01}) // SPS
	buf.Write([]byte{0, 0, 0, 1})
	buf.Write([]byte{0x68, 0x02}) // PPS
	buf.Write([]byte{0, 0, 0, 1})
	buf.Write([]byte{0x41, 0x03}) // non-IDR

	scanner := newAnnexBScanner(&buf)
	au, err := scanner.nextAU()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(au) != 1 {
		t.Fatalf("expected 1 NALU (non-IDR only), got %d", len(au))
	}
}

func TestMjpegTranscodeHandler_503OnConcurrent(t *testing.T) {
	// A reader that blocks forever on Read.
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	handler := mjpegTranscodeHandler(pr)

	// First request: start in background, it will block in the read loop.
	firstReady := make(chan struct{})
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/stream", nil)
		// Signal once we've entered the handler (handler sets active=true).
		// We can't observe that directly, so we use a small sleep.
		close(firstReady)
		handler.ServeHTTP(rec, req)
	}()

	<-firstReady
	// Give the goroutine time to enter the handler and set active=true.
	time.Sleep(50 * time.Millisecond)

	// Second request: should get 503.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for concurrent client, got %d", rec.Code)
	}
}

func TestMjpegTranscodeHandler_ContentType(t *testing.T) {
	// Empty reader — handler will try to read, get EOF, and close.
	srv := httptest.NewServer(mjpegTranscodeHandler(strings.NewReader("")))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "multipart/x-mixed-replace") {
		t.Errorf("expected multipart/x-mixed-replace content type, got %q", ct)
	}
}
