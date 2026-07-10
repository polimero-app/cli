package bambulan

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/protocoltrace"
)

const (
	ftpPort         = 990
	ftpUser         = "bblp"
	ftpRootName     = "sdcard"
	ftpRootDesc     = "SD card"
	ftpPassiveStart = 50000
	ftpPassiveEnd   = 50100
)

// ftpConn is the subset of *ftp.ServerConn used by this driver.
type ftpConn interface {
	Login(user, password string) error
	List(path string) ([]*ftp.Entry, error)
	FileSize(path string) (int64, error)
	Retr(path string) (io.ReadCloser, error)
	Stor(path string, r io.Reader) error
	Quit() error
}

// ftpConnAdapter wraps *ftp.ServerConn to satisfy the ftpConn interface.
type ftpConnAdapter struct {
	conn *ftp.ServerConn
}

func (a *ftpConnAdapter) Login(user, password string) error       { return a.conn.Login(user, password) }
func (a *ftpConnAdapter) List(path string) ([]*ftp.Entry, error)  { return a.conn.List(path) }
func (a *ftpConnAdapter) FileSize(path string) (int64, error)     { return a.conn.FileSize(path) }
func (a *ftpConnAdapter) Retr(path string) (io.ReadCloser, error) { return a.conn.Retr(path) }
func (a *ftpConnAdapter) Stor(path string, r io.Reader) error     { return a.conn.Stor(path, r) }
func (a *ftpConnAdapter) Quit() error                             { return a.conn.Quit() }

// ftpDialer creates FTP connections. Injected for testing.
type ftpDialer func(ctx context.Context, addr string, tlsCfg *tls.Config) (ftpConn, error)

// realFTPDial creates a real FTPS connection to a Bambu printer.
func realFTPDial(ctx context.Context, addr string, tlsCfg *tls.Config) (ftpConn, error) {
	timeout := 10 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
		if timeout <= 0 {
			timeout = 1 * time.Second
		}
	}

	dialer := &net.Dialer{Timeout: timeout}
	dialTLS := func(network, address string) (net.Conn, error) {
		raw, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		conn := newContextBoundConn(ctx, tls.Client(raw, tlsCfg))
		if deadline, ok := ctx.Deadline(); ok {
			if err := conn.SetDeadline(deadline); err != nil {
				_ = conn.Close()
				return nil, err
			}
		}
		return conn, nil
	}

	conn, err := ftp.Dial(addr,
		ftp.DialWithDialFunc(dialTLS),
		ftp.DialWithTLS(tlsCfg),
		ftp.DialWithShutTimeout(timeout),
	)
	if err != nil {
		return nil, err
	}
	return &ftpConnAdapter{conn: conn}, nil
}

// contextBoundConn closes an FTP control or data connection when ctx is
// canceled. Closing the socket is required to interrupt reads and writes that
// are already blocked inside the FTP library.
type contextBoundConn struct {
	net.Conn
	done     chan struct{}
	closeErr error
	once     sync.Once
}

func newContextBoundConn(ctx context.Context, conn net.Conn) *contextBoundConn {
	bound := &contextBoundConn{Conn: conn, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = bound.SetDeadline(time.Now())
			_ = bound.Close()
		case <-bound.done:
		}
	}()
	return bound
}

func (c *contextBoundConn) Close() error {
	c.once.Do(func() {
		close(c.done)
		c.closeErr = c.Conn.Close()
	})
	return c.closeErr
}

// Handshake preserves the optional TLS handshake method used by the FTP
// library for zero-byte uploads.
func (c *contextBoundConn) Handshake() error {
	handshaker, ok := c.Conn.(interface{ Handshake() error })
	if !ok {
		return nil
	}
	return handshaker.Handshake()
}

// ftpDial is the driver's FTP dialer, overridable in tests.
func (d *Driver) ftpDial() ftpDialer {
	if d.dialFTP != nil {
		return d.dialFTP
	}
	return realFTPDial
}

// connectFTP establishes an authenticated FTPS connection.
func (d *Driver) connectFTP(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, log *slog.Logger) (ftpConn, error) {
	trace := protocoltrace.FromContext(ctx)
	endpoint := fmt.Sprintf("%s:%d", p.Host, ftpPort)

	tlsCfg, err := buildFTPTLSConfig(p.Serial, s.TLSFingerprint, p.Insecure)
	if err != nil {
		return nil, err
	}

	addr := fmt.Sprintf("%s:%d", p.Host, ftpPort)
	log.Debug("connecting to FTPS", "addr", addr, "insecure", p.Insecure)

	connectStart := time.Now()
	conn, err := d.ftpDial()(ctx, addr, tlsCfg)
	if err != nil {
		dur := time.Since(connectStart).Milliseconds()
		category := "connection_error"
		if isFingerprintMismatch(err) {
			category = "auth_rejected"
		}
		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     "FTP",
			Phase:         "connect",
			Transport:     "ftps",
			Endpoint:      endpoint,
			DurationMs:    &dur,
			ErrorCategory: category,
		})
		log.Debug("FTPS connection failed", "err", err.Error())
		if isFingerprintMismatch(err) {
			return nil, apperr.Wrap(3, "TLS fingerprint mismatch", err)
		}
		return nil, apperr.Newf(4, "FTP connection failed: %s", sanitizeFTPError(err))
	}
	connectDur := time.Since(connectStart).Milliseconds()
	trace.Emit(protocoltrace.Event{
		Timestamp:  time.Now().UTC(),
		Driver:     "bambu-lan",
		Operation:  "FTP",
		Phase:      "connect",
		Transport:  "ftps",
		Endpoint:   endpoint,
		DurationMs: &connectDur,
	})

	log.Debug("FTPS connected, authenticating")
	authStart := time.Now()
	if err := conn.Login(ftpUser, s.AccessCode); err != nil {
		dur := time.Since(authStart).Milliseconds()
		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     "FTP",
			Phase:         "authenticate",
			Transport:     "ftps",
			Endpoint:      endpoint,
			DurationMs:    &dur,
			ErrorCategory: "auth_rejected",
		})
		_ = conn.Quit()
		log.Debug("FTPS authentication failed")
		return nil, apperr.Newf(3, "FTP authentication failed")
	}
	authDur := time.Since(authStart).Milliseconds()
	trace.Emit(protocoltrace.Event{
		Timestamp:  time.Now().UTC(),
		Driver:     "bambu-lan",
		Operation:  "FTP",
		Phase:      "authenticate",
		Transport:  "ftps",
		Endpoint:   endpoint,
		DurationMs: &authDur,
	})

	log.Debug("FTPS authenticated successfully")
	return conn, nil
}

// buildFTPTLSConfig creates a TLS config for FTPS with fingerprint verification.
// A ClientSessionCache is required because Bambu printers (especially H2 family)
// enforce TLS session reuse on data connections (RFC 4217).
func buildFTPTLSConfig(serial, fingerprint string, insecure bool) (*tls.Config, error) {
	cfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Bambu CA not in OS trust stores; leaf cert pinned by TOFU (ADR 0007)
		ServerName:         serial,
		ClientSessionCache: tls.NewLRUClientSessionCache(1),
	}

	if insecure {
		return cfg, nil
	}

	if !driver.ValidTLSFingerprint(fingerprint) {
		return nil, apperr.New(3, "TLS fingerprint is missing or invalid")
	}

	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return apperr.New(3, "no peer certificate presented")
		}
		leaf := cs.PeerCertificates[0]
		hash := sha256.Sum256(leaf.Raw)
		got := "sha256:" + hex.EncodeToString(hash[:])
		if got != fingerprint {
			return &fingerprintMismatchError{got: got, want: fingerprint}
		}
		return nil
	}

	return cfg, nil
}

// FileRoots returns the storage roots available on the Bambu printer.
func (d *Driver) FileRoots(
	ctx context.Context,
	p driver.ProfileInput,
	s driver.SecretsBundle,
	log *slog.Logger,
) ([]driver.FileRoot, error) {
	conn, err := d.connectFTP(ctx, p, s, log)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Quit() }()

	roots := []driver.FileRoot{
		{
			Name:          ftpRootName,
			Description:   ftpRootDesc,
			Writable:      true,
			CapacityBytes: nil,
			FreeBytes:     nil,
			Metadata:      map[string]any{},
		},
	}

	return roots, nil
}

// FileList lists files at the given root and path on the Bambu printer.
func (d *Driver) FileList(
	ctx context.Context,
	p driver.ProfileInput,
	s driver.SecretsBundle,
	root string,
	remotePath string,
	recursive bool,
	log *slog.Logger,
) (*driver.FileListResult, error) {
	if root != ftpRootName {
		return nil, apperr.Newf(2, "unknown root %q", root)
	}

	conn, err := d.connectFTP(ctx, p, s, log)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Quit() }()

	if recursive {
		return d.listRecursive(ctx, conn, root, remotePath, log)
	}

	entries, err := d.listDir(conn, root, remotePath, log)
	if err != nil {
		return nil, err
	}

	return &driver.FileListResult{Entries: entries}, nil
}

func (d *Driver) listDir(conn ftpConn, root, remotePath string, log *slog.Logger) ([]driver.FileEntry, error) {
	ftpPath, err := mapToFTPPath(remotePath)
	if err != nil {
		return nil, err
	}
	log.Debug("listing FTP directory", "path", ftpPath)

	rawEntries, err := conn.List(ftpPath)
	if err != nil {
		return nil, mapFTPError(err, remotePath)
	}

	entries := make([]driver.FileEntry, 0, len(rawEntries))
	for _, e := range rawEntries {
		if e.Name == "." || e.Name == ".." {
			continue
		}
		entry := mapFTPEntry(e, root, remotePath)
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	return entries, nil
}

func (d *Driver) listRecursive(ctx context.Context, conn ftpConn, root, remotePath string, log *slog.Logger) (*driver.FileListResult, error) {
	var allEntries []driver.FileEntry

	type queueItem struct {
		path string
	}
	queue := []queueItem{{path: remotePath}}

	for len(queue) > 0 {
		select {
		case <-ctx.Done():
			return nil, apperr.Newf(4, "directory listing cancelled")
		default:
		}

		item := queue[0]
		queue = queue[1:]

		entries, err := d.listDir(conn, root, item.path, log)
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			allEntries = append(allEntries, entry)
			if entry.Type == driver.FileEntryTypeDirectory {
				queue = append(queue, queueItem{path: entry.Path})
			}
		}
	}

	sort.Slice(allEntries, func(i, j int) bool {
		return allEntries[i].DevicePath < allEntries[j].DevicePath
	})

	return &driver.FileListResult{Entries: allEntries}, nil
}

// FileDownload downloads a file from the Bambu printer's storage.
func (d *Driver) FileDownload(
	ctx context.Context,
	p driver.ProfileInput,
	s driver.SecretsBundle,
	root string,
	remotePath string,
	dst io.Writer,
	log *slog.Logger,
) (*driver.FileTransferResult, error) {
	if root != ftpRootName {
		return nil, apperr.Newf(2, "unknown root %q", root)
	}

	conn, err := d.connectFTP(ctx, p, s, log)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Quit() }()

	ftpPath, err := mapToFTPPath(remotePath)
	if err != nil {
		return nil, err
	}
	log.Debug("downloading file via FTP", "path", ftpPath)

	resp, err := conn.Retr(ftpPath)
	if err != nil {
		return nil, mapFTPError(err, remotePath)
	}
	defer func() { _ = resp.Close() }()

	n, err := io.Copy(dst, &contextReader{ctx: ctx, r: resp})
	if err != nil {
		if ctx.Err() != nil {
			return nil, apperr.Newf(4, "download cancelled")
		}
		return nil, apperr.Newf(4, "download stream interrupted")
	}

	return &driver.FileTransferResult{BytesTransferred: &n}, nil
}

// FileUpload uploads a file to the Bambu printer's storage.
func (d *Driver) FileUpload(
	ctx context.Context,
	p driver.ProfileInput,
	s driver.SecretsBundle,
	root string,
	remotePath string,
	src io.Reader,
	size int64,
	overwrite bool,
	log *slog.Logger,
) (*driver.FileTransferResult, error) {
	if root != ftpRootName {
		return nil, apperr.Newf(2, "unknown root %q", root)
	}

	conn, err := d.connectFTP(ctx, p, s, log)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Quit() }()

	ftpPath, err := mapToFTPPath(remotePath)
	if err != nil {
		return nil, err
	}

	// Check existence when overwrite is false. The driver spec requires
	// failing closed when existence cannot be determined safely.
	if !overwrite {
		exists, checkErr := remoteFileExists(conn, ftpPath)
		if checkErr != nil {
			log.Debug("upload existence check failed", "path", ftpPath, "err", checkErr)
			return nil, apperr.Newf(1, "could not determine whether destination exists: %s:%s", root, remotePath)
		}
		if exists {
			return nil, apperr.Newf(2, "device path already exists: %s:%s", root, remotePath)
		}
	}

	log.Debug("uploading file via FTP", "path", ftpPath)

	// Wrap reader to count bytes and check context cancellation.
	cr := &countingReader{r: &contextReader{ctx: ctx, r: src}}
	if err := conn.Stor(ftpPath, cr); err != nil {
		if ctx.Err() != nil {
			return nil, apperr.Newf(4, "upload cancelled")
		}
		return nil, mapFTPError(err, remotePath)
	}

	n := cr.n
	return &driver.FileTransferResult{BytesTransferred: &n}, nil
}

// remoteFileExists reports whether ftpPath refers to an existing file.
// A LIST of the exact path is tried first; servers that reject listing a
// file path (for example when the library issues MLSD, RFC 3659) fall back
// to a SIZE probe. The error is non-nil only when existence could not be
// determined at all, in which case callers must fail closed.
func remoteFileExists(conn ftpConn, ftpPath string) (bool, error) {
	entries, listErr := conn.List(ftpPath)
	if listErr == nil {
		for _, e := range entries {
			if e.Name == path.Base(ftpPath) {
				return true, nil
			}
		}
		return false, nil
	}

	if _, sizeErr := conn.FileSize(ftpPath); sizeErr != nil {
		if isFTPNotFound(sizeErr) {
			return false, nil
		}
		return false, sizeErr
	}
	return true, nil
}

// isFTPNotFound reports whether an FTP error indicates a missing path.
func isFTPNotFound(err error) bool {
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "550") ||
		strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "not found")
}

// countingReader wraps an io.Reader and counts bytes read.
type countingReader struct {
	r io.Reader
	n int64
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.n += int64(n)
	return n, err
}

// contextReader wraps an io.Reader and checks for context cancellation
// before each read, allowing long transfers to be interrupted.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (cr *contextReader) Read(p []byte) (int, error) {
	select {
	case <-cr.ctx.Done():
		return 0, cr.ctx.Err()
	default:
		return cr.r.Read(p)
	}
}

// mapToFTPPath converts a normalized device path to an FTP path.
// sdcard:/ -> /   sdcard:/models/cube.3mf -> /models/cube.3mf
// It defensively re-validates the path per the driver spec: the command layer
// rejects traversal before dispatch, but the driver must still reject `..`
// segments, NUL bytes, and control characters.
func mapToFTPPath(devicePath string) (string, error) {
	// Device path is already the path portion (e.g. "/" or "/models/cube.3mf")
	if devicePath == "" || devicePath == "/" {
		return "/", nil
	}
	for _, r := range devicePath {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return "", apperr.Newf(2, "invalid device path: contains control characters")
		}
	}
	for _, seg := range strings.Split(strings.Trim(devicePath, "/"), "/") {
		if seg == ".." {
			return "", apperr.Newf(2, "invalid device path: contains traversal segment")
		}
	}
	return devicePath, nil
}

// mapFTPEntry converts an FTP entry to a driver.FileEntry.
func mapFTPEntry(e *ftp.Entry, root, parentPath string) driver.FileEntry {
	entryPath := parentPath
	if entryPath == "/" {
		entryPath = "/" + e.Name
	} else {
		entryPath = strings.TrimSuffix(entryPath, "/") + "/" + e.Name
	}

	var entryType driver.FileEntryType
	switch e.Type {
	case ftp.EntryTypeFile:
		entryType = driver.FileEntryTypeFile
	case ftp.EntryTypeFolder:
		entryType = driver.FileEntryTypeDirectory
	default:
		entryType = driver.FileEntryTypeUnknown
	}

	var sizeBytes *int64
	if entryType == driver.FileEntryTypeFile && e.Size > 0 {
		s := int64(e.Size)
		sizeBytes = &s
	}

	var modifiedAt *string
	if !e.Time.IsZero() {
		t := e.Time.UTC().Format(time.RFC3339)
		modifiedAt = &t
	}

	return driver.FileEntry{
		Name:       e.Name,
		Root:       root,
		Path:       entryPath,
		DevicePath: root + ":" + entryPath,
		Type:       entryType,
		SizeBytes:  sizeBytes,
		ModifiedAt: modifiedAt,
		Metadata:   map[string]any{},
	}
}

// mapFTPError converts FTP errors to appropriate apperr codes.
func mapFTPError(err error, remotePath string) error {
	msg := err.Error()
	lower := strings.ToLower(msg)

	switch {
	case strings.Contains(lower, "550"):
		return apperr.Newf(2, "device path not found: %s", remotePath)
	case strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "not found"):
		return apperr.Newf(2, "device path not found: %s", remotePath)
	case strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "deadline") ||
		strings.Contains(lower, "i/o timeout"):
		return apperr.Newf(4, "FTP operation timed out")
	case strings.Contains(lower, "refused"):
		return apperr.Newf(4, "FTP data connection refused")
	case strings.Contains(lower, "reset"):
		return apperr.Newf(4, "FTP data connection reset")
	case strings.Contains(lower, "pasv") || strings.Contains(lower, "epsv"):
		return apperr.Newf(4, "FTP passive mode negotiation failed")
	default:
		// Include sanitized detail for unexpected errors to aid debugging.
		sanitized := sanitizeProtocolError(msg)
		return apperr.Newf(4, "FTP operation failed: %s", sanitized)
	}
}

// sanitizeProtocolError removes credentials and protocol secrets from an error,
// keeping the structural information useful for debugging.
func sanitizeProtocolError(msg string) string {
	lower := strings.ToLower(msg)
	// Strip anything that looks like it might contain credentials.
	if strings.Contains(lower, "pass") || strings.Contains(lower, "user") {
		return "protocol error (details redacted)"
	}
	// Truncate long messages.
	if len(msg) > 120 {
		msg = msg[:120] + "..."
	}
	return msg
}

// sanitizeFTPError removes potential secrets from FTP error messages.
func sanitizeFTPError(err error) string {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline"):
		return "connection timed out"
	case strings.Contains(lower, "refused"):
		return "connection refused (is Developer Mode enabled?)"
	case strings.Contains(lower, "reset"):
		return "connection reset by printer"
	case strings.Contains(lower, "no route"):
		return "no route to host"
	case strings.Contains(lower, "tls"):
		return "TLS handshake failed"
	case strings.Contains(lower, "i/o timeout"):
		return "connection timed out"
	case strings.Contains(lower, "eof"):
		return "connection closed unexpectedly"
	default:
		return "connection failed"
	}
}
