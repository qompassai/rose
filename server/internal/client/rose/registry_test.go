package rose

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qompassai/rose/server/internal/cache/blob"
	"github.com/qompassai/rose/server/internal/testutil"
)

func ExampleRegistry_cancelOnFirstError() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx = WithTrace(ctx, &Trace{
		Update: func(l *Layer, n int64, err error) {
			if err != nil {
				// Discontinue pulling layers if there is an
				// error instead of continuing to pull more
				// data.
				cancel()
			}
		},
	})

	var r Registry
	if err := r.Pull(ctx, "model"); err != nil {
		// panic for demo purposes
		panic(err)
	}
}

func TestManifestMarshalJSON(t *testing.T) {
	// All manifests should contain an "empty" config object.
	var m Manifest
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"config":{"digest":"sha256:`)) {
		t.Error("expected manifest to contain empty config")
		t.Fatalf("got:\n%s", string(data))
	}
}

var errRoundTrip = errors.New("forced roundtrip error")

type recordRoundTripper http.HandlerFunc

func (rr recordRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	rr(w, req)
	if w.Code == 499 {
		return nil, errRoundTrip
	}
	resp := w.Result()
	// For some reason, Response.Request is not set by httptest.NewRecorder, so we
	// set it manually.
	resp.Request = req
	return w.Result(), nil
}

// newClient constructs a cache with predefined manifests for testing. The manifests are:
//
//	empty:         no data
//	zero:          no layers
//	single:        one layer with the contents "exists"
//	multiple:      two layers with the contents "exists" and "here"
//	notfound:      a layer that does not exist in the cache
//	null:          one null layer (e.g. [null])
//	sizemismatch:  one valid layer, and one with a size mismatch (file size is less than the reported size)
//	invalid:       a layer with invalid JSON data
//
// Tests that want to ensure the client does not communicate with the upstream
// registry should pass a nil handler, which will cause a panic if
// communication is attempted.
//
// To simulate a network error, pass a handler that returns a 499 status code.
func newClient(t *testing.T, upstreamRegistry http.HandlerFunc) (*Registry, *blob.DiskCache) {
	t.Helper()

	c, err := blob.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	mklayer := func(data string) *Layer {
		return &Layer{
			Digest: importBytes(t, c, data),
			Size:   int64(len(data)),
		}
	}

	r := &Registry{
		Cache: c,
		HTTPClient: &http.Client{
			Transport: recordRoundTripper(upstreamRegistry),
		},
	}

	link := func(name string, manifest string) {
		n, err := r.parseName(name)
		if err != nil {
			panic(err)
		}
		d, err := c.Import(bytes.NewReader([]byte(manifest)), int64(len(manifest)))
		if err != nil {
			panic(err)
		}
		if err := c.Link(n.String(), d); err != nil {
			panic(err)
		}
	}

	commit := func(name string, layers ...*Layer) {
		t.Helper()
		data, err := json.Marshal(&Manifest{Layers: layers})
		if err != nil {
			t.Fatal(err)
		}
		link(name, string(data))
	}

	link("empty", "")
	commit("zero")
	commit("single", mklayer("exists"))
	commit("multiple", mklayer("exists"), mklayer("present"))
	commit("notfound", &Layer{Digest: blob.DigestFromBytes("notfound"), Size: int64(len("notfound"))})
	commit("null", nil)
	commit("sizemismatch", mklayer("exists"), &Layer{Digest: blob.DigestFromBytes("present"), Size: 499})
	link("invalid", "!!!!!")

	return r, c
}

func okHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func checkErrCode(t *testing.T, err error, status int, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Status != status || e.Code != code {
		t.Errorf("err = %v; want %v %v", err, status, code)
	}
}

func importBytes(t *testing.T, c *blob.DiskCache, data string) blob.Digest {
	d, err := c.Import(strings.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func withTraceUnexpected(ctx context.Context) (context.Context, *Trace) {
	t := &Trace{Update: func(*Layer, int64, error) { panic("unexpected") }}
	return WithTrace(ctx, t), t
}

func TestPushZero(t *testing.T) {
	rc, _ := newClient(t, okHandler)
	err := rc.Push(t.Context(), "empty", nil)
	if !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("err = %v; want %v", err, ErrManifestInvalid)
	}
}

func TestPushSingle(t *testing.T) {
	rc, _ := newClient(t, okHandler)
	err := rc.Push(t.Context(), "single", nil)
	testutil.Check(t, err)
}

func TestPushMultiple(t *testing.T) {
	rc, _ := newClient(t, okHandler)
	err := rc.Push(t.Context(), "multiple", nil)
	testutil.Check(t, err)
}

func TestPushNotFound(t *testing.T) {
	rc, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %v", r)
	})
	err := rc.Push(t.Context(), "notfound", nil)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v; want %v", err, fs.ErrNotExist)
	}
}

func TestPushNullLayer(t *testing.T) {
	rc, _ := newClient(t, nil)
	err := rc.Push(t.Context(), "null", nil)
	if err == nil || !strings.Contains(err.Error(), "invalid manifest") {
		t.Errorf("err = %v; want invalid manifest", err)
	}
}

func TestPushSizeMismatch(t *testing.T) {
	rc, _ := newClient(t, nil)
	ctx, _ := withTraceUnexpected(t.Context())
	got := rc.Push(ctx, "sizemismatch", nil)
	if got == nil || !strings.Contains(got.Error(), "size mismatch") {
		t.Errorf("err = %v; want size mismatch", got)
	}
}

func TestPushInvalid(t *testing.T) {
	rc, _ := newClient(t, nil)
	err := rc.Push(t.Context(), "invalid", nil)
	if err == nil || !strings.Contains(err.Error(), "invalid manifest") {
		t.Errorf("err = %v; want invalid manifest", err)
	}
}

func TestPushExistsAtRemote(t *testing.T) {
	var pushed bool
	rc, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/uploads/") {
			if !pushed {
				// First push. Return an uploadURL.
				pushed = true
				w.Header().Set("Location", "http://blob.store/blobs/123")
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}

		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	})

	rc.MaxStreams = 1 // prevent concurrent uploads

	var errs []error
	ctx := WithTrace(t.Context(), &Trace{
		Update: func(_ *Layer, n int64, err error) {
			// uploading one at a time so no need to lock
			errs = append(errs, err)
		},
	})

	check := testutil.Checker(t)

	err := rc.Push(ctx, "single", nil)
	check(err)

	if !errors.Is(errors.Join(errs...), nil) {
		t.Errorf("errs = %v; want %v", errs, []error{ErrCached})
	}

	err = rc.Push(ctx, "single", nil)
	check(err)
}

func TestPushRemoteError(t *testing.T) {
	rc, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/blobs/") {
			w.WriteHeader(500)
			io.WriteString(w, `{"errors":[{"code":"blob_error"}]}`)
			return
		}
	})
	got := rc.Push(t.Context(), "single", nil)
	checkErrCode(t, got, 500, "blob_error")
}

func TestPushLocationError(t *testing.T) {
	rc, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", ":///x")
		w.WriteHeader(http.StatusAccepted)
	})
	got := rc.Push(t.Context(), "single", nil)
	wantContains := "invalid upload URL"
	if got == nil || !strings.Contains(got.Error(), wantContains) {
		t.Errorf("err = %v; want to contain %v", got, wantContains)
	}
}

func TestPushUploadRoundtripError(t *testing.T) {
	rc, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "blob.store" {
			w.WriteHeader(499) // force RoundTrip error on upload
			return
		}
		w.Header().Set("Location", "http://blob.store/blobs/123")
	})
	got := rc.Push(t.Context(), "single", nil)
	if !errors.Is(got, errRoundTrip) {
		t.Errorf("got = %v; want %v", got, errRoundTrip)
	}
}

func TestPushUploadFileOpenError(t *testing.T) {
	rc, c := newClient(t, okHandler)
	ctx := WithTrace(t.Context(), &Trace{
		Update: func(l *Layer, _ int64, err error) {
			// Remove the file just before it is opened for upload,
			// but after the initial Stat that happens before the
			// upload starts
			os.Remove(c.GetFile(l.Digest))
		},
	})
	got := rc.Push(ctx, "single", nil)
	if !errors.Is(got, fs.ErrNotExist) {
		t.Errorf("got = %v; want fs.ErrNotExist", got)
	}
}

func TestPushCommitRoundtripError(t *testing.T) {
	rc, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/blobs/") {
			panic("unexpected")
		}
		w.WriteHeader(499) // force RoundTrip error
	})
	err := rc.Push(t.Context(), "zero", nil)
	if !errors.Is(err, errRoundTrip) {
		t.Errorf("err = %v; want %v", err, errRoundTrip)
	}
}

func checkNotExist(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v; want fs.ErrNotExist", err)
	}
}

func TestRegistryPullInvalidName(t *testing.T) {
	rc, _ := newClient(t, nil)
	err := rc.Pull(t.Context(), "://")
	if !errors.Is(err, ErrNameInvalid) {
		t.Errorf("err = %v; want %v", err, ErrNameInvalid)
	}
}

func TestRegistryPullInvalidManifest(t *testing.T) {
	cases := []string{
		"",
		"null",
		"!!!",
		`{"layers":[]}`,
	}

	for _, resp := range cases {
		rc, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, resp)
		})
		err := rc.Pull(t.Context(), "x")
		if !errors.Is(err, ErrManifestInvalid) {
			t.Errorf("err = %v; want invalid manifest", err)
		}
	}
}

func TestRegistryPullNotCached(t *testing.T) {
	check := testutil.Checker(t)

	var c *blob.DiskCache
	var rc *Registry

	d := blob.DigestFromBytes("some data")
	rc, c = newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/blobs/") {
			io.WriteString(w, "some data")
			return
		}
		fmt.Fprintf(w, `{"layers":[{"digest":%q,"size":9}]}`, d)
	})

	// Confirm that the layer does not exist locally
	_, err := rc.ResolveLocal("model")
	checkNotExist(t, err)

	_, err = c.Get(d)
	checkNotExist(t, err)

	err = rc.Pull(t.Context(), "model")
	check(err)

	mw, err := rc.Resolve(t.Context(), "model")
	check(err)
	mg, err := rc.ResolveLocal("model")
	check(err)
	if !reflect.DeepEqual(mw, mg) {
		t.Errorf("mw = %v; mg = %v", mw, mg)
	}

	// Confirm successful download
	info, err := c.Get(d)
	check(err)
	if info.Digest != d {
		t.Errorf("info.Digest = %v; want %v", info.Digest, d)
	}
	if info.Size != 9 {
		t.Errorf("info.Size = %v; want %v", info.Size, 9)
	}

	data, err := os.ReadFile(c.GetFile(d))
	check(err)
	if string(data) != "some data" {
		t.Errorf("data = %q; want %q", data, "exists")
	}
}

func TestRegistryPullCached(t *testing.T) {
	cached := blob.DigestFromBytes("exists")
	rc, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/blobs/") {
			w.WriteHeader(499) // should not be called
			return
		}
		if strings.Contains(r.URL.Path, "/manifests/") {
			fmt.Fprintf(w, `{"layers":[{"digest":%q,"size":6}]}`, cached)
		}
	})

	var errs []error
	var reads []int64
	ctx := WithTrace(t.Context(), &Trace{
		Update: func(d *Layer, n int64, err error) {
			t.Logf("update %v %d %v", d, n, err)
			reads = append(reads, n)
			errs = append(errs, err)
		},
	})

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	err := rc.Pull(ctx, "single")
	testutil.Check(t, err)

	want := []int64{0, 6}
	if !errors.Is(errors.Join(errs...), ErrCached) {
		t.Errorf("errs = %v; want %v", errs, ErrCached)
	}
	if !slices.Equal(reads, want) {
		t.Errorf("pairs = %v; want %v", reads, want)
	}
}

func TestRegistryPullManifestNotFound(t *testing.T) {
	rc, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	err := rc.Pull(t.Context(), "notfound")
	checkErrCode(t, err, 404, "")
}

func TestRegistryPullResolveRemoteError(t *testing.T) {
	rc, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"errors":[{"code":"an_error"}]}`)
	})
	err := rc.Pull(t.Context(), "single")
	checkErrCode(t, err, 500, "an_error")
}

func TestRegistryPullResolveRoundtripError(t *testing.T) {
	rc, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/manifests/") {
			w.WriteHeader(499) // force RoundTrip error
			return
		}
	})
	err := rc.Pull(t.Context(), "single")
	if !errors.Is(err, errRoundTrip) {
		t.Errorf("err = %v; want %v", err, errRoundTrip)
	}
}

// TestRegistryPullMixedCachedNotCached tests that cached layers do not
// interfere with pulling layers that are not cached
func TestRegistryPullMixedCachedNotCached(t *testing.T) {
	x := blob.DigestFromBytes("xxxxxx")
	e := blob.DigestFromBytes("exists")
	y := blob.DigestFromBytes("yyyyyy")

	for i := range 10 {
		t.Logf("iteration %d", i)

		digests := []blob.Digest{x, e, y}

		rand.Shuffle(len(digests), func(i, j int) {
			digests[i], digests[j] = digests[j], digests[i]
		})

		manifest := fmt.Sprintf(`{
			"layers": [
				{"digest":"%s","size":6},
				{"digest":"%s","size":6},
				{"digest":"%s","size":6}
			]
		}`, digests[0], digests[1], digests[2])

		rc, c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			switch path.Base(r.URL.Path) {
			case "latest":
				io.WriteString(w, manifest)
			case x.String():
				io.WriteString(w, "xxxxxx")
			case e.String():
				io.WriteString(w, "exists")
			case y.String():
				io.WriteString(w, "yyyyyy")
			default:
				panic(fmt.Sprintf("unexpected request: %v", r))
			}
		})

		ctx := WithTrace(t.Context(), &Trace{
			Update: func(l *Layer, n int64, err error) {
				t.Logf("update %v %d %v", l, n, err)
			},
		})

		// Check that we pull all layers that we can.

		err := rc.Pull(ctx, "mixed")
		if err != nil {
			t.Fatal(err)
		}

		for _, d := range digests {
			info, err := c.Get(d)
			if err != nil {
				t.Fatalf("Get(%v): %v", d, err)
			}
			if info.Size != 6 {
				t.Errorf("info.Size = %v; want %v", info.Size, 6)
			}
		}
	}
}

func TestRegistryResolveByDigest(t *testing.T) {
	check := testutil.Checker(t)

	exists := blob.DigestFromBytes("exists")
	rc, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/alice/palace/blobs/"+exists.String() {
			w.WriteHeader(499) // should not hit manifest endpoint
		}
		fmt.Fprintf(w, `{"layers":[{"digest":%q,"size":5}]}`, exists)
	})

	_, err := rc.Resolve(t.Context(), "alice/palace@"+exists.String())
	check(err)
}

func TestInsecureSkipVerify(t *testing.T) {
	exists := blob.DigestFromBytes("exists")

	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"layers":[{"digest":%q,"size":5}]}`, exists)
	}))
	defer s.Close()

	const name = "library/insecure"

	var rc Registry
	url := fmt.Sprintf("https://%s/%s", s.Listener.Addr(), name)
	_, err := rc.Resolve(t.Context(), url)
	if err == nil || !strings.Contains(err.Error(), "failed to verify") {
		t.Errorf("err = %v; want cert verification failure", err)
	}

	url = fmt.Sprintf("https+insecure://%s/%s", s.Listener.Addr(), name)
	_, err = rc.Resolve(t.Context(), url)
	testutil.Check(t, err)
}

func TestCanRetry(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("x"), false},
		{ErrCached, false},
		{ErrManifestInvalid, false},
		{ErrNameInvalid, false},
		{&Error{Status: 100}, false},
		{&Error{Status: 500}, true},
	}
	for _, tt := range cases {
		if got := canRetry(tt.err); got != tt.want {
			t.Errorf("CanRetry(%v) = %v; want %v", tt.err, got, tt.want)
		}
	}
}

func TestErrorUnmarshal(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		want    *Error
		wantErr bool
	}{
		{
			name:    "errors empty",
			data:    `{"errors":[]}`,
			wantErr: true,
		},
		{
			name:    "errors empty",
			data:    `{"errors":[]}`,
			wantErr: true,
		},
		{
			name: "errors single",
			data: `{"errors":[{"code":"blob_unknown"}]}`,
			want: &Error{Code: "blob_unknown", Message: ""},
		},
		{
			name: "errors multiple",
			data: `{"errors":[{"code":"blob_unknown"},{"code":"blob_error"}]}`,
			want: &Error{Code: "blob_unknown", Message: ""},
		},
		{
			name:    "error empty",
			data:    `{"error":""}`,
			wantErr: true,
		},
		{
			name:    "error very empty",
			data:    `{}`,
			wantErr: true,
		},
		{
			name: "error message",
			data: `{"error":"message", "code":"code"}`,
			want: &Error{Code: "code", Message: "message"},
		},
		{
			name:    "invalid value",
			data:    `{"error": 1}`,
			wantErr: true,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var got Error
			err := json.Unmarshal([]byte(tt.data), &got)
			if err != nil {
				if tt.wantErr {
					return
				}
				t.Errorf("Unmarshal() error = %v", err)
				// fallthrough and check got
			}
			if tt.want == nil {
				tt.want = &Error{}
			}
			if !reflect.DeepEqual(got, *tt.want) {
				t.Errorf("got = %v; want %v", got, *tt.want)
			}
		})
	}
}

// TestParseNameErrors tests that parseName returns errors messages with enough
// detail for users to debug naming issues they may encounter. Previous to this
// test, the error messages were not very helpful and each problem was reported
// as the same message.
//
// It is only for testing error messages, not that all invalids and valids are
// covered. Those are in other tests for names.Name and blob.Digest.
func TestParseNameExtendedErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{}

	var r Registry
	for _, tt := range cases {
		_, _, _, err := r.parseNameExtended(tt.name)
		if !errors.Is(err, tt.err) {
			t.Errorf("[%s]: err = %v; want %v", tt.name, err, tt.err)
		}
		if err != nil && !strings.Contains(err.Error(), tt.want) {
			t.Errorf("[%s]: err =\n\t%v\nwant\n\t%v", tt.name, err, tt.want)
		}
	}
}

func TestParseNameExtended(t *testing.T) {
	cases := []struct {
		in     string
		scheme string
		name   string
		digest string
		err    string
	}{
		{in: "http://m", scheme: "http", name: "m"},
		{in: "https+insecure://m", scheme: "https+insecure", name: "m"},
		{in: "http+insecure://m", err: "unsupported scheme"},

		{in: "http://m@sha256:1111111111111111111111111111111111111111111111111111111111111111", scheme: "http", name: "m", digest: "sha256:1111111111111111111111111111111111111111111111111111111111111111"},

		{in: "", err: "invalid or missing name"},
		{in: "m", scheme: "https", name: "m"},
		{in: "://", err: "invalid or missing name"},
		{in: "@sha256:deadbeef", err: "invalid digest"},
		{in: "@sha256:deadbeef@sha256:deadbeef", err: "invalid digest"},
	}
	for _, tt := range cases {
		t.Run(tt.in, func(t *testing.T) {
			var r Registry
			scheme, n, digest, err := r.parseNameExtended(tt.in)
			if err != nil {
				if tt.err == "" {
					t.Errorf("err = %v; want nil", err)
				} else if !strings.Contains(err.Error(), tt.err) {
					t.Errorf("err = %v; want %q", err, tt.err)
				}
			} else if tt.err != "" {
				t.Errorf("err = nil; want %q", tt.err)
			}
			if err == nil && !n.IsFullyQualified() {
				t.Errorf("name = %q; want fully qualified", n)
			}

			if scheme != tt.scheme {
				t.Errorf("scheme = %q; want %q", scheme, tt.scheme)
			}

			// smoke-test name is superset of tt.name
			if !strings.Contains(n.String(), tt.name) {
				t.Errorf("name = %q; want %q", n, tt.name)
			}

			tt.digest = cmp.Or(tt.digest, (&blob.Digest{}).String())
			if digest.String() != tt.digest {
				t.Errorf("digest = %q; want %q", digest, tt.digest)
			}
		})
	}
}

func TestUnlink(t *testing.T) {
	t.Run("found by name", func(t *testing.T) {
		rc, _ := newClient(t, nil)

		// confirm linked
		_, err := rc.ResolveLocal("single")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// unlink
		_, err = rc.Unlink("single")
		testutil.Check(t, err)

		// confirm unlinked
		_, err = rc.ResolveLocal("single")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v; want fs.ErrNotExist", err)
		}
	})
	t.Run("not found by name", func(t *testing.T) {
		rc, _ := newClient(t, nil)
		ok, err := rc.Unlink("manifestNotFound")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Error("expected not found")
		}
	})
}

func TestPullChunksums(t *testing.T) {
	check := testutil.Checker(t)

	content := "hello"
	var chunksums string
	contentDigest := func() blob.Digest {
		return blob.DigestFromBytes(content)
	}
	rc, c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/manifests/latest"):
			fmt.Fprintf(w, `{"layers":[{"digest":%q,"size":%d}]}`, contentDigest(), len(content))
		case strings.HasSuffix(r.URL.Path, "/chunksums/"+contentDigest().String()):
			loc := fmt.Sprintf("http://blob.store/v2/library/test/blobs/%s", contentDigest())
			w.Header().Set("Content-Location", loc)
			io.WriteString(w, chunksums)
		case strings.Contains(r.URL.Path, "/blobs/"+contentDigest().String()):
			http.ServeContent(w, r, contentDigest().String(), time.Time{}, strings.NewReader(content))
		default:
			t.Errorf("unexpected request: %v", r)
			http.NotFound(w, r)
		}
	})

	rc.MaxStreams = 1        // prevent concurrent chunk downloads
	rc.ChunkingThreshold = 1 // for all blobs to be chunked

	var mu sync.Mutex
	var reads []int64
	ctx := WithTrace(t.Context(), &Trace{
		Update: func(l *Layer, n int64, err error) {
			t.Logf("Update: %v %d %v", l, n, err)
			mu.Lock()
			reads = append(reads, n)
			mu.Unlock()
		},
	})

	chunksums = fmt.Sprintf("%s 0-2\n%s 3-4\n",
		blob.DigestFromBytes("hel"),
		blob.DigestFromBytes("lo"),
	)
	err := rc.Pull(ctx, "test")
	check(err)
	wantReads := []int64{
		0, // initial signaling of layer pull starting
		3, // first chunk read
		2, // second chunk read
	}
	if !slices.Equal(reads, wantReads) {
		t.Errorf("reads = %v; want %v", reads, wantReads)
	}

	mw, err := rc.Resolve(t.Context(), "test")
	check(err)
	mg, err := rc.ResolveLocal("test")
	check(err)
	if !reflect.DeepEqual(mw, mg) {
		t.Errorf("mw = %v; mg = %v", mw, mg)
	}
	for i := range mg.Layers {
		_, err = c.Get(mg.Layers[i].Digest)
		if err != nil {
			t.Errorf("Get(%v): %v", mg.Layers[i].Digest, err)
		}
	}

	// missing chunks
	content = "llama"
	chunksums = fmt.Sprintf("%s 0-1\n", blob.DigestFromBytes("ll"))
	err = rc.Pull(ctx, "missingchunks")
	if err == nil {
		t.Error("expected error because of missing chunks")
	}
}
