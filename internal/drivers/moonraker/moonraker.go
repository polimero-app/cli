package moonraker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/protocoltrace"
)

const (
	driverName  = "moonraker"
	defaultPort = "7125"
)

// Driver implements the Moonraker HTTP API.
type Driver struct {
	client *http.Client
}

// New returns a moonraker driver.
func New() *Driver {
	return &Driver{client: &http.Client{
		// Redirects are refused: following one could resend X-Api-Key to a
		// different host or silently downgrade https to http.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (d *Driver) Name() string { return driverName }

func (d *Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{
		Status:           true,
		FileList:         true,
		FileDownload:     true,
		FileUpload:       true,
		JobStart:         true,
		JobPause:         true,
		JobResume:        true,
		JobCancel:        true,
		TemperatureWrite: true,
		MotionControl:    true,
	}
}

func (d *Driver) ValidateProfile(_ driver.ProfileInput) error { return nil }

func (d *Driver) ConnectCheck(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle) (string, error) {
	if err := d.requestJSON(ctx, p, s, "ConnectCheck", http.MethodGet, "/server/info", nil, nil); err != nil {
		return "", err
	}
	return "", nil
}

func (d *Driver) CaptureFingerprint(_ context.Context, _ driver.ProfileInput) (string, error) {
	return "", apperr.Newf(5, "driver %q does not support TLS fingerprint capture", driverName)
}

func (d *Driver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return nil, apperr.Newf(5, "driver %q does not support discovery", driverName)
}

func (d *Driver) CameraStream(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraStreamResult, error) {
	return nil, apperr.Newf(5, "driver %q does not support camera stream", driverName)
}

func (d *Driver) CameraSnapshot(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraSnapshotResult, error) {
	return nil, apperr.Newf(5, "driver %q does not support camera snapshot", driverName)
}

func (d *Driver) Status(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	var payload struct {
		Status map[string]any `json:"status"`
	}
	q := url.Values{}
	q.Add("webhooks", "")
	q.Add("print_stats", "")
	q.Add("virtual_sdcard", "")
	q.Add("extruder", "")
	q.Add("heater_bed", "")
	if err := d.requestJSON(
		ctx,
		p,
		s,
		"Status",
		http.MethodGet,
		"/printer/objects/query",
		q,
		&payload,
	); err != nil {
		return nil, err
	}

	state := mapState(payload.Status)
	temps := mapTemperatures(payload.Status)
	job := mapJob(payload.Status)
	progress := mapProgress(payload.Status)

	return &driver.StatusResult{
		State:        state,
		Temperatures: temps,
		Job:          job,
		Progress:     progress,
		Errors:       []driver.StatusError{},
		Warnings:     []driver.StatusWarning{},
		Capabilities: d.Capabilities(),
	}, nil
}

func (d *Driver) FileRoots(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) ([]driver.FileRoot, error) {
	return []driver.FileRoot{
		{
			Name:        "gcodes",
			Description: "Moonraker gcode storage",
			Writable:    true,
		},
	}, nil
}

func (d *Driver) FileList(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, root, devicePath string, recursive bool, _ *slog.Logger) (*driver.FileListResult, error) {
	if root != "gcodes" {
		return nil, apperr.Newf(2, "unknown root %q", root)
	}

	entries := make([]driver.FileEntry, 0)

	if recursive {
		q := url.Values{}
		q.Set("root", "gcodes")
		var files []map[string]any
		if err := d.requestJSON(ctx, p, s, "FileList", http.MethodGet, "/server/files/list", q, &files); err != nil {
			return nil, err
		}
		for _, raw := range files {
			entry := fileEntryFromListItem(raw)
			if entry == nil {
				continue
			}
			entries = append(entries, *entry)
		}
		return &driver.FileListResult{Entries: entries}, nil
	}

	q := url.Values{}
	q.Set("path", toMoonrakerPath(devicePath))
	var payload struct {
		Dirs  []map[string]any `json:"dirs"`
		Files []map[string]any `json:"files"`
	}
	if err := d.requestJSON(ctx, p, s, "FileList", http.MethodGet, "/server/files/directory", q, &payload); err != nil {
		return nil, err
	}
	for _, raw := range payload.Dirs {
		if entry := fileEntryFromDirectoryItem(raw, true); entry != nil {
			entries = append(entries, *entry)
		}
	}
	for _, raw := range payload.Files {
		if entry := fileEntryFromDirectoryItem(raw, false); entry != nil {
			entries = append(entries, *entry)
		}
	}
	return &driver.FileListResult{Entries: entries}, nil
}

func (d *Driver) FileDownload(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, root, devicePath string, dst io.Writer, _ *slog.Logger) (*driver.FileTransferResult, error) {
	if root != "gcodes" {
		return nil, apperr.Newf(2, "unknown root %q", root)
	}
	rel := strings.TrimPrefix(devicePath, "/")
	endpoint := "/server/files/gcodes/" + escapePathSegments(rel)
	req, err := d.newRequest(ctx, p, s, http.MethodGet, endpoint, nil, nil)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	resp, err := d.client.Do(req)
	if err != nil {
		d.emitHTTPTrace(ctx, "FileDownload", req, start, nil, 0, classifyTraceError(err))
		return nil, classifyHTTPError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		d.emitHTTPTrace(ctx, "FileDownload", req, start, nil, resp.StatusCode, "auth_rejected")
		return nil, apperr.New(3, "Moonraker authentication rejected")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		d.emitHTTPTrace(ctx, "FileDownload", req, start, nil, resp.StatusCode, "protocol_error")
		return nil, apperr.Newf(1, "moonraker request failed with status %d", resp.StatusCode)
	}
	n, err := io.Copy(dst, resp.Body)
	if err != nil {
		d.emitHTTPTrace(ctx, "FileDownload", req, start, nil, resp.StatusCode, classifyTraceError(err))
		return nil, classifyHTTPError(err)
	}
	d.emitHTTPTrace(ctx, "FileDownload", req, start, &n, resp.StatusCode, "")
	return &driver.FileTransferResult{BytesTransferred: &n}, nil
}

func (d *Driver) FileUpload(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, root, devicePath string, src io.Reader, _ int64, _ bool, _ *slog.Logger) (*driver.FileTransferResult, error) {
	if root != "gcodes" {
		return nil, apperr.Newf(2, "unknown root %q", root)
	}
	if src == nil {
		return nil, apperr.New(2, "upload source is required")
	}

	parent := path.Dir(devicePath)
	if parent == "." {
		parent = "/"
	}
	filename := path.Base(devicePath)
	if filename == "." || filename == "/" || filename == "" {
		return nil, apperr.New(2, "upload destination must include a file name")
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("root", "gcodes"); err != nil {
		return nil, apperr.Newf(1, "cannot encode upload form: %s", err)
	}
	if err := writer.WriteField("path", strings.TrimPrefix(parent, "/")); err != nil {
		return nil, apperr.Newf(1, "cannot encode upload form: %s", err)
	}
	filePart, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, apperr.Newf(1, "cannot encode upload form: %s", err)
	}
	n, err := io.Copy(filePart, src)
	if err != nil {
		return nil, apperr.Newf(1, "cannot read upload source: %s", err)
	}
	if err := writer.Close(); err != nil {
		return nil, apperr.Newf(1, "cannot finalize upload form: %s", err)
	}

	req, err := d.newRequest(ctx, p, s, http.MethodPost, "/server/files/upload", nil, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	start := time.Now()
	resp, err := d.client.Do(req)
	if err != nil {
		d.emitHTTPTrace(ctx, "FileUpload", req, start, nil, 0, classifyTraceError(err))
		return nil, classifyHTTPError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		d.emitHTTPTrace(ctx, "FileUpload", req, start, nil, resp.StatusCode, "auth_rejected")
		return nil, apperr.New(3, "Moonraker authentication rejected")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		d.emitHTTPTrace(ctx, "FileUpload", req, start, nil, resp.StatusCode, "protocol_error")
		return nil, apperr.Newf(1, "moonraker request failed with status %d", resp.StatusCode)
	}
	d.emitHTTPTrace(ctx, "FileUpload", req, start, &n, resp.StatusCode, "")
	return &driver.FileTransferResult{BytesTransferred: &n}, nil
}

func (d *Driver) JobStart(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, devicePath string, _ driver.JobStartOptions) (driver.JobActionResult, error) {
	root, rel, err := splitDevicePath(devicePath)
	if err != nil {
		return driver.JobActionResult{}, err
	}
	if root != "gcodes" {
		return driver.JobActionResult{}, apperr.Newf(2, "unsupported root %q for moonraker job start", root)
	}
	q := url.Values{}
	q.Set("filename", rel)
	if err := d.requestJSON(ctx, p, s, "JobStart", http.MethodPost, "/printer/print/start", q, nil); err != nil {
		return driver.JobActionResult{}, err
	}
	return d.waitForState(ctx, p, s, "printing")
}

func (d *Driver) JobPause(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger) (driver.JobActionResult, error) {
	if err := d.requestJSON(ctx, p, s, "JobPause", http.MethodPost, "/printer/print/pause", nil, nil); err != nil {
		return driver.JobActionResult{}, err
	}
	return d.waitForState(ctx, p, s, "paused")
}

func (d *Driver) JobResume(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger) (driver.JobActionResult, error) {
	if err := d.requestJSON(ctx, p, s, "JobResume", http.MethodPost, "/printer/print/resume", nil, nil); err != nil {
		return driver.JobActionResult{}, err
	}
	return d.waitForState(ctx, p, s, "printing")
}

func (d *Driver) JobCancel(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger) (driver.JobActionResult, error) {
	if err := d.requestJSON(ctx, p, s, "JobCancel", http.MethodPost, "/printer/print/cancel", nil, nil); err != nil {
		return driver.JobActionResult{}, err
	}
	return d.waitForState(ctx, p, s, "idle")
}

func (d *Driver) TemperatureSet(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, targets driver.TemperatureTargets) (driver.TemperatureResult, error) {
	lines := make([]string, 0, 3)
	if targets.NozzleCelsius != nil {
		lines = append(lines, fmt.Sprintf("M104 S%.0f", *targets.NozzleCelsius))
	}
	if targets.BedCelsius != nil {
		lines = append(lines, fmt.Sprintf("M140 S%.0f", *targets.BedCelsius))
	}
	if targets.ChamberCelsius != nil {
		lines = append(lines, fmt.Sprintf("M141 S%.0f", *targets.ChamberCelsius))
	}
	if len(lines) == 0 {
		return driver.TemperatureResult{}, apperr.New(2, "at least one target must be set")
	}
	if err := d.runGCodeScript(ctx, p, s, "TemperatureSet", strings.Join(lines, "\n")); err != nil {
		return driver.TemperatureResult{}, err
	}
	return driver.TemperatureResult{
		Targets:      targets,
		Warnings:     []driver.StatusWarning{},
		Capabilities: d.Capabilities(),
	}, nil
}

func (d *Driver) MotionHome(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, axes []driver.Axis) (driver.MotionResult, error) {
	gcode := "G28"
	if len(axes) > 0 {
		var b strings.Builder
		b.WriteString(gcode)
		for _, axis := range axes {
			switch axis {
			case driver.AxisX:
				b.WriteString(" X")
			case driver.AxisY:
				b.WriteString(" Y")
			case driver.AxisZ:
				b.WriteString(" Z")
			}
		}
		gcode = b.String()
	}
	if err := d.runGCodeScript(ctx, p, s, "MotionHome", gcode); err != nil {
		return driver.MotionResult{}, err
	}
	return driver.MotionResult{
		State:        driver.MotionStateAccepted,
		Warnings:     []driver.StatusWarning{},
		Capabilities: d.Capabilities(),
	}, nil
}

func (d *Driver) MotionJog(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, delta driver.JogDelta) (driver.MotionResult, error) {
	parts := make([]string, 0, 4)
	if delta.XMillimeters != nil {
		parts = append(parts, fmt.Sprintf("X%.3f", *delta.XMillimeters))
	}
	if delta.YMillimeters != nil {
		parts = append(parts, fmt.Sprintf("Y%.3f", *delta.YMillimeters))
	}
	if delta.ZMillimeters != nil {
		parts = append(parts, fmt.Sprintf("Z%.3f", *delta.ZMillimeters))
	}
	if len(parts) == 0 {
		return driver.MotionResult{}, apperr.New(2, "at least one axis delta is required")
	}
	if delta.FeedrateMmPerMin > 0 {
		parts = append(parts, fmt.Sprintf("F%d", delta.FeedrateMmPerMin))
	}
	script := "G91\nG1 " + strings.Join(parts, " ") + "\nG90"
	if err := d.runGCodeScript(ctx, p, s, "MotionJog", script); err != nil {
		return driver.MotionResult{}, err
	}
	return driver.MotionResult{
		State:        driver.MotionStateAccepted,
		Warnings:     []driver.StatusWarning{},
		Capabilities: d.Capabilities(),
	}, nil
}

func (d *Driver) runGCodeScript(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, operation, script string) error {
	q := url.Values{}
	q.Set("script", script)
	return d.requestJSON(ctx, p, s, operation, http.MethodPost, "/printer/gcode/script", q, nil)
}

func (d *Driver) waitForState(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, expected string) (driver.JobActionResult, error) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return driver.JobActionResult{}, classifyHTTPError(ctx.Err())
		case <-ticker.C:
			st, err := d.Status(ctx, p, s, nil)
			if err != nil {
				return driver.JobActionResult{}, err
			}
			if st != nil && st.State == expected {
				return driver.JobActionResult{
					State:        expected,
					Warnings:     []driver.StatusWarning{},
					Capabilities: d.Capabilities(),
				}, nil
			}
		}
	}
}

func (d *Driver) requestJSON(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, operation, method, endpoint string, query url.Values, out any) error {
	req, err := d.newRequest(ctx, p, s, method, endpoint, query, nil)
	if err != nil {
		return err
	}
	start := time.Now()
	resp, err := d.client.Do(req)
	if err != nil {
		d.emitHTTPTrace(ctx, operation, req, start, nil, 0, classifyTraceError(err))
		return classifyHTTPError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		d.emitHTTPTrace(ctx, operation, req, start, nil, resp.StatusCode, "auth_rejected")
		return apperr.New(3, "Moonraker authentication rejected")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		d.emitHTTPTrace(ctx, operation, req, start, nil, resp.StatusCode, "protocol_error")
		return apperr.Newf(1, "moonraker request failed with status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		d.emitHTTPTrace(ctx, operation, req, start, nil, resp.StatusCode, classifyTraceError(err))
		return classifyHTTPError(err)
	}
	bc := int64(len(body))

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		d.emitHTTPTrace(ctx, operation, req, start, &bc, resp.StatusCode, "parse_error")
		return apperr.New(1, "invalid response from printer")
	}
	if envelope.Error != nil {
		msg := strings.ToLower(envelope.Error.Message)
		if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "forbidden") {
			d.emitHTTPTrace(ctx, operation, req, start, &bc, resp.StatusCode, "auth_rejected")
			return apperr.New(3, "Moonraker authentication rejected")
		}
		d.emitHTTPTrace(ctx, operation, req, start, &bc, resp.StatusCode, "protocol_error")
		return apperr.New(1, "moonraker API returned an error")
	}
	if out == nil {
		d.emitHTTPTrace(ctx, operation, req, start, &bc, resp.StatusCode, "")
		return nil
	}
	if len(envelope.Result) == 0 {
		d.emitHTTPTrace(ctx, operation, req, start, &bc, resp.StatusCode, "parse_error")
		return apperr.New(1, "moonraker response missing result")
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		d.emitHTTPTrace(ctx, operation, req, start, &bc, resp.StatusCode, "parse_error")
		return apperr.New(1, "invalid response payload from printer")
	}
	d.emitHTTPTrace(ctx, operation, req, start, &bc, resp.StatusCode, "")
	return nil
}

func (d *Driver) emitHTTPTrace(
	ctx context.Context,
	operation string,
	req *http.Request,
	start time.Time,
	byteCount *int64,
	statusCode int,
	errorCategory string,
) {
	trace := protocoltrace.FromContext(ctx)
	dur := time.Since(start).Milliseconds()
	detail := map[string]any{
		"method": req.Method,
		"path":   req.URL.Path,
	}
	if statusCode > 0 {
		detail["status"] = statusCode
	}
	ev := protocoltrace.Event{
		Timestamp:     time.Now().UTC(),
		Driver:        driverName,
		Operation:     operation,
		Phase:         "request",
		Transport:     "http",
		Endpoint:      req.URL.Host,
		DurationMs:    &dur,
		ByteCount:     byteCount,
		ErrorCategory: errorCategory,
		Detail:        detail,
	}
	trace.Emit(ev)
}

func classifyTraceError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	return "connection_error"
}

func (d *Driver) newRequest(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, method, endpoint string, query url.Values, body io.Reader) (*http.Request, error) {
	base, err := normalizeBaseURL(p.Host)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, apperr.Newf(2, "invalid host: %s", err)
	}
	u.Path = path.Join(u.Path, endpoint)
	if query != nil {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, apperr.Newf(1, "cannot create request: %s", err)
	}
	req.Header.Set("Accept", "application/json")
	if s.AccessCode != "" {
		req.Header.Set("X-Api-Key", s.AccessCode)
	}
	return req, nil
}

func normalizeBaseURL(host string) (string, error) {
	raw := strings.TrimSpace(host)
	if raw == "" {
		return "", apperr.New(2, "--host is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", apperr.Newf(2, "invalid --host %q: %s", host, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", apperr.Newf(2, "invalid --host %q: scheme must be http or https", host)
	}
	if u.Host == "" {
		return "", apperr.Newf(2, "invalid --host %q", host)
	}
	if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), defaultPort)
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

func classifyHTTPError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return apperr.New(4, "request cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return apperr.New(4, "request timed out")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return apperr.New(4, "request timed out")
	}
	return apperr.New(4, "connection failed")
}

func mapState(status map[string]any) string {
	if status == nil {
		return "unknown"
	}
	if webhooks, ok := asMap(status["webhooks"]); ok {
		if wstate, ok := asString(webhooks["state"]); ok {
			ls := strings.ToLower(wstate)
			if ls == "shutdown" || ls == "error" {
				return "error"
			}
		}
	}
	if ps, ok := asMap(status["print_stats"]); ok {
		if st, ok := asString(ps["state"]); ok {
			switch strings.ToLower(st) {
			case "printing":
				return "printing"
			case "paused":
				return "paused"
			case "error", "shutdown":
				return "error"
			case "complete", "standby", "ready", "cancelled", "canceled":
				return "idle"
			default:
				return "unknown"
			}
		}
	}
	return "unknown"
}

func mapTemperatures(status map[string]any) *driver.Temperatures {
	var t driver.Temperatures
	var has bool
	if ex, ok := asMap(status["extruder"]); ok {
		cur, hasCur := asFloat(ex["temperature"])
		tgt, hasTgt := asFloat(ex["target"])
		if hasCur || hasTgt {
			has = true
			temp := &driver.Temperature{CurrentCelsius: cur}
			if hasTgt {
				temp.TargetCelsius = &tgt
			}
			t.Nozzle = temp
		}
	}
	if bed, ok := asMap(status["heater_bed"]); ok {
		cur, hasCur := asFloat(bed["temperature"])
		tgt, hasTgt := asFloat(bed["target"])
		if hasCur || hasTgt {
			has = true
			temp := &driver.Temperature{CurrentCelsius: cur}
			if hasTgt {
				temp.TargetCelsius = &tgt
			}
			t.Bed = temp
		}
	}
	if !has {
		return nil
	}
	return &t
}

func mapJob(status map[string]any) *driver.Job {
	ps, ok := asMap(status["print_stats"])
	if !ok {
		return nil
	}
	filename, ok := asString(ps["filename"])
	if !ok || filename == "" {
		return nil
	}
	return &driver.Job{Name: filename}
}

func mapProgress(status map[string]any) *driver.Progress {
	vsd, ok := asMap(status["virtual_sdcard"])
	if !ok {
		return nil
	}
	progressRaw, ok := asFloat(vsd["progress"])
	if !ok {
		return nil
	}
	percent := int(progressRaw * 100)
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return &driver.Progress{Percent: percent}
}

func toMoonrakerPath(devicePath string) string {
	if devicePath == "/" {
		return "gcodes"
	}
	return "gcodes/" + strings.TrimPrefix(devicePath, "/")
}

func splitDevicePath(full string) (root string, relative string, err error) {
	parts := strings.SplitN(full, ":", 2)
	if len(parts) != 2 {
		return "", "", apperr.Newf(2, "invalid device path %q", full)
	}
	root = parts[0]
	relative = strings.TrimPrefix(parts[1], "/")
	if relative == "" {
		return "", "", apperr.Newf(2, "invalid device path %q", full)
	}
	return root, relative, nil
}

func fileEntryFromListItem(raw map[string]any) *driver.FileEntry {
	p, ok := asString(raw["path"])
	if !ok || p == "" {
		return nil
	}
	devicePath := "/" + strings.TrimPrefix(strings.TrimPrefix(p, "gcodes"), "/")
	typ := driver.FileEntryTypeFile
	if dirFlag, ok := asBool(raw["dirname"]); ok && dirFlag {
		typ = driver.FileEntryTypeDirectory
	}
	entry := &driver.FileEntry{
		Name:       path.Base(devicePath),
		Root:       "gcodes",
		Path:       devicePath,
		DevicePath: "gcodes:" + devicePath,
		Type:       typ,
		Metadata:   map[string]any{},
	}
	if sz, ok := asInt64(raw["size"]); ok {
		entry.SizeBytes = &sz
	}
	if mod, ok := asUnixTimeString(raw["modified"]); ok {
		entry.ModifiedAt = &mod
	}
	return entry
}

func fileEntryFromDirectoryItem(raw map[string]any, isDir bool) *driver.FileEntry {
	p, ok := asString(raw["path"])
	if !ok || p == "" {
		if rel, ok := asString(raw["dirname"]); ok && rel != "" {
			p = rel
		}
	}
	if p == "" {
		return nil
	}
	devicePath := "/" + strings.TrimPrefix(strings.TrimPrefix(p, "gcodes"), "/")
	entryType := driver.FileEntryTypeFile
	if isDir {
		entryType = driver.FileEntryTypeDirectory
	}
	entry := &driver.FileEntry{
		Name:       path.Base(devicePath),
		Root:       "gcodes",
		Path:       devicePath,
		DevicePath: "gcodes:" + devicePath,
		Type:       entryType,
		Metadata:   map[string]any{},
	}
	if sz, ok := asInt64(raw["size"]); ok {
		entry.SizeBytes = &sz
	}
	if mod, ok := asUnixTimeString(raw["modified"]); ok {
		entry.ModifiedAt = &mod
	}
	return entry
}

func escapePathSegments(rel string) string {
	parts := strings.Split(rel, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case json.Number:
		return t.String(), true
	}
	return "", false
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		if err == nil {
			return f, true
		}
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

func asInt64(v any) (int64, bool) {
	f, ok := asFloat(v)
	if !ok {
		return 0, false
	}
	return int64(f), true
}

func asBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch strings.ToLower(t) {
		case "true", "1":
			return true, true
		case "false", "0":
			return false, true
		}
	}
	return false, false
}

func asUnixTimeString(v any) (string, bool) {
	f, ok := asFloat(v)
	if !ok || f <= 0 {
		return "", false
	}
	t := time.Unix(int64(f), 0).UTC()
	s := t.Format(time.RFC3339)
	return s, true
}
