package bambulan

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
	"time"
)

// makeSelfSignedCert generates a throwaway self-signed cert for testing.
func makeSelfSignedCert(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return cert
}

func TestMapState(t *testing.T) {
	cases := []struct {
		gcodeState string
		want       string
	}{
		{"IDLE", "idle"},
		{"FINISH", "idle"},
		{"PRINTING", "printing"},
		{"PREPARE", "printing"},
		{"RUNNING", "printing"},
		{"SLICING", "printing"},
		{"PAUSED", "paused"},
		{"FAILED", "error"},
		{"", "unknown"},
		{"UNKNOWN_STATE", "unknown"},
	}
	for _, c := range cases {
		got := mapState(c.gcodeState)
		if got != c.want {
			t.Errorf("mapState(%q) = %q, want %q", c.gcodeState, got, c.want)
		}
	}
}

func TestBuildTLSConfig_Insecure_NoVerifyConnection(t *testing.T) {
	cfg, err := buildTLSConfig("SN001", "sha256:aabbcc", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VerifyConnection != nil {
		t.Error("VerifyConnection should be nil for insecure mode")
	}
	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true")
	}
}

func TestBuildTLSConfig_EmptyFingerprint_NoVerifyConnection(t *testing.T) {
	cfg, err := buildTLSConfig("SN001", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VerifyConnection != nil {
		t.Error("VerifyConnection should be nil when fingerprint is empty (capture mode)")
	}
}

func TestBuildTLSConfig_Mismatch_ReturnsFingerprintMismatchError(t *testing.T) {
	cfg, err := buildTLSConfig("SN001", "sha256:expectedfingerprint", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cert := makeSelfSignedCert(t)
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}

	verifyErr := cfg.VerifyConnection(cs)
	if verifyErr == nil {
		t.Fatal("expected fingerprint mismatch error, got nil")
	}
	var fpErr *fingerprintMismatchError
	if !errors.As(verifyErr, &fpErr) {
		t.Fatalf("expected *fingerprintMismatchError, got %T: %v", verifyErr, verifyErr)
	}
	if fpErr.want != "sha256:expectedfingerprint" {
		t.Errorf("want = %q, expected sha256:expectedfingerprint", fpErr.want)
	}
}

func TestBuildTLSConfig_Match_ReturnsNil(t *testing.T) {
	cert := makeSelfSignedCert(t)
	sum := sha256.Sum256(cert.Raw)
	fp := "sha256:" + hex.EncodeToString(sum[:])

	cfg, _ := buildTLSConfig("SN001", fp, false)
	cs := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}

	if err := cfg.VerifyConnection(cs); err != nil {
		t.Errorf("unexpected error for matching fingerprint: %v", err)
	}
}
