package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestHTTPClientUsesRealHybridTLS(t *testing.T) {
	files := makeIdentity(t, makeAuthority(t), nil)
	endpoint := mustEndpoint(t, "https://127.0.0.1:443")
	config, err := ServerTLSConfig(endpoint, files)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.TLS.CurveID != tls.X25519MLKEM768 || len(r.TLS.VerifiedChains) == 0 {
			http.Error(w, "hybrid mTLS not negotiated", http.StatusForbidden)
			return
		}
		w.Write([]byte("secure"))
	}))
	ts.TLS = config
	ts.StartTLS()
	defer ts.Close()
	client, err := NewHTTPClient(mustEndpoint(t, ts.URL), files)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.TLS.CurveID != tls.X25519MLKEM768 {
		t.Fatal("HTTP client did not complete a verified hybrid TLS request")
	}
}

func TestHTTPClientRejectsClassicalServer(t *testing.T) {
	files := makeIdentity(t, makeAuthority(t), nil)
	config, err := ServerTLSConfig(mustEndpoint(t, "https://127.0.0.1:443"), files)
	if err != nil {
		t.Fatal(err)
	}
	config.CurvePreferences = []tls.CurveID{tls.X25519}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("classical TLS request reached the application")
		w.WriteHeader(http.StatusOK)
	}))
	ts.TLS = config
	ts.StartTLS()
	defer ts.Close()
	client, err := NewHTTPClient(mustEndpoint(t, ts.URL), files)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if response, err := client.Get(ts.URL); err == nil {
		response.Body.Close()
		t.Fatal("client silently fell back to classical TLS")
	}
}

func TestHTTPClientTransportIsolation(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://credentials:SECRET@proxy.invalid:8888")
	t.Setenv("HTTPS_PROXY", "http://credentials:SECRET@proxy.invalid:8888")
	client, err := NewHTTPClient(mustEndpoint(t, ""), Files{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	tr, ok := client.Transport.(*http.Transport)
	if !ok || tr == http.DefaultTransport || tr.Proxy != nil {
		t.Fatal("client inherited the default transport or an environment proxy")
	}
	if tr.TLSHandshakeTimeout != TLSHandshakeTimeout || tr.ResponseHeaderTimeout != ResponseHeaderTimeout ||
		tr.IdleConnTimeout != IdleTimeout || tr.MaxConnsPerHost != MaxClientConnections ||
		tr.MaxResponseHeaderBytes != MaxResponseHeaderBytes {
		t.Fatal("client transport bounds are not enforced")
	}
	if _, err := tr.DialContext(context.Background(), "tcp", "localhost:11434"); err == nil {
		t.Fatal("plaintext dial resolved DNS")
	}
	if _, err := tr.DialContext(context.Background(), "tcp", "192.0.2.1:11434"); err == nil {
		t.Fatal("plaintext dial permitted a remote address")
	}
}

func TestHTTPClientRedirectAndCancellation(t *testing.T) {
	var reached int
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/destination", http.StatusFound)
		case "/destination":
			mu.Lock()
			reached++
			mu.Unlock()
		case "/wait":
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
	defer ts.Close()
	client, err := NewHTTPClient(mustEndpoint(t, ts.URL), Files{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if _, err := client.Get(ts.URL + "/redirect"); err == nil {
		t.Fatal("redirect was accepted")
	}
	mu.Lock()
	if reached != 0 {
		t.Error("redirect destination was contacted")
	}
	mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/wait", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.ReadAll(response.Body); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("body read ignored request cancellation: %v", err)
	}
}

func TestConnectionLimiterCloseUnblocksAccept(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := &limitedListener{Listener: raw, tokens: make(chan struct{}, 1), done: make(chan struct{})}
	ln.tokens <- struct{}{}
	result := make(chan error, 1)
	go func() {
		_, err := ln.Accept()
		result <- err
	}()
	ln.Close()
	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("blocked accept returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing the listener did not unblock an at-capacity accept")
	}
}

func TestConnectionLimiterReleasesExactlyOnce(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	tokens := make(chan struct{}, 1)
	tokens <- struct{}{}
	conn := &limitedConn{Conn: a, release: func() { <-tokens }}
	conn.Close()
	conn.Close()
	if len(tokens) != 0 {
		t.Fatal("closed connection retained a capacity token")
	}
}
