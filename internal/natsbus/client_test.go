package natsbus

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/nats-io/nats.go"
)

func TestTLSOptionsVerifyServerIdentity(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	options, err := connectionOptions(Options{TLSCAFile: ca, TLSServerName: "nats.example"})
	if err != nil {
		t.Fatal(err)
	}
	natsOptions := nats.GetDefaultOptions()
	for _, option := range options {
		if err := option(&natsOptions); err != nil {
			t.Fatal(err)
		}
	}
	if !natsOptions.Secure || natsOptions.TLSConfig == nil {
		t.Fatal("TLS transport was not enabled")
	}
	c := natsOptions.TLSConfig
	if c.InsecureSkipVerify || c.ServerName != "nats.example" || c.MinVersion < tls.VersionTLS12 {
		t.Fatal("TLS identity checks were not enabled")
	}
	if _, err := server.Certificate().Verify(x509.VerifyOptions{Roots: c.RootCAs}); err != nil {
		t.Fatalf("configured CA was not trusted: %v", err)
	}
}

func TestInvalidTLSMaterialRejectedBeforeConnecting(t *testing.T) {
	invalidCA := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(invalidCA, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, o := range []Options{
		{TLSCertFile: "client.pem"}, {TLSKeyFile: "client.key"}, {TLSCAFile: invalidCA},
		{TLSCAFile: filepath.Join(t.TempDir(), "missing")}, {TLSCertFile: invalidCA, TLSKeyFile: invalidCA},
	} {
		if _, err := connectionOptions(o); err == nil {
			t.Fatal("invalid TLS configuration accepted")
		}
	}
}
