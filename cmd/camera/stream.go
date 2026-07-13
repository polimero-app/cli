package camera

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/protocoltrace"
	"github.com/spf13/cobra"
)

const commandStream = "camera stream"

// streamCommandWithDeps constructs the "camera stream" cobra command with injected dependencies.
func streamCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		port          int
		timeout       string
		insecure      bool
		protocolTrace string
	}

	cmd := &cobra.Command{
		Use:   "stream <name>",
		Short: "Stream camera feed from a printer via a local HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return writeUsageError(cmd, commandStream, "profile name is required")
			}
			if len(args) > 1 {
				return writeUsageError(cmd, commandStream, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runStream(cmd, args[0], flags.port, flags.timeout, flags.insecure, flags.protocolTrace, deps)
		},
	}
	cmd.Flags().IntVar(&flags.port, "port", 8080, "local HTTP server port")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "auto-stop after this duration (e.g. 30m)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	cmd.Flags().StringVar(&flags.protocolTrace, "protocol-trace", "", "write protocol diagnostics to this file (JSON Lines)")
	return cmd
}

func runStream(cmd *cobra.Command, nameArg string, port int, timeoutFlag string, insecureFlag bool, protocolTrace string, deps Deps) (retErr error) {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return writeUsageError(cmd, commandStream, fmtErr.Error())
	}

	if err := validateFlags(port, timeoutFlag); err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStream, err)
	}

	traceCtx, traceCleanup, traceErr := protocoltrace.Setup(cmd.Context(), protocolTrace)
	if traceErr != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStream, traceErr)
	}
	defer protocoltrace.Finish(traceCleanup, cmd.ErrOrStderr(), &retErr)

	signalCtx, signalStop := signal.NotifyContext(traceCtx, syscall.SIGINT, syscall.SIGTERM)
	defer signalStop()
	streamCtx, streamCancel := context.WithCancel(signalCtx)
	defer streamCancel()

	result, name, err := openStream(streamCtx, cmd, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStream, err)
	}
	defer func() { _ = result.Stream.Close() }()

	if timeoutFlag != "" {
		duration, _ := time.ParseDuration(timeoutFlag)
		timer := time.AfterFunc(duration, streamCancel)
		defer timer.Stop()
	}

	return serve(cmd, streamCtx, name, port, format, result)
}

func validateFlags(port int, timeoutFlag string) error {
	if port < 1 || port > 65535 {
		return apperr.Newf(2, "--port must be between 1 and 65535, got %d", port)
	}
	if timeoutFlag != "" {
		d, err := time.ParseDuration(timeoutFlag)
		if err != nil {
			return apperr.Newf(2, "invalid --timeout %q: %s", timeoutFlag, err)
		}
		if d <= 0 {
			return apperr.New(2, "--timeout must be greater than zero")
		}
	}
	return nil
}

func openStream(ctx context.Context, cmd *cobra.Command, nameArg, timeoutFlag string, insecureFlag bool, deps Deps) (*driver.CameraStreamResult, string, error) {
	rp, err := resolveProfile(ctx, nameArg, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return nil, "", err
	}
	if !rp.driver.Capabilities().CameraStream {
		return nil, "", apperr.Newf(5, "driver %q does not support camera streaming", rp.input.Driver)
	}

	result, err := rp.driver.CameraStream(ctx, rp.input, rp.secrets, deps.Log)
	if err != nil {
		return nil, "", err
	}
	if result == nil || result.Stream == nil {
		return nil, "", apperr.New(1, "driver returned an invalid camera stream")
	}
	return result, rp.name, nil
}

// requireLoopbackHost rejects requests whose Host header does not name a
// loopback address. The server only listens on 127.0.0.1, but a DNS-rebinding
// attack (a hostile domain resolving to 127.0.0.1) would otherwise let a web
// page read the camera feed through the victim's browser.
func requireLoopbackHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !isLoopbackHost(host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && ip.IsLoopback()
}

// streamHandler serves the single upstream camera feed to at most one HTTP
// client at a time. The feed cannot be duplicated, so concurrent requests
// receive 503 Service Unavailable until the active client disconnects.
func streamHandler(stream io.Reader, contentType string) http.Handler {
	var active atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !active.CompareAndSwap(false, true) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "stream already has an active client", http.StatusServiceUnavailable)
			return
		}
		defer active.Store(false)

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 32*1024)
		for {
			n, readErr := stream.Read(buf)
			if n > 0 {
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if readErr != nil {
				return
			}
		}
	})
}

func serve(cmd *cobra.Command, ctx context.Context, name string, port int, format output.Format, result *driver.CameraStreamResult) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Check port availability before starting the server.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		_ = result.Stream.Close()
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStream,
			apperr.Newf(2, "port %d is already in use or unavailable", port))
	}

	url := fmt.Sprintf("http://%s/stream", addr)

	contentType, err := streamContentType(result.Format)
	if err != nil {
		_ = ln.Close()
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, commandStream, err)
	}

	mux := http.NewServeMux()
	mux.Handle("/stream", streamHandler(result.Stream, contentType))

	srv := &http.Server{
		Handler:     requireLoopbackHost(mux),
		ReadTimeout: 5 * time.Second,
	}

	// Write output once server is ready.
	if format == output.FormatJSON {
		_ = writeStreamJSONSuccess(cmd.OutOrStdout(), name, url, string(result.Format), port)
	} else {
		writeHumanStart(cmd.OutOrStdout(), name, result.Format, url)
	}

	// Start server in background.
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Serve(ln)
	}()

	// Wait for signal, timeout, or server error.
	select {
	case <-ctx.Done():
		// Clean exit on signal, caller cancellation, or auto-stop timeout.
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return apperr.Wrap(1, "HTTP server error", err)
		}
		return nil
	}

	// Graceful shutdown.
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)

	if format == output.FormatHuman {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Stream stopped.")
	}
	return nil
}

func streamContentType(format driver.CameraFormat) (string, error) {
	switch format {
	case driver.CameraFormatMJPEG:
		return "multipart/x-mixed-replace; boundary=frame", nil
	case driver.CameraFormatH264:
		return "video/h264", nil
	default:
		return "", apperr.Newf(1, "driver returned unsupported camera format %q", format)
	}
}

func writeHumanStart(w io.Writer, name string, format driver.CameraFormat, url string) {
	var formatDesc string
	switch format {
	case driver.CameraFormatMJPEG:
		formatDesc = "MJPEG (open in browser)"
	case driver.CameraFormatH264:
		formatDesc = "H.264 (open with VLC or mpv)"
	}
	_, _ = fmt.Fprintf(w, "Streaming camera from %s\n", name)
	_, _ = fmt.Fprintf(w, "Format: %s\n", formatDesc)
	_, _ = fmt.Fprintf(w, "URL: %s\n", url)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Press Ctrl+C to stop.")
}

func writeStreamJSONSuccess(w io.Writer, name, url, format string, port int) error {
	type streamData struct {
		Profile string `json:"profile"`
		URL     string `json:"url"`
		Format  string `json:"format"`
		Port    int    `json:"port"`
	}
	return output.WriteEnvelope(w, output.Envelope{
		OK: true,
		Data: streamData{
			Profile: name,
			URL:     url,
			Format:  format,
			Port:    port,
		},
		Error: nil,
		Meta:  output.Meta{Command: commandStream},
	})
}
