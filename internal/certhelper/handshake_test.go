package certhelper

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

// hybridServerConfig mirrors internal/transport's server policy:
// TLS 1.3 only, the X25519MLKEM768 hybrid curve, and required verified
// client certificates. The transport package itself lives on the
// security-port branch; this test proves the helper's output satisfies
// the same policy shape end to end.
func hybridServerConfig(store *Store, t *testing.T) *tls.Config {
	t.Helper()
	return &tls.Config{
		MinVersion:       tls.VersionTLS13,
		MaxVersion:       tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{tls.X25519MLKEM768},
		Certificates:     []tls.Certificate{loadPair(t, store.ServerCertPath(), store.ServerKeyPath())},
		ClientAuth:       tls.RequireAndVerifyClientCert,
		ClientCAs:        caPool(t, store),
	}
}

func hybridClientConfig(store *Store, name string, t *testing.T) *tls.Config {
	t.Helper()
	cfg := &tls.Config{
		MinVersion:       tls.VersionTLS13,
		MaxVersion:       tls.VersionTLS13,
		CurvePreferences: []tls.CurveID{tls.X25519MLKEM768},
		RootCAs:          caPool(t, store),
		ServerName:       "localhost",
	}
	if name != "" {
		cfg.Certificates = []tls.Certificate{loadPair(t, store.ClientCertPath(name), store.ClientKeyPath(name))}
	}
	return cfg
}

// roundTrip runs one mutual-TLS echo exchange over loopback and
// returns the client's handshake/write error, if any.
func roundTrip(serverCfg, clientCfg *tls.Config) error {
	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverCfg)
	if err != nil {
		return err
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err == nil {
			_, _ = conn.Write(buf)
		}
	}()
	conn, err := tls.Dial("tcp", ln.Addr().String(), clientCfg)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		return err
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if string(buf) != "ping" {
		return errors.New("echo mismatch")
	}
	return nil
}

func TestMutualTLSHandshake(t *testing.T) {
	store := newTestStore(t)
	provision(t, store)
	if err := roundTrip(hybridServerConfig(store, t), hybridClientConfig(store, "laptop", t)); err != nil {
		t.Fatalf("valid client must complete the handshake: %v", err)
	}
}

func TestMutualTLSRejectsWrongCA(t *testing.T) {
	store := newTestStore(t)
	provision(t, store)
	foreign := newTestStore(t)
	provision(t, foreign)
	// Client cert from a different CA, but trusting our server's CA:
	// the server must refuse it during client verification.
	cfg := hybridClientConfig(store, "", t)
	cfg.Certificates = []tls.Certificate{loadPair(t, foreign.ClientCertPath("laptop"), foreign.ClientKeyPath("laptop"))}
	if err := roundTrip(hybridServerConfig(store, t), cfg); err == nil {
		t.Fatal("server accepted a client certificate from a foreign CA")
	}
}

func TestMutualTLSRejectsNoClientCert(t *testing.T) {
	store := newTestStore(t)
	provision(t, store)
	if err := roundTrip(hybridServerConfig(store, t), hybridClientConfig(store, "", t)); err == nil {
		t.Fatal("server accepted a client with no certificate")
	}
}

func TestMutualTLSRejectsExpiredClient(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.InitCA("rose-ca", 365); err != nil {
		t.Fatal(err)
	}
	if _, err := store.IssueServer([]string{"localhost"}, 90); err != nil {
		t.Fatal(err)
	}
	ca, err := store.loadCA()
	if err != nil {
		t.Fatal(err)
	}
	// White-box: issue an already-expired client leaf directly.
	now := time.Now()
	leaf, keyPEM, err := makeLeaf(ca, leafSpec{
		commonName: "expired",
		usage:      x509.ExtKeyUsageClientAuth,
		notBefore:  now.Add(-48 * time.Hour),
		notAfter:   now.Add(-24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := dir+"/expired.crt", dir+"/expired.key"
	chain := append(pemEncode("CERTIFICATE", leaf.Raw), ca.certPEM...)
	if err := os.WriteFile(certPath, chain, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := hybridClientConfig(store, "", t)
	cfg.Certificates = []tls.Certificate{loadPair(t, certPath, keyPath)}
	if err := roundTrip(hybridServerConfig(store, t), cfg); err == nil {
		t.Fatal("server accepted an expired client certificate")
	}
}
