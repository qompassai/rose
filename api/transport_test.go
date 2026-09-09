package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/qompassai/rose/internal/transport"
)

func clearTLSEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ROSE_TLS_CERT", "ROSE_TLS_KEY", "ROSE_TLS_CLIENT_CA", "ROSE_TLS_CA"} {
		t.Setenv(name, "")
	}
}

func TestHardenedClientEnvironment(t *testing.T) {
	clearTLSEnvironment(t)
	t.Setenv("ROSE_HOST", "")
	client, err := ClientFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	if client.http == http.DefaultClient || client.base.String() != "http://127.0.0.1:11434" {
		t.Fatal("environment client did not use isolated literal-loopback defaults")
	}
	for _, value := range []string{"http://0.0.0.0:11434", "http://localhost:11434", "http://192.0.2.1:11434", "https://example.com"} {
		t.Setenv("ROSE_HOST", value)
		if _, err := ClientFromEnvironment(); err == nil {
			t.Errorf("accepted insecure configuration %q", value)
		}
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type repeatingBody struct {
	count  int
	closed bool
}

func (b *repeatingBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	b.count += len(p)
	return len(p), nil
}

func (b *repeatingBody) Close() error { b.closed = true; return nil }

func TestOrdinaryJSONResponseBound(t *testing.T) {
	body := &repeatingBody{}
	base, err := url.Parse("http://127.0.0.1:11434")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(base, &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})})
	if _, err := client.Version(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unbounded ordinary JSON response: %v", err)
	}
	if body.count != transport.MaxJSONResponseBytes+1 || !body.closed {
		t.Fatalf("read %d bytes, closed=%v", body.count, body.closed)
	}
}

func TestBlobUploadNotCappedByJSONResponseBound(t *testing.T) {
	want := int64(transport.MaxJSONResponseBytes + 1024)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil || n != want {
			t.Errorf("blob received=%d err=%v", n, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()
	clearTLSEnvironment(t)
	t.Setenv("ROSE_HOST", ts.URL)
	client, err := ClientFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body := io.LimitReader(&repeatingBody{}, want)
	if err := client.CreateBlob(ctx, "test-digest", body); err != nil {
		t.Fatal(err)
	}
}

func TestStreamReadFailuresPropagate(t *testing.T) {
	base, err := url.Parse("http://127.0.0.1:11434")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []io.ReadCloser{
		io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), maxBufferSize+1))),
		io.NopCloser(errorReader{}),
	} {
		client := NewClient(base, &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
		})})
		if err := client.Generate(context.Background(), &GenerateRequest{}, func(GenerateResponse) error { return nil }); err == nil {
			t.Fatal("stream swallowed a scanner error")
		}
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestVersionMetadataCompatibility(t *testing.T) {
	for _, payload := range []string{`{"version":"1.2.3"}`, `{"version":"1.2.3","backend":"rose","protocol":"rose-hybrid-mtls-v1"}`} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, payload)
		}))
		base, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatal(err)
		}
		client := NewClient(base, ts.Client())
		version, err := client.Version(context.Background())
		if err != nil || version != "1.2.3" {
			t.Errorf("version compatibility failed: version=%q err=%v", version, err)
		}
		info, err := client.VersionInfo(context.Background())
		if err != nil || info.Version != "1.2.3" {
			t.Errorf("typed metadata failed: info=%v err=%v", info, err)
		}
		ts.Close()
	}
}
