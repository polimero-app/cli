package bambulan

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

// mockFTPConn implements ftpConn for testing.
type mockFTPConn struct {
	loginErr   error
	listResult []*ftp.Entry
	listErr    error
	sizeResult int64
	sizeErr    error
	retrData   []byte
	retrErr    error
	storErr    error
	storData   []byte // captured data from Stor
	storCalled bool
	quitErr    error
}

func (m *mockFTPConn) Login(_, _ string) error { return m.loginErr }
func (m *mockFTPConn) List(_ string) ([]*ftp.Entry, error) {
	return m.listResult, m.listErr
}
func (m *mockFTPConn) FileSize(_ string) (int64, error) {
	return m.sizeResult, m.sizeErr
}
func (m *mockFTPConn) Retr(_ string) (io.ReadCloser, error) {
	if m.retrErr != nil {
		return nil, m.retrErr
	}
	return io.NopCloser(bytes.NewReader(m.retrData)), nil
}
func (m *mockFTPConn) Stor(_ string, r io.Reader) error {
	m.storCalled = true
	if m.storErr != nil {
		return m.storErr
	}
	data, _ := io.ReadAll(r)
	m.storData = data
	return nil
}
func (m *mockFTPConn) Quit() error { return m.quitErr }

func mockDialer(conn ftpConn, err error) ftpDialer {
	return func(_ context.Context, _ string, _ *tls.Config) (ftpConn, error) {
		return conn, err
	}
}

func testProfileInput() driver.ProfileInput {
	return driver.ProfileInput{
		Name:     "test-printer",
		Driver:   "bambu-lan",
		Host:     "192.0.2.10",
		Serial:   "SN001",
		Timeout:  10 * time.Second,
		Insecure: true,
	}
}

func testSecrets() driver.SecretsBundle {
	return driver.SecretsBundle{
		AccessCode: "testcode",
	}
}

func TestFileRoots_ReturnsSDCard(t *testing.T) {
	conn := &mockFTPConn{}
	d := &Driver{dialFTP: mockDialer(conn, nil)}

	ctx := context.Background()
	roots, err := d.FileRoots(ctx, testProfileInput(), testSecrets(), slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(roots))
	}
	if roots[0].Name != "sdcard" {
		t.Errorf("expected root name 'sdcard', got %q", roots[0].Name)
	}
	if !roots[0].Writable {
		t.Error("expected root to be writable")
	}
}

func TestFileRoots_ConnectionError(t *testing.T) {
	d := &Driver{dialFTP: mockDialer(nil, errors.New("connection refused"))}

	ctx := context.Background()
	_, err := d.FileRoots(ctx, testProfileInput(), testSecrets(), slog.Default())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFileRoots_FingerprintMismatchExitsAuth(t *testing.T) {
	fpErr := &fingerprintMismatchError{got: "aa", want: "bb"}
	d := &Driver{dialFTP: mockDialer(nil, fpErr)}

	ctx := context.Background()
	_, err := d.FileRoots(ctx, testProfileInput(), testSecrets(), slog.Default())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *apperr.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != 3 {
		t.Errorf("expected exit code 3, got %d", exitErr.Code)
	}
	if err.Error() != "TLS fingerprint mismatch" {
		t.Errorf("expected sanitized message %q, got %q", "TLS fingerprint mismatch", err.Error())
	}
	if !errors.As(err, new(*fingerprintMismatchError)) {
		t.Error("expected wrapped fingerprintMismatchError for errors.As")
	}
}

func TestFileRoots_AuthError(t *testing.T) {
	conn := &mockFTPConn{loginErr: errors.New("530 Login incorrect")}
	d := &Driver{dialFTP: mockDialer(conn, nil)}

	ctx := context.Background()
	_, err := d.FileRoots(ctx, testProfileInput(), testSecrets(), slog.Default())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, err) { // just ensure it's non-nil
		t.Fatalf("unexpected error type: %v", err)
	}
}

func TestFileList_ListsEntries(t *testing.T) {
	modTime := time.Date(2026, 6, 15, 12, 34, 0, 0, time.UTC)
	conn := &mockFTPConn{
		listResult: []*ftp.Entry{
			{Name: "models", Type: ftp.EntryTypeFolder, Time: modTime},
			{Name: "cube.3mf", Type: ftp.EntryTypeFile, Size: 240640, Time: modTime},
			{Name: ".", Type: ftp.EntryTypeFolder},
			{Name: "..", Type: ftp.EntryTypeFolder},
		},
	}
	d := &Driver{dialFTP: mockDialer(conn, nil)}

	ctx := context.Background()
	result, err := d.FileList(ctx, testProfileInput(), testSecrets(), "sdcard", "/", false, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries (. and .. filtered), got %d", len(result.Entries))
	}
	// Entries should be sorted by name.
	if result.Entries[0].Name != "cube.3mf" {
		t.Errorf("expected first entry 'cube.3mf', got %q", result.Entries[0].Name)
	}
	if result.Entries[1].Name != "models" {
		t.Errorf("expected second entry 'models', got %q", result.Entries[1].Name)
	}
	// Check type mapping.
	if result.Entries[0].Type != driver.FileEntryTypeFile {
		t.Errorf("expected file type, got %q", result.Entries[0].Type)
	}
	if result.Entries[1].Type != driver.FileEntryTypeDirectory {
		t.Errorf("expected directory type, got %q", result.Entries[1].Type)
	}
}

func TestFileList_UnknownRoot_ReturnsError(t *testing.T) {
	conn := &mockFTPConn{}
	d := &Driver{dialFTP: mockDialer(conn, nil)}

	ctx := context.Background()
	_, err := d.FileList(ctx, testProfileInput(), testSecrets(), "unknown", "/", false, slog.Default())
	if err == nil {
		t.Fatal("expected error for unknown root")
	}
}

func TestFileList_MaliciousPath_RejectedByDriver(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{"traversal", "/models/../../etc/passwd"},
		{"nul byte", "/models/a\x00b.3mf"},
		{"c0 control", "/models/a\nb.3mf"},
		{"c1 control", "/models/a\u009bb.3mf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &mockFTPConn{}
			d := &Driver{dialFTP: mockDialer(conn, nil)}

			ctx := context.Background()
			_, err := d.FileList(ctx, testProfileInput(), testSecrets(), "sdcard", tc.path, false, slog.Default())
			var exitErr *apperr.ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != 2 {
				t.Fatalf("expected exit 2 for %q, got %v", tc.path, err)
			}
			if strings.Contains(err.Error(), tc.path) {
				t.Errorf("error message echoes raw path: %q", err.Error())
			}
		})
	}
}

func TestFileUpload_StoresData(t *testing.T) {
	conn := &mockFTPConn{
		listResult: []*ftp.Entry{}, // empty - no existing file
	}
	d := &Driver{dialFTP: mockDialer(conn, nil)}

	ctx := context.Background()
	data := []byte("hello world")
	result, err := d.FileUpload(ctx, testProfileInput(), testSecrets(), "sdcard", "/test.3mf", bytes.NewReader(data), int64(len(data)), false, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.BytesTransferred == nil || *result.BytesTransferred != int64(len(data)) {
		t.Errorf("expected %d bytes transferred, got %v", len(data), result.BytesTransferred)
	}
	if !bytes.Equal(conn.storData, data) {
		t.Errorf("stored data mismatch")
	}
}

func TestFileUpload_UnknownRoot_ReturnsError(t *testing.T) {
	conn := &mockFTPConn{}
	d := &Driver{dialFTP: mockDialer(conn, nil)}

	ctx := context.Background()
	_, err := d.FileUpload(ctx, testProfileInput(), testSecrets(), "unknown", "/test.3mf", bytes.NewReader(nil), 0, false, slog.Default())
	if err == nil {
		t.Fatal("expected error for unknown root")
	}
}

func TestFileUpload_RefusesOverwrite_WhenFileExists(t *testing.T) {
	conn := &mockFTPConn{
		listResult: []*ftp.Entry{{Name: "test.3mf", Type: ftp.EntryTypeFile}},
	}
	d := &Driver{dialFTP: mockDialer(conn, nil)}

	ctx := context.Background()
	_, err := d.FileUpload(ctx, testProfileInput(), testSecrets(), "sdcard", "/test.3mf", bytes.NewReader([]byte("x")), 1, false, slog.Default())
	if err == nil {
		t.Fatal("expected error when destination exists without overwrite")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected already-exists message, got %q", err.Error())
	}
	if conn.storCalled {
		t.Error("Stor must not be called when destination exists")
	}
}

func TestFileUpload_FailsClosed_WhenExistenceCheckFails(t *testing.T) {
	conn := &mockFTPConn{
		listErr: errors.New("MLSD of a file path is not allowed"),
		sizeErr: errors.New("450 transient failure"),
	}
	d := &Driver{dialFTP: mockDialer(conn, nil)}

	ctx := context.Background()
	_, err := d.FileUpload(ctx, testProfileInput(), testSecrets(), "sdcard", "/test.3mf", bytes.NewReader([]byte("x")), 1, false, slog.Default())
	if err == nil {
		t.Fatal("expected fail-closed error when existence cannot be determined")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("expected exit code 1, got %v", err)
	}
	if conn.storCalled {
		t.Error("Stor must not be called when existence is undetermined")
	}
}

func TestFileUpload_SizeFallback_ExistingFile_Refuses(t *testing.T) {
	conn := &mockFTPConn{
		listErr:    errors.New("MLSD of a file path is not allowed"),
		sizeResult: 1234,
	}
	d := &Driver{dialFTP: mockDialer(conn, nil)}

	ctx := context.Background()
	_, err := d.FileUpload(ctx, testProfileInput(), testSecrets(), "sdcard", "/test.3mf", bytes.NewReader([]byte("x")), 1, false, slog.Default())
	if err == nil {
		t.Fatal("expected error when SIZE probe finds an existing file")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if conn.storCalled {
		t.Error("Stor must not be called when destination exists")
	}
}

func TestFileUpload_SizeFallback_NotFound_Proceeds(t *testing.T) {
	conn := &mockFTPConn{
		listErr: errors.New("MLSD of a file path is not allowed"),
		sizeErr: errors.New("550 Could not get file size."),
	}
	d := &Driver{dialFTP: mockDialer(conn, nil)}

	ctx := context.Background()
	data := []byte("hello world")
	result, err := d.FileUpload(ctx, testProfileInput(), testSecrets(), "sdcard", "/test.3mf", bytes.NewReader(data), int64(len(data)), false, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.BytesTransferred == nil || *result.BytesTransferred != int64(len(data)) {
		t.Errorf("expected %d bytes transferred, got %v", len(data), result.BytesTransferred)
	}
}

func TestFileUpload_Overwrite_SkipsExistenceCheck(t *testing.T) {
	conn := &mockFTPConn{
		listErr: errors.New("listing failed"),
		sizeErr: errors.New("450 transient failure"),
	}
	d := &Driver{dialFTP: mockDialer(conn, nil)}

	ctx := context.Background()
	data := []byte("hello world")
	_, err := d.FileUpload(ctx, testProfileInput(), testSecrets(), "sdcard", "/test.3mf", bytes.NewReader(data), int64(len(data)), true, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error with --overwrite: %v", err)
	}
	if !bytes.Equal(conn.storData, data) {
		t.Errorf("stored data mismatch")
	}
}

func TestBuildFTPTLSConfig_Insecure(t *testing.T) {
	cfg, err := buildFTPTLSConfig("SN001", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true for insecure mode")
	}
	if cfg.VerifyConnection != nil {
		t.Error("expected no VerifyConnection callback for insecure mode")
	}
}

func TestBuildFTPTLSConfig_InvalidFingerprint(t *testing.T) {
	_, err := buildFTPTLSConfig("SN001", "invalid", false)
	if err == nil {
		t.Fatal("expected error for invalid fingerprint")
	}
}

func TestBuildFTPTLSConfig_ValidFingerprint(t *testing.T) {
	fp := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cfg, err := buildFTPTLSConfig("SN001", fp, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VerifyConnection == nil {
		t.Error("expected VerifyConnection callback for secure mode")
	}
	if cfg.ServerName != "SN001" {
		t.Errorf("expected ServerName=SN001, got %q", cfg.ServerName)
	}
}

func TestMapFTPEntry_File(t *testing.T) {
	modTime := time.Date(2026, 6, 15, 12, 34, 0, 0, time.UTC)
	e := &ftp.Entry{
		Name: "cube.3mf",
		Type: ftp.EntryTypeFile,
		Size: 240640,
		Time: modTime,
	}
	entry := mapFTPEntry(e, "sdcard", "/")
	if entry.Name != "cube.3mf" {
		t.Errorf("Name = %q", entry.Name)
	}
	if entry.Root != "sdcard" {
		t.Errorf("Root = %q", entry.Root)
	}
	if entry.Path != "/cube.3mf" {
		t.Errorf("Path = %q", entry.Path)
	}
	if entry.DevicePath != "sdcard:/cube.3mf" {
		t.Errorf("DevicePath = %q", entry.DevicePath)
	}
	if entry.Type != driver.FileEntryTypeFile {
		t.Errorf("Type = %q", entry.Type)
	}
	if entry.SizeBytes == nil || *entry.SizeBytes != 240640 {
		t.Errorf("SizeBytes = %v", entry.SizeBytes)
	}
	if entry.ModifiedAt == nil || *entry.ModifiedAt != "2026-06-15T12:34:00Z" {
		t.Errorf("ModifiedAt = %v", entry.ModifiedAt)
	}
}

func TestMapFTPEntry_Directory(t *testing.T) {
	e := &ftp.Entry{
		Name: "models",
		Type: ftp.EntryTypeFolder,
	}
	entry := mapFTPEntry(e, "sdcard", "/")
	if entry.Type != driver.FileEntryTypeDirectory {
		t.Errorf("Type = %q, want directory", entry.Type)
	}
	if entry.SizeBytes != nil {
		t.Errorf("expected nil SizeBytes for directory, got %v", entry.SizeBytes)
	}
}

func TestMapFTPEntry_NestedPath(t *testing.T) {
	e := &ftp.Entry{
		Name: "bracket.3mf",
		Type: ftp.EntryTypeFile,
		Size: 1887436,
	}
	entry := mapFTPEntry(e, "sdcard", "/models")
	if entry.Path != "/models/bracket.3mf" {
		t.Errorf("Path = %q, want /models/bracket.3mf", entry.Path)
	}
	if entry.DevicePath != "sdcard:/models/bracket.3mf" {
		t.Errorf("DevicePath = %q", entry.DevicePath)
	}
}

func TestCapabilities_FileOpsTrue(t *testing.T) {
	d := New()
	caps := d.Capabilities()
	if !caps.FileList {
		t.Error("expected FileList = true")
	}
	if !caps.FileDownload {
		t.Error("expected FileDownload = true")
	}
	if !caps.FileUpload {
		t.Error("expected FileUpload = true")
	}
}
