package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testAuthority struct {
	cert *x509.Certificate
	key  ed25519.PrivateKey
	pem  []byte
}

func makeAuthority(t *testing.T) testAuthority {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testAuthority{cert: cert, key: private, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func makeIdentity(t *testing.T, ca testAuthority, mutate func(*x509.Certificate)) Files {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "test peer"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	if mutate != nil {
		mutate(cert)
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, ca.cert, public, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := Files{
		CertFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem"),
		ClientCAFile: filepath.Join(dir, "ca.pem"), CAFile: filepath.Join(dir, "ca.pem"),
	}
	writeFixture(t, files.CertFile, append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), ca.pem...))
	writeFixture(t, files.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	writeFixture(t, files.CAFile, ca.pem)
	return files
}

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustEndpoint(t *testing.T, value string) *url.URL {
	t.Helper()
	endpoint, err := ParseEndpoint(value)
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}

func testConfigs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	ca := makeAuthority(t)
	files := makeIdentity(t, ca, nil)
	endpoint := mustEndpoint(t, "https://127.0.0.1:11434")
	server, err := ServerTLSConfig(endpoint, files)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ClientTLSConfig(endpoint, files)
	if err != nil {
		t.Fatal(err)
	}
	return server, client
}

type handshakeResult struct {
	state tls.ConnectionState
	err   error
}

func realHandshake(t *testing.T, server, client *tls.Config) (handshakeResult, handshakeResult) {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := WrapListener(raw, server)
	if err != nil {
		raw.Close()
		t.Fatal(err)
	}
	defer ln.Close()
	results := make(chan handshakeResult, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			results <- handshakeResult{err: err}
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		secure := conn.(*tls.Conn)
		err = secure.Handshake()
		results <- handshakeResult{state: secure.ConnectionState(), err: err}
	}()
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	secure := tls.Client(conn, client)
	clientErr := secure.Handshake()
	clientResult := handshakeResult{state: secure.ConnectionState(), err: clientErr}
	select {
	case serverResult := <-results:
		return serverResult, clientResult
	case <-time.After(5 * time.Second):
		t.Fatal("TLS handshake did not finish within its deadline")
		return handshakeResult{}, handshakeResult{}
	}
}

func TestHybridMutualTLSHandshake(t *testing.T) {
	server, client := testConfigs(t)
	serverResult, clientResult := realHandshake(t, server, client)
	for _, result := range []handshakeResult{serverResult, clientResult} {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.state.Version != tls.VersionTLS13 || result.state.CurveID != tls.X25519MLKEM768 {
			t.Fatalf("negotiated version=%x curve=%v", result.state.Version, result.state.CurveID)
		}
		if len(result.state.VerifiedChains) == 0 || !result.state.HandshakeComplete {
			t.Fatal("peer certificate was not verified")
		}
	}
}

func TestRejectTLSFallbackAndUntrustedPeers(t *testing.T) {
	tests := map[string]func(*tls.Config){
		"classical only": func(c *tls.Config) { c.CurvePreferences = []tls.CurveID{tls.X25519} },
		"TLS 1.2": func(c *tls.Config) {
			c.MinVersion, c.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
			c.CurvePreferences = []tls.CurveID{tls.X25519}
		},
		"missing client certificate": func(c *tls.Config) { c.Certificates = nil },
		"untrusted server":           func(c *tls.Config) { c.RootCAs = x509.NewCertPool() },
		"wrong server name":          func(c *tls.Config) { c.ServerName = "different.example" },
		"untrusted client": func(c *tls.Config) {
			files := makeIdentity(t, makeAuthority(t), nil)
			other, err := ClientTLSConfig(mustEndpoint(t, "https://127.0.0.1:11434"), files)
			if err != nil {
				t.Fatal(err)
			}
			c.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				return &other.Certificates[0], nil
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			server, client := testConfigs(t)
			mutate(client)
			a, b := realHandshake(t, server, client)
			if a.err == nil && b.err == nil {
				t.Fatal("forbidden TLS connection succeeded")
			}
			if (name == "missing client certificate" || name == "untrusted client") && a.err == nil {
				t.Fatal("server did not reject an unauthenticated peer")
			}
		})
	}
}
