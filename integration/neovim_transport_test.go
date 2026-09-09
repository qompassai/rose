package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qompassai/rose/internal/transport"
)

// This is a real Neovim/curl/TLS protocol test, not a model inference test.
// Explicit paths opt in; ordinary go test does not require another checkout.
func TestNeovimTransport(t *testing.T) {
	root := os.Getenv("ROSE_NVIM_ROOT")
	if root == "" {
		t.Skip("set ROSE_NVIM_ROOT and NVIM to enable the cross-repository test")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("ROSE_NVIM_ROOT must be an absolute checkout path")
	}
	if _, err := os.Stat(filepath.Join(root, "lua", "rose", "init.lua")); err != nil {
		t.Fatal(err)
	}
	nvim, err := exec.LookPath(os.Getenv("NVIM"))
	if err != nil {
		t.Fatalf("NVIM must name a real Neovim executable: %v", err)
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Fatal("real curl with X25519MLKEM768 support is required")
	}
	serverFiles, clientFiles := transportIdentities(t)
	for _, tc := range []struct {
		name, provider                     string
		tls, badCA, classical, wantFailure bool
	}{
		{name: "default_rose_loopback", provider: "rose"},
		{name: "legacy_ollama_options", provider: "ollama"},
		{name: "rose_hybrid_mtls", provider: "rose", tls: true},
		{name: "rose_rejects_untrusted_server", provider: "rose", tls: true, badCA: true, wantFailure: true},
		{name: "rose_rejects_classical_only_server", provider: "rose", tls: true, classical: true, wantFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if tc.tls && (r.TLS == nil || r.TLS.Version != tls.VersionTLS13 ||
					r.TLS.CurveID != tls.X25519MLKEM768 || len(r.TLS.VerifiedChains) == 0) {
					t.Error("request did not negotiate verified hybrid TLS 1.3")
					http.Error(w, "TLS invariant failed", http.StatusBadRequest)
					return
				}
				serveNeovimChat(t, w, r)
			})
			base := startNeovimFixture(t, handler, serverFiles, tc.tls, tc.classical)
			files := clientFiles
			if tc.badCA {
				_, untrusted := transportIdentities(t)
				files.CAFile = untrusted.CAFile
			}
			runNeovim(t, nvim, root, base, tc.provider, files, tc.wantFailure)
			want := int32(1)
			if tc.wantFailure {
				want = 0
			}
			if got := requests.Load(); got != want {
				t.Fatalf("requests reaching the handler = %d, want %d", got, want)
			}
		})
	}
}

func serveNeovimChat(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/api/chat" {
		t.Error("unexpected chat route or method")
		http.Error(w, "unexpected route", http.StatusBadRequest)
		return
	}
	var request struct {
		Model    string                           `json:"model"`
		Stream   *bool                            `json:"stream"`
		Messages []struct{ Role, Content string } `json:"messages"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&request); err != nil {
		t.Errorf("decode request: %v", err)
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if request.Model != "transport-fixture" || request.Stream == nil || *request.Stream ||
		len(request.Messages) != 1 || request.Messages[0].Content != "synthetic transport check" {
		t.Error("Neovim did not send the compatible non-streaming chat payload")
		http.Error(w, "unexpected payload", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := io.WriteString(w, `{"message":{"role":"assistant","content":"verified fixture response"},"done":true}`); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func startNeovimFixture(t *testing.T, handler http.Handler, files transport.Files, secure, classical bool) string {
	t.Helper()
	ts := httptest.NewUnstartedServer(handler)
	t.Cleanup(ts.Close)
	endpoint := &url.URL{Scheme: "http", Host: ts.Listener.Addr().String()}
	var config *tls.Config
	if secure {
		endpoint.Scheme = "https"
		var err error
		config, err = transport.ServerTLSConfig(endpoint, files)
		if err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if classical {
		// Deliberately bypass Rose's policy for this negative client test.
		config.CurvePreferences = []tls.CurveID{tls.X25519}
		ts.Listener = tls.NewListener(ts.Listener, config)
	} else {
		ts.Listener, err = transport.WrapListener(ts.Listener, config)
		if err != nil {
			t.Fatal(err)
		}
	}
	ts.Config.ReadHeaderTimeout = transport.ReadHeaderTimeout
	ts.Config.IdleTimeout = transport.IdleTimeout
	ts.Config.MaxHeaderBytes = transport.MaxHeaderBytes
	ts.Start()
	return endpoint.String()
}

func runNeovim(t *testing.T, nvim, root, base, provider string, files transport.Files, wantFailure bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nvim, "--headless", "-u", "NONE", "-l", "testdata/neovim_transport.lua")
	home := t.TempDir()
	keylog := filepath.Join(home, "tls-keys.log")
	cmd.Env = append(os.Environ(),
		"ROSE_TEST_NVIM_ROOT="+root, "ROSE_TEST_BASE="+base, "ROSE_TEST_PROVIDER="+provider,
		"ROSE_TEST_CA="+files.CAFile, "ROSE_TEST_CERT="+files.CertFile, "ROSE_TEST_KEY="+files.KeyFile,
		fmt.Sprintf("ROSE_TEST_EXPECT_FAILURE=%t", wantFailure),
		"HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1",
		"CURL_CA_BUNDLE="+filepath.Join(home, "missing-ca.pem"), "SSLKEYLOGFILE="+keylog,
		"NVIM_LOG_FILE="+filepath.Join(home, "nvim.log"),
	)
	output := &boundedTestOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		t.Fatalf("Neovim transport regression: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "ROSE_TRANSPORT_PASS") {
		t.Fatalf("Neovim did not report completed assertions: %s", output.String())
	}
	if strings.HasPrefix(base, "https://") {
		if _, err := os.Stat(keylog); !os.IsNotExist(err) {
			t.Fatalf("TLS key logging was not isolated: %v", err)
		}
	}
}

type boundedTestOutput struct{ bytes.Buffer }

func (b *boundedTestOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 64<<10 - b.Len(); remaining > 0 {
		_, _ = b.Buffer.Write(p[:min(remaining, n)])
	}
	return n, nil
}

func transportIdentities(t *testing.T) (transport.Files, transport.Files) {
	t.Helper()
	dir := t.TempDir()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Rose fixture CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(dir, "ca.pem")
	writeTestPEM(t, caPath, "CERTIFICATE", caDER)
	issue := func(name string, serial int64, usage x509.ExtKeyUsage) transport.Files {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		leaf := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
			NotBefore: ca.NotBefore, NotAfter: ca.NotAfter,
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
			IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		}
		der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		files := transport.Files{
			CertFile: filepath.Join(dir, name+".pem"), KeyFile: filepath.Join(dir, name+".key"),
			CAFile: caPath, ClientCAFile: caPath,
		}
		writeTestPEM(t, files.CertFile, "CERTIFICATE", der)
		writeTestPEM(t, files.KeyFile, "PRIVATE KEY", keyDER)
		return files
	}
	return issue("server", 2, x509.ExtKeyUsageServerAuth), issue("client", 3, x509.ExtKeyUsageClientAuth)
}

func writeTestPEM(t *testing.T, path, kind string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data}), 0o600); err != nil {
		t.Fatal(err)
	}
}
