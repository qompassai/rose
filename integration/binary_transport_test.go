package integration

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/qompassai/rose/internal/transport"
)

// This opt-in smoke test starts the built Rose server with isolated storage.
// It exercises real route registration and shutdown but never downloads a model.
func TestRoseBinaryTransport(t *testing.T) {
	binary := os.Getenv("ROSE_BINARY")
	if binary == "" {
		t.Skip("set ROSE_BINARY to an absolute built Rose executable")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("ROSE_BINARY must be absolute")
	}
	serverFiles, clientFiles := transportIdentities(t)
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			endpoint := &url.URL{Scheme: scheme, Host: ln.Addr().String()}
			if err := ln.Close(); err != nil {
				t.Fatal(err)
			}
			serverIdentity, clientIdentity := serverFiles, clientFiles
			if scheme == "http" {
				serverIdentity, clientIdentity = transport.Files{}, transport.Files{}
			}
			client, err := transport.NewHTTPClient(endpoint, clientIdentity)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			home := t.TempDir()
			cmd := exec.CommandContext(ctx, binary, "serve")
			cmd.Dir = home
			cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home,
				"ROSE_HOST="+endpoint.String(), "ROSE_MODELS="+filepath.Join(home, "models"),
				"ROSE_TLS_CERT="+serverIdentity.CertFile, "ROSE_TLS_KEY="+serverIdentity.KeyFile,
				"ROSE_TLS_CLIENT_CA="+serverIdentity.ClientCAFile, "ROSE_TLS_CA=",
				"ROSE_NO_PRUNE=true", "ROSE_DEBUG=false",
			)
			output := &boundedTestOutput{}
			cmd.Stdout, cmd.Stderr = output, output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				cancel()
				if !waited {
					_ = cmd.Wait()
				}
				if t.Failed() {
					t.Log(output.String())
				}
			}()
			waitForRoseVersion(t, ctx, client, endpoint)
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String()+"/debug/pprof/", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("global pprof route exposed: HTTP %d", response.StatusCode)
			}
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			if err != nil {
				t.Fatalf("server did not shut down cleanly: %v", err)
			}
		})
	}
}

func waitForRoseVersion(t *testing.T, ctx context.Context, client *http.Client, endpoint *url.URL) {
	t.Helper()
	var lastError error
	for range 100 {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String()+"/api/version", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			lastError = err
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		var version struct{ Version, Backend, Protocol string }
		err = json.NewDecoder(http.MaxBytesReader(nil, response.Body, 4096)).Decode(&version)
		closeErr := response.Body.Close()
		if err != nil || closeErr != nil || response.StatusCode != http.StatusOK ||
			version.Backend != "rose" || version.Protocol != "rose-hybrid-mtls-v1" {
			t.Fatalf("unexpected version endpoint: HTTP %d %+v decode=%v close=%v",
				response.StatusCode, version, err, closeErr)
		}
		if endpoint.Scheme == "https" && (response.TLS == nil ||
			response.TLS.Version != tls.VersionTLS13 || response.TLS.CurveID != tls.X25519MLKEM768 ||
			len(response.TLS.VerifiedChains) == 0) {
			t.Fatal("built backend did not negotiate verified hybrid TLS 1.3")
		}
		return
	}
	t.Fatal(fmt.Errorf("Rose did not become ready within bounded retries: %w", lastError))
}
