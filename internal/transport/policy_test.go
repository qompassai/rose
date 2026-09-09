package transport

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEndpointParsing(t *testing.T) {
	for input, want := range map[string]string{
		"": "http://127.0.0.1:11434", "127.0.0.1:0": "http://127.0.0.1:0",
		"::1": "http://[::1]:11434", "[::1]:80": "http://[::1]:80",
		"https://example.com":          "https://example.com:443",
		"https://example.com:443/rose": "https://example.com:443/rose",
		"https://:0":                   "https://:0",
	} {
		got, err := ParseEndpoint(input)
		if err != nil || got.String() != want {
			t.Errorf("%q: got %v, %v; want %s", input, got, err, want)
		}
	}
	for _, input := range []string{
		"ftp://127.0.0.1", "http://user:SECRET@localhost:80", "127.0.0.1:65536",
		"127.0.0.1:-1", "127.0.0.1:no", "127.0.0.1:", "http://127.0.0.1:80?q=SECRET",
		"https://example.com/#SECRET", "http://bad host", "http://[bad]:80", "http://x\\y:80",
		"http://127.0.0.1/a\x01b", "http://127.0.0.1/a b", strings.Repeat("x", MaxEndpointBytes+1),
	} {
		if _, err := ParseEndpoint(input); err == nil {
			t.Errorf("accepted malformed endpoint %q", input)
		} else if strings.Contains(err.Error(), "SECRET") {
			t.Fatal("endpoint error revealed credentials")
		}
	}
}

func TestClientSystemRootsRemainOptional(t *testing.T) {
	files := makeIdentity(t, makeAuthority(t), nil)
	files.CAFile, files.ClientCAFile = "", ""
	config, err := ClientTLSConfig(mustEndpoint(t, "https://localhost:443"), files)
	if err != nil || config.RootCAs != nil {
		t.Fatalf("system roots option rejected: %v", err)
	}
}

func TestPlaintextAndTLSConfigurationPolicy(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "[::1]", "127.8.9.10"} {
		endpoint := mustEndpoint(t, "http://"+host+":11434")
		config, err := ServerTLSConfig(endpoint, Files{})
		if err != nil || config != nil {
			t.Fatalf("loopback rejected: %v", err)
		}
	}
	for _, host := range []string{"0.0.0.0", "[::]", "10.0.0.1", "example.com", "localhost", "127.1", ""} {
		endpoint := mustEndpoint(t, "http://"+host+":11434")
		if _, err := ServerTLSConfig(endpoint, Files{}); err == nil {
			t.Errorf("server accepted plaintext host %q", host)
		}
		if _, err := ClientTLSConfig(endpoint, Files{}); err == nil {
			t.Errorf("client accepted plaintext host %q", host)
		}
	}
	for _, files := range []Files{{CertFile: "cert"}, {KeyFile: "key"}, {CAFile: "ca"}, {ClientCAFile: "ca"}, {CertFile: "cert", KeyFile: "key"}} {
		if _, err := ServerTLSConfig(mustEndpoint(t, "http://127.0.0.1:11434"), files); err == nil {
			t.Errorf("accepted TLS files on HTTP: %+v", files)
		}
	}
	for _, files := range []Files{{}, {CertFile: "cert"}, {KeyFile: "key"}, {CAFile: "ca"}} {
		if _, err := ServerTLSConfig(mustEndpoint(t, "https://127.0.0.1:11434"), files); err == nil {
			t.Errorf("server accepted incomplete TLS files: %+v", files)
		}
		if _, err := ClientTLSConfig(mustEndpoint(t, "https://127.0.0.1:11434"), files); err == nil {
			t.Errorf("client accepted incomplete TLS files: %+v", files)
		}
	}
}

func TestInvalidCredentialsFailBeforeListen(t *testing.T) {
	ca := makeAuthority(t)
	tests := map[string]func(*testing.T, *Files){
		"bad certificate": func(t *testing.T, f *Files) { writeFixture(t, f.CertFile, []byte("not a cert")) },
		"bad key":         func(t *testing.T, f *Files) { writeFixture(t, f.KeyFile, []byte("SECRET invalid key")) },
		"key contents instead of path": func(t *testing.T, f *Files) {
			f.KeyFile = "-----BEGIN PRIVATE KEY-----\nSECRET\n-----END PRIVATE KEY-----"
		},
		"bad CA":     func(t *testing.T, f *Files) { writeFixture(t, f.ClientCAFile, []byte("not a CA")) },
		"missing CA": func(t *testing.T, f *Files) { f.ClientCAFile = "" },
		"oversize certificate": func(t *testing.T, f *Files) {
			writeFixture(t, f.CertFile, bytes.Repeat([]byte("x"), MaxPEMBytes+1))
		},
		"oversize key": func(t *testing.T, f *Files) {
			writeFixture(t, f.KeyFile, bytes.Repeat([]byte("x"), MaxPEMBytes+1))
		},
		"oversize CA": func(t *testing.T, f *Files) {
			writeFixture(t, f.ClientCAFile, bytes.Repeat([]byte("x"), MaxPEMBytes+1))
		},
		"mismatched key": func(t *testing.T, f *Files) { f.KeyFile = makeIdentity(t, ca, nil).KeyFile },
		"directory key":  func(t *testing.T, f *Files) { f.KeyFile = t.TempDir() },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			files := makeIdentity(t, ca, nil)
			mutate(t, &files)
			_, err := ServerTLSConfig(mustEndpoint(t, "https://127.0.0.1:11434"), files)
			if err == nil {
				t.Fatal("invalid credentials accepted")
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Fatal("error leaked private key contents")
			}
		})
	}
}

func TestCertificateRoleAndValidity(t *testing.T) {
	ca := makeAuthority(t)
	for name, mutate := range map[string]func(*x509.Certificate){
		"expired":          func(c *x509.Certificate) { c.NotAfter = time.Now().Add(-time.Minute) },
		"future":           func(c *x509.Certificate) { c.NotBefore = time.Now().Add(time.Minute) },
		"client role only": func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth} },
		"CA identity":      func(c *x509.Certificate) { c.IsCA = true },
		"missing SAN":      func(c *x509.Certificate) { c.DNSNames, c.IPAddresses = nil, nil },
		"cannot sign":      func(c *x509.Certificate) { c.KeyUsage = x509.KeyUsageKeyEncipherment },
	} {
		t.Run(name, func(t *testing.T) {
			files := makeIdentity(t, ca, mutate)
			if _, err := ServerTLSConfig(mustEndpoint(t, "https://127.0.0.1:443"), files); err == nil {
				t.Fatal("unusable server certificate accepted")
			}
		})
	}
}

func TestPrivateKeyPermissionsAndSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permission policy")
	}
	files := makeIdentity(t, makeAuthority(t), nil)
	if err := os.Chmod(files.KeyFile, 0o644); err != nil {
		t.Fatal(err)
	}
	endpoint := mustEndpoint(t, "https://127.0.0.1:443")
	if _, err := ServerTLSConfig(endpoint, files); err == nil {
		t.Fatal("world-readable private key accepted")
	}
	if err := os.Chmod(files.KeyFile, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "key-link")
	if err := os.Symlink(files.KeyFile, link); err != nil {
		t.Fatal(err)
	}
	files.KeyFile = link
	if _, err := ServerTLSConfig(endpoint, files); err == nil {
		t.Fatal("symlink private key accepted")
	}
}

func TestActualListenerAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "0.0.0.0:0"} {
		t.Run(address, func(t *testing.T) {
			ln, err := net.Listen("tcp4", address)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			wrapped, err := WrapListener(ln, nil)
			loopback := ln.Addr().(*net.TCPAddr).IP.IsLoopback()
			if (err == nil) != loopback {
				t.Fatalf("actual address %s: err=%v", ln.Addr(), err)
			}
			if wrapped != nil {
				wrapped.Close()
			}
		})
	}
	server, _ := testConfigs(t)
	server.CurvePreferences = []tls.CurveID{tls.X25519}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := WrapListener(ln, server); err == nil {
		t.Fatal("listener accepted weakened TLS configuration")
	}
}
