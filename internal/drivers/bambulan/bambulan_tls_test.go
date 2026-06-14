package bambulan

import (
	"context"
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
	"net"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/polimero-app/cli/internal/apperr"
)

func makeSelfSignedTLSCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-printer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        parsed,
	}
}

func newDialTLSDriver(dialFn func(context.Context, string, *tls.Config) (*tls.Conn, error)) *Driver {
	return &Driver{
		newClient: func(_ *mqtt.ClientOptions) mqttConn { panic("not used") },
		dialTLS:   dialFn,
	}
}

func TestCaptureFingerprint_HappyPath(t *testing.T) {
	tlsCert := makeSelfSignedTLSCert(t)
	serverCfg := &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	sum := sha256.Sum256(tlsCert.Certificate[0])
	wantFP := "sha256:" + hex.EncodeToString(sum[:])

	drv := newDialTLSDriver(func(_ context.Context, _ string, clientCfg *tls.Config) (*tls.Conn, error) {
		serverConn, clientConn := net.Pipe()
		tlsServer := tls.Server(serverConn, serverCfg)
		tlsClient := tls.Client(clientConn, clientCfg)
		go func() { _ = tlsServer.Handshake() }()
		if err := tlsClient.Handshake(); err != nil {
			return nil, err
		}
		return tlsClient, nil
	})

	fp, err := drv.CaptureFingerprint(context.Background(), "192.0.2.1", "SN001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp != wantFP {
		t.Errorf("fingerprint = %q, want %q", fp, wantFP)
	}
}

func TestCaptureFingerprint_DialError_ExitsCode4(t *testing.T) {
	drv := newDialTLSDriver(func(_ context.Context, _ string, _ *tls.Config) (*tls.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	})
	_, err := drv.CaptureFingerprint(context.Background(), "192.0.2.1", "SN001")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4, got %v", err)
	}
}

func TestCaptureFingerprint_ContextCancelled_ExitsCode4(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	drv := newDialTLSDriver(func(ctx context.Context, _ string, _ *tls.Config) (*tls.Conn, error) {
		return nil, ctx.Err()
	})
	_, err := drv.CaptureFingerprint(ctx, "192.0.2.1", "SN001")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4, got %v", err)
	}
}
