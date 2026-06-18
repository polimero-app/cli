package camera

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/profile"
	"github.com/spf13/cobra"
)

// streamCommandWithDeps constructs the "camera stream" cobra command with injected dependencies.
func streamCommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		port     int
		timeout  string
		insecure bool
	}

	cmd := &cobra.Command{
		Use:   "stream <name>",
		Short: "Stream camera feed from a printer via a local HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if len(args) > 1 {
				return writeUsageError(cmd, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runStream(cmd, args[0], flags.port, flags.timeout, flags.insecure, deps)
		},
	}
	cmd.Flags().IntVar(&flags.port, "port", 8080, "local HTTP server port")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "auto-stop after this duration (e.g. 30m)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	return cmd
}

func runStream(cmd *cobra.Command, nameArg string, port int, timeoutFlag string, insecureFlag bool, deps Deps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}

	name := strings.ToLower(nameArg)

	if err := validateFlags(port, timeoutFlag); err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, err)
	}

	result, err := openStream(cmd, name, timeoutFlag, insecureFlag, deps)
	if err != nil {
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, err)
	}
	defer func() { _ = result.Stream.Close() }()

	return serve(cmd, name, port, timeoutFlag, format, result)
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

func openStream(cmd *cobra.Command, name, timeoutFlag string, insecureFlag bool, deps Deps) (*driver.CameraStreamResult, error) {
	if err := profile.ValidateName(name); err != nil {
		return nil, err
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return nil, apperr.Newf(1, "cannot resolve config directory: %s", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return nil, apperr.Newf(2, "cannot load config: %s", err)
	}
	p, ok := cfg.GetProfile(name)
	if !ok {
		return nil, apperr.Newf(2, "printer profile %q not found", name)
	}

	// Determine connection timeout for the camera probe.
	timeoutStr := p.Timeout
	if timeoutFlag != "" {
		timeoutStr = timeoutFlag
	}
	if timeoutStr == "" {
		timeoutStr = "10s"
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return nil, apperr.Newf(2, "invalid --timeout %q: %s", timeoutStr, err)
	}
	if timeout <= 0 {
		return nil, apperr.New(2, "--timeout must be greater than zero")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	insecure := p.Insecure || insecureFlag

	kcAcct := fmt.Sprintf("%s:%s:access-code", p.Driver, name)
	accessCode, err := deps.KC.Get(ctx, "polimero", kcAcct)
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return nil, apperr.Newf(3, "access code not found in keychain for %q", name)
		}
		return nil, apperr.Wrap(3, "cannot read access code from keychain", err)
	}

	var tlsFingerprint string
	if !insecure {
		kcFpAcct := fmt.Sprintf("%s:%s:tls-fingerprint", p.Driver, name)
		tlsFingerprint, err = deps.KC.Get(ctx, "polimero", kcFpAcct)
		if err != nil {
			if errors.Is(err, keychain.ErrNotFound) {
				return nil, apperr.Newf(3, "TLS fingerprint not found in keychain for %q", name)
			}
			return nil, apperr.Wrap(3, "cannot read TLS fingerprint from keychain", err)
		}
		if !driver.ValidTLSFingerprint(tlsFingerprint) {
			return nil, apperr.Newf(3, "invalid TLS fingerprint in keychain for %q", name)
		}
	}

	drv, ok := deps.GetDriver(p.Driver)
	if !ok {
		return nil, apperr.Newf(2, "unknown driver %q", p.Driver)
	}
	if !drv.Capabilities().CameraStream {
		return nil, apperr.Newf(5, "driver %q does not support camera streaming", p.Driver)
	}

	pi := driver.ProfileInput{
		Name:     name,
		Driver:   p.Driver,
		Host:     p.Host,
		Serial:   p.Serial,
		Timeout:  timeout,
		Insecure: insecure,
	}
	secrets := driver.SecretsBundle{
		AccessCode:     accessCode,
		TLSFingerprint: tlsFingerprint,
	}

	result, err := drv.CameraStream(ctx, pi, secrets, deps.Log)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func serve(cmd *cobra.Command, name string, port int, timeoutFlag string, format output.Format, result *driver.CameraStreamResult) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// Check port availability before starting the server.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		_ = result.Stream.Close()
		return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format,
			apperr.Newf(2, "port %d is already in use or unavailable", port))
	}

	url := fmt.Sprintf("http://%s/stream", addr)

	// Content-Type based on format.
	var contentType string
	switch result.Format {
	case driver.CameraFormatMJPEG:
		contentType = "multipart/x-mixed-replace; boundary=frame"
	case driver.CameraFormatH264:
		contentType = "video/mp2t"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 32*1024)
		for {
			n, readErr := result.Stream.Read(buf)
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

	srv := &http.Server{
		Handler:     mux,
		ReadTimeout: 5 * time.Second,
	}

	// Write output once server is ready.
	if format == output.FormatJSON {
		_ = writeJSONSuccess(cmd.OutOrStdout(), name, url, string(result.Format), port)
	} else {
		writeHumanStart(cmd.OutOrStdout(), name, result.Format, url)
	}

	// Set up signal handling and optional timeout.
	sigCtx, sigStop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer sigStop()

	var timeoutCh <-chan time.Time
	if timeoutFlag != "" {
		d, _ := time.ParseDuration(timeoutFlag) // already validated
		timeoutCh = time.After(d)
	}

	// Start server in background.
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.Serve(ln)
	}()

	// Wait for signal, timeout, or server error.
	select {
	case <-sigCtx.Done():
		// Clean exit on signal.
	case <-timeoutCh:
		// Clean exit on timeout.
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

func writeJSONSuccess(w io.Writer, name, url, format string, port int) error {
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
		Meta:  output.Meta{Command: "camera stream"},
	})
}

func writeUsageError(cmd *cobra.Command, message string) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}
	return writeError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, apperr.New(2, message))
}

func writeError(out, errOut io.Writer, format output.Format, err error) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	errDetail := buildErrorDetail(err)
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &errDetail,
			Meta:  output.Meta{Command: "camera stream"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", errDetail.Message)
	}
	return apperr.New(code, "")
}

func buildErrorDetail(err error) output.ErrDetail {
	return output.ErrDetail{Code: errorCode(err), Message: err.Error()}
}

func errorCode(err error) string {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		return "error"
	}
	switch exitErr.Code {
	case 2:
		return "config_error"
	case 3:
		msg := err.Error()
		if strings.Contains(msg, "TLS fingerprint mismatch") {
			return "authentication_failed"
		}
		return "secret_not_found"
	case 4:
		return "connection_failed"
	case 5:
		return "capability_unsupported"
	default:
		return "error"
	}
}
