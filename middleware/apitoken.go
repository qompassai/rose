package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// APITokenMiddleware returns a handler that enforces Rose's opt-in
// bearer-token check for the HTTP API (ROSE_API_TOKEN, see envconfig).
//
// Scope decision (deliberate, keep in sync with docs/api-token.md):
// the check covers every route under /api/ and /v1/. The /v1/ tree is
// not a separate, lesser API: the OpenAI-compatible (/v1/chat/completions,
// /v1/models, ...) and Anthropic-compatible (/v1/messages) routes are
// served by the same handlers as /api/, so protecting only /api/ would
// leave the same operations reachable without the token. The /api/
// prefix also covers the Codex desktop proxy mounted at /api/codex.
// The only route left open is "/" (GET/HEAD), which returns the fixed
// string "Rose is running" and is the client's heartbeat probe; it
// exposes no data and runs no operation.
//
// Behavior contract:
//   - token == "" (ROSE_API_TOKEN unset or empty): the handler is a
//     pass-through. Requests are processed exactly as upstream — no
//     header is required and a supplied Authorization header is ignored.
//   - token != "": every in-scope request, loopback included, must
//     carry "Authorization: Bearer <token>". Missing or wrong tokens
//     get 401 with a fixed body that never echoes the presented or
//     expected token.
//
// The expected token is hashed once, here at construction, and each
// presented token is hashed before comparison. Comparing fixed-size
// digests with subtle.ConstantTimeCompare means the comparison itself
// reveals neither the expected token's length nor a matching prefix:
// there is no early return that depends on the secret.
func APITokenMiddleware(token string) gin.HandlerFunc {
	if token == "" {
		return func(c *gin.Context) { c.Next() }
	}

	expected := sha256.Sum256([]byte(token))

	return func(c *gin.Context) {
		if !apiTokenProtectedPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		presented, ok := bearerToken(c.GetHeader("Authorization"))
		presentedDigest := sha256.Sum256([]byte(presented))
		if !ok || subtle.ConstantTimeCompare(presentedDigest[:], expected[:]) != 1 {
			c.Header("WWW-Authenticate", "Bearer")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		c.Next()
	}
}

// apiTokenProtectedPath reports whether path is in the token's scope:
// the /api/ and /v1/ trees (see APITokenMiddleware for the rationale).
func apiTokenProtectedPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") ||
		path == "/v1" || strings.HasPrefix(path, "/v1/")
}

// bearerToken extracts the credential from an "Authorization: Bearer
// <token>" header value. The scheme is matched case-insensitively, as
// HTTP authentication schemes are case-insensitive. ok is false for a
// missing header, a different scheme, or an empty credential; the
// returned string is empty in those cases and is still hashed and
// compared by the caller so failure paths do the same work.
func bearerToken(header string) (token string, ok bool) {
	scheme, credential, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}

	credential = strings.TrimSpace(credential)
	if credential == "" {
		return "", false
	}

	return credential, true
}
