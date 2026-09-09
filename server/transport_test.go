package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qompassai/rose/api"
	"github.com/qompassai/rose/internal/transport"
)

func clearServerTLS(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ROSE_TLS_CERT", "ROSE_TLS_KEY", "ROSE_TLS_CLIENT_CA", "ROSE_TLS_CA"} {
		t.Setenv(name, "")
	}
}

func TestServeRejectsActualPlaintextRemoteBeforeSideEffects(t *testing.T) {
	clearServerTLS(t)
	t.Setenv("ROSE_HOST", "")
	models := filepath.Join(t.TempDir(), "must-not-exist")
	t.Setenv("ROSE_MODELS", models)
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := Serve(ln); err == nil {
		t.Fatal("configured loopback bypassed the actual wildcard listener check")
	}
	if _, err := os.Stat(models); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid server configuration caused model filesystem effects: %v", err)
	}
	if _, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("rejected listener was not closed: %v", err)
	}
}

func TestServeRejectsTLSMisconfigurationAndClosesListener(t *testing.T) {
	cases := []struct{ host, cert, key, ca string }{
		{host: "https://127.0.0.1:11434"},
		{host: "http://192.0.2.1:11434"},
		{host: "http://localhost:11434"},
		{host: "http://127.0.0.1:11434", cert: "nonexistent"},
		{host: "https://127.0.0.1:11434", cert: "nonexistent", key: "nonexistent", ca: "nonexistent"},
	}
	for _, tt := range cases {
		t.Run(tt.host+tt.cert, func(t *testing.T) {
			clearServerTLS(t)
			t.Setenv("ROSE_HOST", tt.host)
			t.Setenv("ROSE_TLS_CERT", tt.cert)
			t.Setenv("ROSE_TLS_KEY", tt.key)
			t.Setenv("ROSE_TLS_CLIENT_CA", tt.ca)
			models := filepath.Join(t.TempDir(), "must-not-exist")
			t.Setenv("ROSE_MODELS", models)
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			if err := Serve(ln); err == nil {
				t.Fatal("misconfiguration accepted")
			}
			if _, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("listener leaked on validation failure: %v", err)
			}
			if _, err := os.Stat(models); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("filesystem modified before TLS validation: %v", err)
			}
		})
	}
}

func TestHTTPServerIsolatesRoutesAndBoundsHeaders(t *testing.T) {
	previous := http.DefaultServeMux
	http.DefaultServeMux = http.NewServeMux()
	t.Cleanup(func() { http.DefaultServeMux = previous })
	http.HandleFunc("/debug/pprof/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	s := &Server{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 11434}}
	h, err := s.GenerateRoutes(nil)
	if err != nil {
		t.Fatal(err)
	}
	srvr := newHTTPServer(h, context.Background())
	if srvr.Handler == nil || srvr.ReadHeaderTimeout != transport.ReadHeaderTimeout ||
		srvr.IdleTimeout != transport.IdleTimeout || srvr.MaxHeaderBytes != transport.MaxHeaderBytes {
		t.Fatal("HTTP server lost handler isolation or named resource bounds")
	}
	recorder := httptest.NewRecorder()
	srvr.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/debug/pprof/", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("default mux handler exposed: status=%d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	srvr.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/version", nil))
	var response api.VersionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || response.Backend != "rose" || response.Protocol != "rose-hybrid-mtls-v1" {
		t.Fatalf("wrong backend metadata: %+v status=%d", response, recorder.Code)
	}
}

func TestHTTPServeErrorCleansUpWithoutSignal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	srvr := newHTTPServer(http.NotFoundHandler(), context.Background())
	result := make(chan error, 1)
	go func() { result <- serveHTTP(context.Background(), srvr, ln) }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("unexpected listener failure was hidden")
		}
	case <-time.After(time.Second):
		t.Fatal("serve error waited indefinitely for a signal")
	}
	fresh, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err := srvr.Serve(fresh); !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("HTTP server was not closed after serve error: %v", err)
	}
}

func TestHTTPContextCancellationClosesListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srvr := newHTTPServer(http.NotFoundHandler(), ctx)
	result := make(chan error, 1)
	go func() { result <- serveHTTP(ctx, srvr, ln) }()
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not stop HTTP serving")
	}
	if _, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listener leaked after cancellation: %v", err)
	}
}
