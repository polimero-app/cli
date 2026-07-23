package bambulan

import (
	"bytes"
	"crypto/tls"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
)

func TestPrepareAnnexBAccessUnit_StartsWithParameterSetsAndIDR(t *testing.T) {
	sps := []byte{0x67, 0x64}
	pps := []byte{0x68, 0xee}
	started := false

	if data := prepareAnnexBAccessUnit([][]byte{{0x41, 0x01}}, &sps, &pps, &started); data != nil {
		t.Fatalf("non-IDR access unit unexpectedly started stream: %x", data)
	}
	data := prepareAnnexBAccessUnit([][]byte{{0x65, 0xaa}}, &sps, &pps, &started)
	want := []byte{
		0, 0, 0, 1, 0x67, 0x64,
		0, 0, 0, 1, 0x68, 0xee,
		0, 0, 0, 1, 0x65, 0xaa,
	}
	if !bytes.Equal(data, want) {
		t.Fatalf("Annex-B data = %x, want %x", data, want)
	}
	if !started {
		t.Fatal("stream was not marked started")
	}
	if bytes.HasPrefix(data, []byte{0x47}) {
		t.Fatal("stream contains an MPEG-TS sync byte")
	}
}

func TestPrepareAnnexBAccessUnit_UpdatesParameterSetsBeforeFirstIDR(t *testing.T) {
	var sps, pps []byte
	started := false
	data := prepareAnnexBAccessUnit([][]byte{
		{0x67, 0x01}, {0x68, 0x02}, {0x65, 0x03},
	}, &sps, &pps, &started)
	if bytes.Count(data, []byte{0, 0, 0, 1}) != 3 {
		t.Fatalf("expected exactly three Annex-B NALUs, got %x", data)
	}
}

// rtspTestHandler serves a single pre-built H.264 stream to any reader and
// signals on playCh once a client has entered the PLAY state.
type rtspTestHandler struct {
	server *gortsplib.Server
	stream *gortsplib.ServerStream
	mu     sync.Mutex
	playCh chan struct{}
}

func (h *rtspTestHandler) OnDescribe(
	_ *gortsplib.ServerHandlerOnDescribeCtx,
) (*base.Response, *gortsplib.ServerStream, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *rtspTestHandler) OnSetup(
	_ *gortsplib.ServerHandlerOnSetupCtx,
) (*base.Response, *gortsplib.ServerStream, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *rtspTestHandler) OnPlay(_ *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	select {
	case h.playCh <- struct{}{}:
	default:
	}
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// startRTSPSTestServer starts a TLS RTSP server with one H.264 track on a
// random localhost port and returns the handler and port.
func startRTSPSTestServer(t *testing.T) (*rtspTestHandler, *description.Media, int) {
	t.Helper()

	cert := makeSelfSignedTLSCert(t)

	// Reserve a random free port for the server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	forma := &format.H264{
		PayloadTyp:        96,
		PacketizationMode: 1,
	}
	medi := &description.Media{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{forma},
	}

	h := &rtspTestHandler{playCh: make(chan struct{}, 1)}
	h.server = &gortsplib.Server{
		Handler:     h,
		RTSPAddress: "127.0.0.1:" + strconv.Itoa(port),
		TLSConfig:   &tls.Config{Certificates: []tls.Certificate{cert}},
	}
	if err := h.server.Start(); err != nil {
		t.Fatalf("start RTSP server: %v", err)
	}
	t.Cleanup(h.server.Close)

	h.stream = &gortsplib.ServerStream{
		Server: h.server,
		Desc:   &description.Session{Medias: []*description.Media{medi}},
	}
	if err := h.stream.Initialize(); err != nil {
		t.Fatalf("init server stream: %v", err)
	}
	t.Cleanup(h.stream.Close)

	return h, medi, port
}

// TestRTSPStream_FatalDecodeErrorDoesNotDeadlock is a regression test for a
// deadlock: the OnPacketRTP callback used to call client.Close() directly on
// a fatal decode error, but with TCP-interleaved transport gortsplib runs
// that callback on its connection-reader goroutine, and Close() waits for
// that same goroutine to exit. The stream then hung forever, as did every
// later Close() caller blocking on the shared sync.Once.
func TestRTSPStream_FatalDecodeErrorDoesNotDeadlock(t *testing.T) {
	h, medi, port := startRTSPSTestServer(t)

	clientTLS := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test server uses a throwaway self-signed cert
	c, cliMedi, forma, rtpDec, err := connectRTSPSH264(clientTLS, "127.0.0.1", "testcode", port, 5*time.Second)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	stream, err := newRTSPStream(c, cliMedi, forma, rtpDec)
	if err != nil {
		t.Fatalf("newRTSPStream: %v", err)
	}

	select {
	case <-h.playCh:
	case <-time.After(5 * time.Second):
		t.Fatal("client never reached PLAY state")
	}

	// A one-byte FU-A payload (NALU type 28) with no FU header is a fatal
	// "invalid FU-A packet" decode error, not one of the two ignored
	// benign sentinel errors.
	pkt := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    96,
			SequenceNumber: 1,
			Timestamp:      90000,
			SSRC:           0x1234,
		},
		Payload: []byte{0x1c},
	}
	if err := h.stream.WritePacketRTP(medi, pkt); err != nil {
		t.Fatalf("write RTP packet: %v", err)
	}

	// Read must observe the fatal error instead of blocking forever.
	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, readErr := stream.Read(buf); readErr != nil {
				readDone <- readErr
				return
			}
		}
	}()

	select {
	case readErr := <-readDone:
		if readErr == nil {
			t.Fatal("expected a non-nil stream error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: Read did not return after fatal decode error")
	}

	// Close must also return promptly rather than blocking on the Once.
	closeDone := make(chan struct{})
	go func() {
		_ = stream.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: Close did not return after fatal decode error")
	}
}

// TestRTSPStream_SlowReaderGetsNewestFrame is a regression test for the live
// view showing an ever-growing backlog instead of the current frame: the
// data channel used to be a 256-deep FIFO that dropped the newest frame
// once full, so a decoder slower than the camera's frame rate spent its
// whole life chewing through stale frames instead of the latest one. It
// must instead evict the stale buffered frame and keep only the newest.
func TestRTSPStream_SlowReaderGetsNewestFrame(t *testing.T) {
	h, medi, port := startRTSPSTestServer(t)

	clientTLS := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test server uses a throwaway self-signed cert
	c, cliMedi, forma, rtpDec, err := connectRTSPSH264(clientTLS, "127.0.0.1", "testcode", port, 5*time.Second)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	stream, err := newRTSPStream(c, cliMedi, forma, rtpDec)
	if err != nil {
		t.Fatalf("newRTSPStream: %v", err)
	}
	defer stream.Close()

	select {
	case <-h.playCh:
	case <-time.After(5 * time.Second):
		t.Fatal("client never reached PLAY state")
	}

	// Every access unit below carries its own SPS/PPS and marker=true, so
	// each RTP write completes one whole access unit (see rtph264 Decoder:
	// a packet with Marker set closes out the frame buffer immediately).
	writeFrame := func(ts uint32, seqStart uint16, payload byte) {
		t.Helper()
		pkts := []*rtp.Packet{
			{Header: rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seqStart, Timestamp: ts, SSRC: 1}, Payload: []byte{0x67, 0x01}},                      // SPS
			{Header: rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seqStart + 1, Timestamp: ts, SSRC: 1}, Payload: []byte{0x68, 0x02}},                  // PPS
			{Header: rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seqStart + 2, Timestamp: ts, Marker: true, SSRC: 1}, Payload: []byte{0x65, payload}}, // IDR
		}
		for _, pkt := range pkts {
			if err := h.stream.WritePacketRTP(medi, pkt); err != nil {
				t.Fatalf("write RTP packet: %v", err)
			}
		}
	}

	// Send three frames back-to-back without reading any of them, as a
	// decoder that can't keep up with the camera would. Give the
	// single connection-reader goroutine time to process all three before
	// the first Read call.
	writeFrame(0, 0, 0xAA)
	writeFrame(90000, 3, 0xBB)
	writeFrame(180000, 6, 0xCC)
	time.Sleep(500 * time.Millisecond)

	buf := make([]byte, 4096)
	n, err := stream.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got := buf[:n]
	if !bytes.Contains(got, []byte{0x65, 0xCC}) {
		t.Fatalf("Read returned stale frame, want newest (0xCC): %x", got)
	}
	if bytes.Contains(got, []byte{0x65, 0xAA}) || bytes.Contains(got, []byte{0x65, 0xBB}) {
		t.Fatalf("Read returned a backlog of stale frames instead of just the newest: %x", got)
	}
}

// TestRTSPStream_CloseWhileStreaming verifies a plain Close with no error
// terminates the stream and unblocks a concurrent reader.
func TestRTSPStream_CloseWhileStreaming(t *testing.T) {
	h, _, port := startRTSPSTestServer(t)

	clientTLS := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test server uses a throwaway self-signed cert
	c, cliMedi, forma, rtpDec, err := connectRTSPSH264(clientTLS, "127.0.0.1", "testcode", port, 5*time.Second)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	stream, err := newRTSPStream(c, cliMedi, forma, rtpDec)
	if err != nil {
		t.Fatalf("newRTSPStream: %v", err)
	}

	select {
	case <-h.playCh:
	case <-time.After(5 * time.Second):
		t.Fatal("client never reached PLAY state")
	}

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, readErr := stream.Read(buf); readErr != nil {
				readDone <- readErr
				return
			}
		}
	}()

	closeDone := make(chan struct{})
	go func() {
		_ = stream.Close()
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
	}
	select {
	case <-readDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Read did not unblock after Close")
	}
}
