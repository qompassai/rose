package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/qompassai/rose/api"
	"github.com/qompassai/rose/manifest"
	"github.com/qompassai/rose/types/model"
)

// unionRig steers registry traffic for the three union hosts to local
// test servers and records every request path per host.
type unionRig struct {
	mu       sync.Mutex
	paths    map[string][]string
	failDial map[string]bool
	ports    map[string]string
}

func newUnionRig(t *testing.T, handlers map[string]http.Handler) *unionRig {
	t.Helper()

	rig := &unionRig{
		paths:    map[string][]string{},
		failDial: map[string]bool{},
		ports:    map[string]string{},
	}
	for host, handler := range handlers {
		host := host
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rig.mu.Lock()
			rig.paths[host] = append(rig.paths[host], r.Method+" "+r.URL.Path)
			rig.mu.Unlock()
			handler.ServeHTTP(w, r)
		}))
		t.Cleanup(srv.Close)
		_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		rig.ports[host] = port
	}

	prev := testMakeRequestDialContext
	testMakeRequestDialContext = rig.dial
	t.Cleanup(func() { testMakeRequestDialContext = prev })
	return rig
}

func (r *unionRig) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if r.failDial[host] {
		return nil, fmt.Errorf("dial %s: connection refused (test)", addr)
	}
	port, ok := r.ports[host]
	if !ok {
		return nil, fmt.Errorf("no test registry registered for host %q", host)
	}
	return new(net.Dialer).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
}

func (r *unionRig) hits(host string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.paths[host])
}

func (r *unionRig) sawPath(host, substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.paths[host] {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

func serveManifest(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/manifests/") {
			w.Header().Set("Content-Type", manifest.MediaTypeManifest)
			w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	})
}

func notFoundHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
}

const unionTestManifest = `{"schemaVersion":2,"mediaType":"application/vnd.ollama.image.manifest.v1+json","config":{"mediaType":"application/vnd.ollama.image.config","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":2},"layers":[]}`

func TestUnionCandidates(t *testing.T) {
	t.Setenv("ROSE_MODELS", t.TempDir())

	t.Run("public namespace tries harbor first then public hosts", func(t *testing.T) {
		candidates, err := unionCandidates(model.ParseName("library/model:latest"))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"harbor.qompass.ai", "registry.ollama.ai", "ollama.com"}
		if len(candidates) != len(want) {
			t.Fatalf("got %d candidates, want %d", len(candidates), len(want))
		}
		for i, host := range want {
			if candidates[i].Host != host {
				t.Fatalf("candidate %d host = %q, want %q", i, candidates[i].Host, host)
			}
		}
	})

	t.Run("declared private namespace is harbor only", func(t *testing.T) {
		candidates, err := unionCandidates(model.ParseName("qompassai/model:latest"))
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) != 1 || candidates[0].Host != "harbor.qompass.ai" {
			t.Fatalf("candidates = %v, want harbor only", candidates)
		}
	})
}

func TestPushTargetName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare name pushes to harbor", "model", "harbor.qompass.ai"},
		{"public host name pushes to harbor", "registry.ollama.ai/library/model", "harbor.qompass.ai"},
		{"ollama.com name pushes to harbor", "ollama.com/library/model", "harbor.qompass.ai"},
		{"explicit third-party host is kept", "example.com/ns/model", "example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pushTargetName(model.ParseName(tc.in)); got.Host != tc.want {
				t.Fatalf("pushTargetName(%q).Host = %q, want %q", tc.in, got.Host, tc.want)
			}
		})
	}
}

func TestResolvePullManifestUnion(t *testing.T) {
	digestOf := func(s string) string { return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(s))) }

	t.Run("harbor hit never contacts public hosts", func(t *testing.T) {
		t.Setenv("ROSE_MODELS", t.TempDir())
		rig := newUnionRig(t, map[string]http.Handler{
			"harbor.qompass.ai":  serveManifest(unionTestManifest),
			"registry.ollama.ai": serveManifest(unionTestManifest),
			"ollama.com":         serveManifest(unionTestManifest),
		})

		n := model.ParseName("test-model")
		_, data, resolved, err := resolvePullManifest(t.Context(), n, &registryOptions{Insecure: true})
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != unionTestManifest {
			t.Fatal("returned manifest differs from the served manifest")
		}
		if resolved.Host != "harbor.qompass.ai" {
			t.Fatalf("resolved host = %q, want harbor.qompass.ai", resolved.Host)
		}
		if rig.hits("registry.ollama.ai") != 0 || rig.hits("ollama.com") != 0 {
			t.Fatalf("public hosts contacted on a harbor hit: registry.ollama.ai=%d ollama.com=%d",
				rig.hits("registry.ollama.ai"), rig.hits("ollama.com"))
		}

		pin, ok, err := manifest.ReadResolutionPin(n)
		if err != nil || !ok {
			t.Fatalf("pin not recorded: ok=%v err=%v", ok, err)
		}
		if pin.Host != "harbor.qompass.ai" || pin.Digest != digestOf(unionTestManifest) {
			t.Fatalf("pin = %+v, want harbor + %s", pin, digestOf(unionTestManifest))
		}
	})

	t.Run("harbor 404 falls through to registry.ollama.ai only", func(t *testing.T) {
		t.Setenv("ROSE_MODELS", t.TempDir())
		rig := newUnionRig(t, map[string]http.Handler{
			"harbor.qompass.ai":  notFoundHandler(),
			"registry.ollama.ai": serveManifest(unionTestManifest),
			"ollama.com":         serveManifest(unionTestManifest),
		})

		n := model.ParseName("test-model")
		_, _, resolved, err := resolvePullManifest(t.Context(), n, &registryOptions{Insecure: true})
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Host != "registry.ollama.ai" {
			t.Fatalf("resolved host = %q, want registry.ollama.ai", resolved.Host)
		}
		if rig.hits("ollama.com") != 0 {
			t.Fatal("ollama.com contacted after registry.ollama.ai answered")
		}
		pin, ok, err := manifest.ReadResolutionPin(n)
		if err != nil || !ok || pin.Host != "registry.ollama.ai" {
			t.Fatalf("pin = %+v ok=%v err=%v, want registry.ollama.ai pin", pin, ok, err)
		}
	})

	t.Run("two 404s fall through to ollama.com", func(t *testing.T) {
		t.Setenv("ROSE_MODELS", t.TempDir())
		newUnionRig(t, map[string]http.Handler{
			"harbor.qompass.ai":  notFoundHandler(),
			"registry.ollama.ai": notFoundHandler(),
			"ollama.com":         serveManifest(unionTestManifest),
		})

		_, _, resolved, err := resolvePullManifest(t.Context(), model.ParseName("test-model"), &registryOptions{Insecure: true})
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Host != "ollama.com" {
			t.Fatalf("resolved host = %q, want ollama.com", resolved.Host)
		}
	})

	t.Run("404 everywhere is not found", func(t *testing.T) {
		t.Setenv("ROSE_MODELS", t.TempDir())
		newUnionRig(t, map[string]http.Handler{
			"harbor.qompass.ai":  notFoundHandler(),
			"registry.ollama.ai": notFoundHandler(),
			"ollama.com":         notFoundHandler(),
		})

		_, _, _, err := resolvePullManifest(t.Context(), model.ParseName("test-model"), &registryOptions{Insecure: true})
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v, want os.ErrNotExist", err)
		}
	})

	t.Run("harbor network error surfaces and never falls through", func(t *testing.T) {
		t.Setenv("ROSE_MODELS", t.TempDir())
		rig := newUnionRig(t, map[string]http.Handler{
			"harbor.qompass.ai":  serveManifest(unionTestManifest),
			"registry.ollama.ai": serveManifest(unionTestManifest),
			"ollama.com":         serveManifest(unionTestManifest),
		})
		rig.failDial["harbor.qompass.ai"] = true

		_, _, _, err := resolvePullManifest(t.Context(), model.ParseName("test-model"), &registryOptions{Insecure: true})
		if err == nil {
			t.Fatal("expected the network error to surface")
		}
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("network error must not be reported as not-found: %v", err)
		}
		if rig.hits("registry.ollama.ai") != 0 || rig.hits("ollama.com") != 0 {
			t.Fatal("public hosts contacted after a harbor network error")
		}
	})

	t.Run("harbor 403 surfaces and never falls through", func(t *testing.T) {
		t.Setenv("ROSE_MODELS", t.TempDir())
		rig := newUnionRig(t, map[string]http.Handler{
			"harbor.qompass.ai": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "forbidden", http.StatusForbidden)
			}),
			"registry.ollama.ai": serveManifest(unionTestManifest),
			"ollama.com":         serveManifest(unionTestManifest),
		})

		_, _, _, err := resolvePullManifest(t.Context(), model.ParseName("test-model"), &registryOptions{Insecure: true})
		if err == nil || !strings.Contains(err.Error(), "403") {
			t.Fatalf("err = %v, want a surfaced 403", err)
		}
		if rig.hits("registry.ollama.ai") != 0 || rig.hits("ollama.com") != 0 {
			t.Fatal("public hosts contacted after a harbor auth error")
		}
	})

	t.Run("harbor 401 surfaces and never falls through", func(t *testing.T) {
		t.Setenv("ROSE_MODELS", t.TempDir())
		rig := newUnionRig(t, map[string]http.Handler{
			"harbor.qompass.ai": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// A challenge whose realm is on another host makes
				// the token exchange fail deterministically.
				w.Header().Set("WWW-Authenticate", `Bearer realm="https://auth.invalid/token",service="registry",scope="repository:library/test-model:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
			}),
			"registry.ollama.ai": serveManifest(unionTestManifest),
			"ollama.com":         serveManifest(unionTestManifest),
		})

		_, _, _, err := resolvePullManifest(t.Context(), model.ParseName("test-model"), &registryOptions{Insecure: true})
		if err == nil {
			t.Fatal("expected the auth failure to surface")
		}
		if rig.hits("registry.ollama.ai") != 0 || rig.hits("ollama.com") != 0 {
			t.Fatal("public hosts contacted after a harbor auth failure")
		}
	})

	t.Run("private namespace 404 on harbor never falls through", func(t *testing.T) {
		t.Setenv("ROSE_MODELS", t.TempDir())
		rig := newUnionRig(t, map[string]http.Handler{
			"harbor.qompass.ai":  notFoundHandler(),
			"registry.ollama.ai": serveManifest(unionTestManifest),
			"ollama.com":         serveManifest(unionTestManifest),
		})

		_, _, _, err := resolvePullManifest(t.Context(), model.ParseName("qompassai/secret-model"), &registryOptions{Insecure: true})
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v, want os.ErrNotExist", err)
		}
		if rig.hits("registry.ollama.ai") != 0 || rig.hits("ollama.com") != 0 {
			t.Fatal("public hosts contacted for a private namespace")
		}
	})

	t.Run("namespace pinned harbor-only stays harbor-only", func(t *testing.T) {
		t.Setenv("ROSE_MODELS", t.TempDir())
		rig := newUnionRig(t, map[string]http.Handler{
			"harbor.qompass.ai":  notFoundHandler(),
			"registry.ollama.ai": serveManifest(unionTestManifest),
			"ollama.com":         serveManifest(unionTestManifest),
		})

		// A prior Harbor resolution in this namespace makes the whole
		// namespace Harbor-only even though it is not in the declared
		// private set.
		seed := model.ParseName("acme/first-model")
		if err := manifest.WriteResolutionPin(seed, manifest.ResolutionPin{Host: "harbor.qompass.ai", Digest: digestOf(unionTestManifest)}); err != nil {
			t.Fatal(err)
		}

		_, _, _, err := resolvePullManifest(t.Context(), model.ParseName("acme/second-model"), &registryOptions{Insecure: true})
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v, want os.ErrNotExist", err)
		}
		if rig.hits("registry.ollama.ai") != 0 || rig.hits("ollama.com") != 0 {
			t.Fatal("public hosts contacted for a harbor-pinned namespace")
		}
	})
}

func TestResolvePullManifestPin(t *testing.T) {
	const manifestV2 = `{"schemaVersion":2,"mediaType":"application/vnd.ollama.image.manifest.v1+json","config":{"mediaType":"application/vnd.ollama.image.config","digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":2},"layers":[]}`
	digestV1 := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(unionTestManifest)))

	t.Setenv("ROSE_MODELS", t.TempDir())

	// The tag serves v1 first and v2 after it "moves"; the pinned
	// digest keeps serving v1.
	var moved bool
	harbor := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/"+digestV1):
			w.Header().Set("Content-Type", manifest.MediaTypeManifest)
			w.Write([]byte(unionTestManifest))
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Content-Type", manifest.MediaTypeManifest)
			if moved {
				w.Write([]byte(manifestV2))
			} else {
				w.Write([]byte(unionTestManifest))
			}
		default:
			http.NotFound(w, r)
		}
	})
	rig := newUnionRig(t, map[string]http.Handler{
		"harbor.qompass.ai":  harbor,
		"registry.ollama.ai": notFoundHandler(),
		"ollama.com":         notFoundHandler(),
	})

	n := model.ParseName("pin-model")
	regOpts := &registryOptions{Insecure: true}

	_, data, _, err := resolvePullManifest(t.Context(), n, regOpts)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != unionTestManifest {
		t.Fatal("first resolve did not return v1")
	}

	// The tag moves upstream; the second resolve must serve the pinned
	// digest, fetched by digest, not the moved tag.
	moved = true
	_, data, resolved, err := resolvePullManifest(t.Context(), n, regOpts)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != unionTestManifest {
		t.Fatal("second resolve returned the moved tag's manifest instead of the pinned digest")
	}
	if resolved.Host != "harbor.qompass.ai" {
		t.Fatalf("resolved host = %q, want the pinned harbor.qompass.ai", resolved.Host)
	}
	if !rig.sawPath("harbor.qompass.ai", "/manifests/"+digestV1) {
		t.Fatal("second resolve did not fetch the manifest by its pinned digest")
	}

	// Deleting the pin is the explicit re-resolution action: the next
	// resolve follows the tag again and returns v2.
	if err := manifest.DeleteResolutionPin(n); err != nil {
		t.Fatal(err)
	}
	_, data, _, err = resolvePullManifest(t.Context(), n, regOpts)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != manifestV2 {
		t.Fatal("resolve after pin deletion did not follow the moved tag")
	}
	pin, ok, err := manifest.ReadResolutionPin(n)
	if err != nil || !ok {
		t.Fatalf("pin not re-recorded after re-resolution: ok=%v err=%v", ok, err)
	}
	if want := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(manifestV2))); pin.Digest != want {
		t.Fatalf("re-recorded pin digest = %s, want %s", pin.Digest, want)
	}
}

func TestPushModelHarborFailureNeverPublic(t *testing.T) {
	t.Setenv("ROSE_MODELS", t.TempDir())

	// A local model with a single config blob, written under the
	// ollama.com spelling of the union identity.
	configDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("{}")))
	configPath, err := manifest.BlobsPath(configDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	localManifest := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.ollama.image.manifest.v1+json","config":{"mediaType":"application/vnd.ollama.image.config","digest":%q,"size":2},"layers":[]}`, configDigest)
	name := model.ParseName("ollama.com/library/push-model:latest")
	if err := manifest.WriteManifestData(name, []byte(localManifest)); err != nil {
		t.Fatal(err)
	}

	rig := newUnionRig(t, map[string]http.Handler{
		"harbor.qompass.ai": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "harbor unavailable", http.StatusInternalServerError)
		}),
		"registry.ollama.ai": serveManifest(unionTestManifest),
		"ollama.com":         serveManifest(unionTestManifest),
	})

	err = PushModel(t.Context(), "ollama.com/library/push-model:latest", &registryOptions{Insecure: true}, func(api.ProgressResponse) {})
	if err == nil {
		t.Fatal("expected the harbor failure to fail the push")
	}
	if rig.hits("harbor.qompass.ai") == 0 {
		t.Fatal("push never attempted harbor; the union target rewrite did not happen")
	}
	if rig.hits("registry.ollama.ai") != 0 || rig.hits("ollama.com") != 0 {
		t.Fatal("push contacted a public host after the harbor failure")
	}
}
