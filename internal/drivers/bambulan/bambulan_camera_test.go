package bambulan

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

func newCameraDriver(dialFn func(context.Context, string, *tls.Config) (*tls.Conn, error)) *Driver {
	return &Driver{
		newClient:   func(_ *mqtt.ClientOptions) mqttConn { panic("not used") },
		dialTLS:     dialFn,
		dialRTSPSFn: dialRTSPS,
	}
}

// cameraServer simulates the Bambu camera: reads the 80-byte auth,
// then writes framed JPEG data, then closes.
func cameraServer(t *testing.T, tlsServer *tls.Conn, jpegFrames [][]byte) {
	t.Helper()
	if err := tlsServer.Handshake(); err != nil {
		t.Logf("server handshake: %v", err)
		return
	}
	// Read auth packet.
	var auth [80]byte
	if _, err := io.ReadFull(tlsServer, auth[:]); err != nil {
		t.Logf("server read auth: %v", err)
		_ = tlsServer.Close()
		return
	}
	// Verify auth packet structure.
	if auth[0] != 0x40 || auth[5] != 0x30 {
		t.Logf("server: bad auth magic")
		_ = tlsServer.Close()
		return
	}
	// Write frames.
	for _, frame := range jpegFrames {
		var hdr [16]byte
		binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(frame)))
		if _, err := tlsServer.Write(hdr[:]); err != nil {
			break
		}
		if _, err := tlsServer.Write(frame); err != nil {
			break
		}
	}
	_ = tlsServer.Close()
}

// fakeJPEG returns a minimal byte sequence with JPEG SOI/EOI markers.
func fakeJPEG(payload string) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xD8}) // SOI
	buf.WriteString(payload)
	buf.Write([]byte{0xFF, 0xD9}) // EOI
	return buf.Bytes()
}

func TestCameraStream_MJPEG_HappyPath(t *testing.T) {
	tlsCert := makeSelfSignedTLSCert(t)
	serverCfg := &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	sum := sha256.Sum256(tlsCert.Certificate[0])
	fp := "sha256:" + hex.EncodeToString(sum[:])

	frame1 := fakeJPEG("frame1")

	drv := newCameraDriver(func(_ context.Context, addr string, clientCfg *tls.Config) (*tls.Conn, error) {
		if !strings.Contains(addr, ":6000") {
			t.Fatalf("expected port 6000 dial for MJPEG, got %s", addr)
		}
		serverConn, clientConn := net.Pipe()
		tlsServer := tls.Server(serverConn, serverCfg)
		tlsClient := tls.Client(clientConn, clientCfg)
		go cameraServer(t, tlsServer, [][]byte{frame1})
		if err := tlsClient.Handshake(); err != nil {
			return nil, err
		}
		return tlsClient, nil
	})
	// RTSPS fails → falls back to MJPEG.
	drv.dialRTSPSFn = func(_ *tls.Config, _ string, _ string) (io.ReadCloser, error) {
		return nil, errors.New("RTSPS connection refused")
	}

	pi := driver.ProfileInput{Host: "192.0.2.1", Serial: "SN001", Insecure: true}
	secrets := driver.SecretsBundle{AccessCode: "test", TLSFingerprint: fp}
	result, err := drv.CameraStream(context.Background(), pi, secrets, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = result.Stream.Close() }()

	if result.Format != driver.CameraFormatMJPEG {
		t.Errorf("expected format mjpeg, got %q", result.Format)
	}

	// Read the multipart output and verify it contains the JPEG frame.
	data, err := io.ReadAll(result.Stream)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if !bytes.Contains(data, []byte("--frame\r\n")) {
		t.Error("expected multipart boundary in output")
	}
	if !bytes.Contains(data, []byte("Content-Type: image/jpeg")) {
		t.Error("expected Content-Type header in output")
	}
	if !bytes.Contains(data, frame1) {
		t.Error("expected JPEG frame data in output")
	}
}

func TestCameraStream_MJPEG_MultipleFrames(t *testing.T) {
	tlsCert := makeSelfSignedTLSCert(t)
	serverCfg := &tls.Config{Certificates: []tls.Certificate{tlsCert}}

	frame1 := fakeJPEG("aaa")
	frame2 := fakeJPEG("bbb")

	drv := newCameraDriver(func(_ context.Context, _ string, clientCfg *tls.Config) (*tls.Conn, error) {
		serverConn, clientConn := net.Pipe()
		tlsServer := tls.Server(serverConn, serverCfg)
		tlsClient := tls.Client(clientConn, clientCfg)
		go cameraServer(t, tlsServer, [][]byte{frame1, frame2})
		if err := tlsClient.Handshake(); err != nil {
			return nil, err
		}
		return tlsClient, nil
	})
	drv.dialRTSPSFn = func(_ *tls.Config, _ string, _ string) (io.ReadCloser, error) {
		return nil, errors.New("RTSPS connection refused")
	}

	pi := driver.ProfileInput{Host: "192.0.2.1", Serial: "SN001", Insecure: true}
	secrets := driver.SecretsBundle{AccessCode: "test"}
	result, err := drv.CameraStream(context.Background(), pi, secrets, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = result.Stream.Close() }()

	data, err := io.ReadAll(result.Stream)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if !bytes.Contains(data, frame1) {
		t.Error("expected frame1 in output")
	}
	if !bytes.Contains(data, frame2) {
		t.Error("expected frame2 in output")
	}
	// Should have two boundary markers.
	count := bytes.Count(data, []byte("--frame\r\n"))
	if count != 2 {
		t.Errorf("expected 2 boundaries, got %d", count)
	}
}

func TestCameraStream_AuthPacketFormat(t *testing.T) {
	tlsCert := makeSelfSignedTLSCert(t)
	serverCfg := &tls.Config{Certificates: []tls.Certificate{tlsCert}}

	var receivedAuth [80]byte
	authCh := make(chan struct{})

	drv := newCameraDriver(func(_ context.Context, _ string, clientCfg *tls.Config) (*tls.Conn, error) {
		serverConn, clientConn := net.Pipe()
		tlsServer := tls.Server(serverConn, serverCfg)
		tlsClient := tls.Client(clientConn, clientCfg)
		go func() {
			if err := tlsServer.Handshake(); err != nil {
				return
			}
			_, _ = io.ReadFull(tlsServer, receivedAuth[:])
			close(authCh)
			// Send a valid MJPEG frame so the stream succeeds.
			frame := fakeJPEG("auth-test")
			var hdr [16]byte
			binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(frame)))
			_, _ = tlsServer.Write(hdr[:])
			_, _ = tlsServer.Write(frame)
			_ = tlsServer.Close()
		}()
		if err := tlsClient.Handshake(); err != nil {
			return nil, err
		}
		return tlsClient, nil
	})
	drv.dialRTSPSFn = func(_ *tls.Config, _ string, _ string) (io.ReadCloser, error) {
		return nil, errors.New("RTSPS connection refused")
	}

	pi := driver.ProfileInput{Host: "192.0.2.1", Serial: "SN001", Insecure: true}
	secrets := driver.SecretsBundle{AccessCode: "mycode123"}
	result, err := drv.CameraStream(context.Background(), pi, secrets, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = result.Stream.Close()

	<-authCh

	// Verify auth packet structure.
	if receivedAuth[0] != 0x40 {
		t.Errorf("auth[0] = %#x, want 0x40", receivedAuth[0])
	}
	if receivedAuth[4] != 0x00 || receivedAuth[5] != 0x30 {
		t.Errorf("auth[4:6] = %#x %#x, want 0x00 0x30", receivedAuth[4], receivedAuth[5])
	}
	// Username at offset 16.
	username := string(bytes.TrimRight(receivedAuth[16:48], "\x00"))
	if username != "bblp" {
		t.Errorf("username = %q, want %q", username, "bblp")
	}
	// Access code at offset 48.
	code := string(bytes.TrimRight(receivedAuth[48:80], "\x00"))
	if code != "mycode123" {
		t.Errorf("access code = %q, want %q", code, "mycode123")
	}
}

func TestCameraStream_FallbackToH264(t *testing.T) {
	drv := newCameraDriver(func(_ context.Context, _ string, _ *tls.Config) (*tls.Conn, error) {
		t.Fatal("dialTLS should not be called when RTSPS succeeds")
		return nil, nil
	})

	// RTSPS succeeds directly.
	drv.dialRTSPSFn = func(_ *tls.Config, host, accessCode string) (io.ReadCloser, error) {
		if host != "192.0.2.1" {
			t.Fatalf("unexpected host: %s", host)
		}
		if accessCode != "test" {
			t.Fatalf("unexpected access code: %s", accessCode)
		}
		return io.NopCloser(strings.NewReader("h264-annex-b-data")), nil
	}

	pi := driver.ProfileInput{Host: "192.0.2.1", Serial: "SN001", Insecure: true}
	secrets := driver.SecretsBundle{AccessCode: "test"}
	result, err := drv.CameraStream(context.Background(), pi, secrets, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = result.Stream.Close() }()

	if result.Format != driver.CameraFormatH264 {
		t.Errorf("expected format h264, got %q", result.Format)
	}

	// H.264 returns raw stream, verify readable.
	data, _ := io.ReadAll(result.Stream)
	if string(data) != "h264-annex-b-data" {
		t.Errorf("h264 stream = %q, want %q", data, "h264-annex-b-data")
	}
}

func TestCameraStream_BothPortsFailed_ExitsCode4(t *testing.T) {
	drv := newCameraDriver(func(_ context.Context, _ string, _ *tls.Config) (*tls.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	})
	drv.dialRTSPSFn = func(_ *tls.Config, _ string, _ string) (io.ReadCloser, error) {
		return nil, errors.New("RTSPS connection refused")
	}

	pi := driver.ProfileInput{Host: "192.0.2.1", Serial: "SN001", Insecure: true}
	secrets := driver.SecretsBundle{AccessCode: "test"}
	_, err := drv.CameraStream(context.Background(), pi, secrets, slog.Default())
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4, got %v", err)
	}
}

func TestCameraStream_TLSFingerprintMissing_ExitsCode3(t *testing.T) {
	drv := newCameraDriver(func(_ context.Context, _ string, _ *tls.Config) (*tls.Conn, error) {
		return nil, errors.New("should not be called")
	})

	// Non-insecure with empty fingerprint should fail at TLS config build.
	pi := driver.ProfileInput{Host: "192.0.2.1", Serial: "SN001", Insecure: false}
	secrets := driver.SecretsBundle{AccessCode: "test", TLSFingerprint: ""}
	_, err := drv.CameraStream(context.Background(), pi, secrets, slog.Default())
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestCameraStream_Capabilities_Included(t *testing.T) {
	tlsCert := makeSelfSignedTLSCert(t)
	serverCfg := &tls.Config{Certificates: []tls.Certificate{tlsCert}}

	drv := newCameraDriver(func(_ context.Context, _ string, clientCfg *tls.Config) (*tls.Conn, error) {
		serverConn, clientConn := net.Pipe()
		tlsServer := tls.Server(serverConn, serverCfg)
		tlsClient := tls.Client(clientConn, clientCfg)
		go cameraServer(t, tlsServer, [][]byte{fakeJPEG("x")})
		if err := tlsClient.Handshake(); err != nil {
			return nil, err
		}
		return tlsClient, nil
	})
	drv.dialRTSPSFn = func(_ *tls.Config, _ string, _ string) (io.ReadCloser, error) {
		return nil, errors.New("RTSPS connection refused")
	}

	pi := driver.ProfileInput{Host: "192.0.2.1", Serial: "SN001", Insecure: true}
	secrets := driver.SecretsBundle{AccessCode: "test"}
	result, err := drv.CameraStream(context.Background(), pi, secrets, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = result.Stream.Close() }()

	if !result.Capabilities.CameraStream {
		t.Error("expected CameraStream capability in result")
	}
}

func TestCameraSnapshot_H264_HappyPath(t *testing.T) {
	drv := newCameraDriver(func(_ context.Context, _ string, _ *tls.Config) (*tls.Conn, error) {
		t.Fatal("dialTLS should not be called when H.264 capture succeeds")
		return nil, nil
	})
	frame := fakeJPEG("h264")
	drv.captureH264Snapshot = func(_ context.Context, _ *tls.Config, host, accessCode string) ([]byte, error) {
		if host != "192.0.2.1" {
			t.Fatalf("unexpected host: %s", host)
		}
		if accessCode != "test" {
			t.Fatalf("unexpected access code: %s", accessCode)
		}
		return frame, nil
	}

	pi := driver.ProfileInput{Host: "192.0.2.1", Serial: "SN001", Insecure: true}
	secrets := driver.SecretsBundle{AccessCode: "test"}
	result, err := drv.CameraSnapshot(context.Background(), pi, secrets, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Protocol != cameraProtocolH264 {
		t.Fatalf("protocol = %q, want %q", result.Protocol, cameraProtocolH264)
	}
	if !bytes.Equal(result.Data, frame) {
		t.Fatalf("snapshot data = %v, want %v", result.Data, frame)
	}
	if !result.Capabilities.CameraSnapshot {
		t.Fatal("expected CameraSnapshot capability")
	}
}

func TestCameraSnapshot_MJPEG_Fallback(t *testing.T) {
	tlsCert := makeSelfSignedTLSCert(t)
	serverCfg := &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	frame := fakeJPEG("mjpeg")

	drv := newCameraDriver(func(_ context.Context, addr string, clientCfg *tls.Config) (*tls.Conn, error) {
		if !strings.Contains(addr, ":6000") {
			t.Fatalf("expected port 6000 dial for MJPEG, got %s", addr)
		}
		serverConn, clientConn := net.Pipe()
		tlsServer := tls.Server(serverConn, serverCfg)
		tlsClient := tls.Client(clientConn, clientCfg)
		go cameraServer(t, tlsServer, [][]byte{frame})
		if err := tlsClient.Handshake(); err != nil {
			return nil, err
		}
		return tlsClient, nil
	})
	drv.captureH264Snapshot = func(_ context.Context, _ *tls.Config, _ string, _ string) ([]byte, error) {
		return nil, apperr.New(4, "RTSPS connection refused")
	}

	pi := driver.ProfileInput{Host: "192.0.2.1", Serial: "SN001", Insecure: true}
	secrets := driver.SecretsBundle{AccessCode: "test"}
	result, err := drv.CameraSnapshot(context.Background(), pi, secrets, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Protocol != cameraProtocolMJPEG {
		t.Fatalf("protocol = %q, want %q", result.Protocol, cameraProtocolMJPEG)
	}
	if !bytes.Equal(result.Data, frame) {
		t.Fatalf("snapshot data = %v, want %v", result.Data, frame)
	}
}

func TestCameraSnapshot_H264DecodeError_DoesNotFallback(t *testing.T) {
	drv := newCameraDriver(func(_ context.Context, _ string, _ *tls.Config) (*tls.Conn, error) {
		t.Fatal("dialTLS should not be called when H.264 decode fails")
		return nil, nil
	})
	drv.captureH264Snapshot = func(_ context.Context, _ *tls.Config, _ string, _ string) ([]byte, error) {
		return nil, apperr.New(1, "H.264 frame decode failed")
	}

	pi := driver.ProfileInput{Host: "192.0.2.1", Serial: "SN001", Insecure: true}
	secrets := driver.SecretsBundle{AccessCode: "test"}
	_, err := drv.CameraSnapshot(context.Background(), pi, secrets, slog.Default())
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
}

func TestPrepareH264SnapshotStartAU_PrependsCachedParameters(t *testing.T) {
	params := newH264ParameterSets(
		testH264NALU(h264.NALUTypeSPS, 0x01),
		testH264NALU(h264.NALUTypePPS, 0x02),
	)
	idr := testH264NALU(h264.NALUTypeIDR, 0x03)

	got, ok := prepareH264SnapshotStartAU([][]byte{idr}, &params)
	if !ok {
		t.Fatal("expected access unit to be prepared")
	}

	want := [][]byte{params.sps, params.pps, idr}
	if !equalH264AUs(got, want) {
		t.Fatalf("prepared AU = %v, want %v", got, want)
	}
}

func TestPrepareH264SnapshotStartAU_UsesInBandParametersWithoutDuplicates(t *testing.T) {
	params := newH264ParameterSets(
		testH264NALU(h264.NALUTypeSPS, 0x01),
		testH264NALU(h264.NALUTypePPS, 0x02),
	)
	sps := testH264NALU(h264.NALUTypeSPS, 0x10)
	pps := testH264NALU(h264.NALUTypePPS, 0x20)
	idr := testH264NALU(h264.NALUTypeIDR, 0x30)

	got, ok := prepareH264SnapshotStartAU([][]byte{sps, pps, idr}, &params)
	if !ok {
		t.Fatal("expected access unit to be prepared")
	}

	want := [][]byte{sps, pps, idr}
	if !equalH264AUs(got, want) {
		t.Fatalf("prepared AU = %v, want %v", got, want)
	}
}

func TestPrepareH264SnapshotStartAU_WaitsForKeyframeAndParameters(t *testing.T) {
	tests := []struct {
		name   string
		params h264ParameterSets
		au     [][]byte
	}{
		{
			name: "missing keyframe",
			params: newH264ParameterSets(
				testH264NALU(h264.NALUTypeSPS, 0x01),
				testH264NALU(h264.NALUTypePPS, 0x02),
			),
			au: [][]byte{testH264NALU(h264.NALUTypeNonIDR, 0x03)},
		},
		{
			name:   "missing parameters",
			params: h264ParameterSets{},
			au:     [][]byte{testH264NALU(h264.NALUTypeIDR, 0x03)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, ok := prepareH264SnapshotStartAU(tt.au, &tt.params); ok || got != nil {
				t.Fatalf("prepareH264SnapshotStartAU() = %v, %v; want nil, false", got, ok)
			}
		})
	}
}

func testH264NALU(typ h264.NALUType, payload ...byte) []byte {
	nalu := []byte{0x60 | byte(typ)}
	return append(nalu, payload...)
}

func equalH264AUs(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
