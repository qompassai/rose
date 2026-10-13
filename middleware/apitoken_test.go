package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const testAPIToken = "test-api-token-0123456789"

// newAPITokenRouter builds a minimal router shaped like the real one:
// a protected /api/ route, a protected /v1/ route, and the open root
// route, all behind APITokenMiddleware with the given token.
func newAPITokenRouter(token string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(APITokenMiddleware(token))
	ok := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	r.GET("/api/tags", ok)
	r.GET("/v1/models", ok)
	r.GET("/", ok)
	return r
}

func serveAPITokenRequest(r *gin.Engine, path, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAPITokenMiddlewareTokenSet(t *testing.T) {
	r := newAPITokenRouter(testAPIToken)

	cases := []struct {
		name          string
		path          string
		authorization string
		wantStatus    int
	}{
		{"correct token on /api", "/api/tags", "Bearer " + testAPIToken, http.StatusOK},
		{"correct token on /v1", "/v1/models", "Bearer " + testAPIToken, http.StatusOK},
		{"scheme is case-insensitive", "/api/tags", "bearer " + testAPIToken, http.StatusOK},
		{"missing header on /api", "/api/tags", "", http.StatusUnauthorized},
		{"missing header on /v1", "/v1/models", "", http.StatusUnauthorized},
		{"wrong token", "/api/tags", "Bearer wrong-token", http.StatusUnauthorized},
		{"token with suffix appended", "/api/tags", "Bearer " + testAPIToken + "x", http.StatusUnauthorized},
		{"token is a prefix of the real one", "/api/tags", "Bearer test-api-token", http.StatusUnauthorized},
		{"signed-token style header is not a bearer token", "/api/tags", testAPIToken, http.StatusUnauthorized},
		{"wrong scheme", "/api/tags", "Basic " + testAPIToken, http.StatusUnauthorized},
		{"bearer with no credential", "/api/tags", "Bearer ", http.StatusUnauthorized},
		{"bearer with empty credential", "/api/tags", "Bearer", http.StatusUnauthorized},
		{"root route stays open", "/", "", http.StatusOK},
		{"root route ignores a wrong token", "/", "Bearer wrong-token", http.StatusOK},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveAPITokenRequest(r, tt.path, tt.authorization)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body %q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus == http.StatusOK && rec.Body.String() != "ok" {
				t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
			}
		})
	}
}

func TestAPITokenMiddlewareTokenUnset(t *testing.T) {
	r := newAPITokenRouter("")

	cases := []struct {
		name          string
		path          string
		authorization string
	}{
		{"no header on /api", "/api/tags", ""},
		{"no header on /v1", "/v1/models", ""},
		{"arbitrary header on /api", "/api/tags", "Bearer anything-at-all"},
		{"arbitrary header on /v1", "/v1/models", "Basic dXNlcjpwYXNz"},
		{"no header on root", "/", ""},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveAPITokenRequest(r, tt.path, tt.authorization)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d: with no token configured the middleware must not change behavior", rec.Code, http.StatusOK)
			}
			if rec.Body.String() != "ok" {
				t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
			}
		})
	}
}

// TestAPITokenMiddlewareUnauthorizedBodyLeaksNothing pins the failure
// response: a fixed plain error that contains neither the expected
// token, nor the presented token, nor any fragment of either.
func TestAPITokenMiddlewareUnauthorizedBodyLeaksNothing(t *testing.T) {
	r := newAPITokenRouter(testAPIToken)

	for _, authorization := range []string{"", "Bearer wrong-token", "Bearer " + testAPIToken[:8]} {
		rec := serveAPITokenRequest(r, "/api/tags", authorization)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q: status = %d, want %d", authorization, rec.Code, http.StatusUnauthorized)
		}

		body := rec.Body.String()
		if body != "{\"error\":\"unauthorized\"}" {
			t.Errorf("authorization %q: body = %q, want the fixed unauthorized body", authorization, body)
		}
		for _, secret := range []string{testAPIToken, "wrong-token", testAPIToken[:8]} {
			if strings.Contains(body, secret) {
				t.Errorf("401 body %q leaks %q", body, secret)
			}
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
			t.Errorf("WWW-Authenticate = %q, want %q", got, "Bearer")
		}
	}
}
