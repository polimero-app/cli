package camera

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/polimero-app/cli/internal/driver"
)

func TestStreamContentType_H264IsRawVideo(t *testing.T) {
	contentType, err := streamContentType(driver.CameraFormatH264)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if contentType != "video/h264" {
		t.Fatalf("content type = %q, want video/h264", contentType)
	}
}

// blockingReader emits one chunk, then blocks until released, then returns EOF.
type blockingReader struct {
	once    sync.Once
	release chan struct{}
}

func (r *blockingReader) Read(p []byte) (int, error) {
	sent := false
	r.once.Do(func() {
		sent = true
	})
	if sent {
		return copy(p, []byte("frame")), nil
	}
	<-r.release
	return 0, io.EOF
}

func TestStreamHandler_SecondConcurrentClient_Gets503(t *testing.T) {
	reader := &blockingReader{release: make(chan struct{})}
	srv := httptest.NewServer(streamHandler(reader, "video/h264"))
	defer srv.Close()

	firstStarted := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		resp, err := http.Get(srv.URL)
		if err != nil {
			firstDone <- err
			close(firstStarted)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		buf := make([]byte, 16)
		_, _ = resp.Body.Read(buf)
		close(firstStarted)
		_, err = io.Copy(io.Discard, resp.Body)
		firstDone <- err
	}()

	<-firstStarted

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for concurrent client, got %d", resp.StatusCode)
	}

	close(reader.release)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first client error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first client did not finish")
	}
}

func TestStreamHandler_ClientAfterDisconnect_Succeeds(t *testing.T) {
	reader := &blockingReader{release: make(chan struct{})}
	close(reader.release)
	srv := httptest.NewServer(streamHandler(reader, "video/h264"))
	defer srv.Close()

	for i := 0; i < 2; i++ {
		resp, err := http.Get(srv.URL)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, resp.StatusCode)
		}
		_ = body
	}
}
