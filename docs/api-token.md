# API token authentication (ROSE_API_TOKEN)

Rose's HTTP API is unauthenticated by default, exactly as upstream's
is. Setting `ROSE_API_TOKEN` opts the server into a shared bearer-token
check for operators who expose Rose beyond a fully trusted loopback —
for example to other machines on a LAN, or behind a reverse proxy.

```sh
ROSE_API_TOKEN='a-long-random-secret' rose serve
```

Clients then send the token on every API request:

```sh
curl -H "Authorization: Bearer a-long-random-secret" http://127.0.0.1:11434/api/tags
```

<details>
<summary>Behavior contract</summary>

- **Unset or empty (the default):** no behavior change at all. The
  middleware is a pass-through, no header is required, and a supplied
  `Authorization` header is processed exactly as upstream processes it.
- **Set:** every in-scope request must carry
  `Authorization: Bearer <token>` — including requests from loopback.
  There is no loopback exemption: a token that localhost can bypass
  protects nothing against anything else running on the same machine.
- **Failure:** missing or wrong tokens get `401` with the fixed body
  `{"error":"unauthorized"}` and a `WWW-Authenticate: Bearer` header.
  The body never echoes the presented token or the expected token.
- **Comparison:** the presented token is compared in constant time.
  Both sides are reduced to SHA-256 digests before
  `crypto/subtle.ConstantTimeCompare`, so the comparison leaks neither
  the expected token's length nor a matching prefix.
- **The token is a secret in the logs too:** it is never written to the
  server log. `envconfig` reports `ROSE_API_TOKEN` as set/unset only,
  because the environment summary is logged at startup.

</details>

<details>
<summary>Scope: what is protected, and what is not</summary>

Protected, when the token is set:

- **`/api/*`** — the native Rose API: model management (`/api/pull`,
  `/api/create`, `/api/delete`, ...), inference (`/api/generate`,
  `/api/chat`, `/api/embed`), and account routes. This also covers the
  Codex desktop proxy mounted under `/api/codex`.
- **`/v1/*`** — the OpenAI-compatible routes (`/v1/chat/completions`,
  `/v1/completions`, `/v1/embeddings`, `/v1/models`, `/v1/responses`,
  `/v1/audio/transcriptions`) and the Anthropic-compatible
  `/v1/messages`. These are served by the same handlers as `/api/*`;
  leaving them open would make the `/api/*` check decorative.

Not protected:

- **`/` (GET/HEAD)** — returns the fixed string `Rose is running`,
  exposes no data and runs no operation, and is the client's heartbeat
  probe. It stays open whether or not a token is set.
- **CORS preflight (`OPTIONS`)** — answered by the CORS layer ahead of
  the token middleware, as upstream. A preflight carries no credentials
  and runs no operation; the actual request that follows it is checked.

Anything not served by the Rose HTTP router at all (for example a
separately exposed debugging endpoint) is outside this middleware's
reach and outside this guarantee.

</details>

<details>
<summary>Clients</summary>

The `rose` CLI and any program using Rose's Go API client
(`api.Client`) pick the token up from the same environment variable:
with `ROSE_API_TOKEN` set in the client's environment, the client sends
`Authorization: Bearer <token>` on its requests automatically. No
second variable and no flag are involved.

This is separate from the pre-existing signed-token mechanism
(`ROSE_AUTH`, used for ollama.com account requests). When
`ROSE_API_TOKEN` is set it takes precedence as the client's
`Authorization` header, so do not combine the two: a server protected
by an API token accepts the bearer token, not a signed token.

Third-party OpenAI- or Anthropic-compatible clients work unchanged:
both ecosystems already send `Authorization: Bearer <key>` — put the
Rose token in the client's API-key field.

</details>

<details>
<summary>Choosing and handling a token</summary>

- Use a long random value (for example 32 bytes from a password
  manager or `openssl rand -hex 32`). The token is a single shared
  credential for the whole API: there are no per-user tokens, scopes,
  or rotation in this mechanism — restart the server with a new value
  to rotate it.
- Pass it through the environment, not the command line (command lines
  are visible in process listings) and not a file committed anywhere.
- The token check is not a substitute for transport security. On an
  untrusted network the bearer token travels in cleartext over plain
  HTTP, like upstream's API; put Rose behind TLS (or a tunnel) as well.

</details>
