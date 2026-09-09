# Rose Security Boundaries

This update hardens the Rose/editor integration, transport, authentication helpers
and their lifecycle boundaries. It is not a security audit of every inference
engine, native dependency, registry operation or third-party plugin.

## Hybrid transport, not a quantum-proof claim

Explicit Rose HTTPS uses TLS 1.3 with only `X25519MLKEM768`: classical X25519
combined with ML-KEM-768 key establishment. Both client and server authenticate
certificates, and the server requires a client certificate from its configured
client CA. Go implements the hybrid TLS group in its standard library
([crypto/tls](https://pkg.go.dev/crypto/tls)).

Certificate signatures in this deployment remain classical. Hybrid key
establishment is not post-quantum certificate authentication, and it does not
encrypt stored models, editor buffers or local HTTP traffic. The separate
ML-DSA application signing helpers do not change the TLS certificate chain.

There is no classical-only retry for Rose HTTPS. TLS 1.2, classical-only key
exchange, missing/untrusted client certificates and invalid server identity fail.
Clients without the required TLS capability must be upgraded, not configured to
skip verification.

## Local and remote trust

Plain HTTP is accepted only on a literal loopback endpoint and an actually
loopback-bound server listener. Configuration is validated before server storage
initialization; TLS credentials must be complete before listening. See
[Neovim setup](neovim.md) for server and client environment variables.

Loopback HTTP trusts the local machine. It does not protect against other local
processes, a compromised editor, malicious plugins, local administrators or
untrusted browser access allowed by the existing origin configuration.

Remote mTLS authenticates membership in the configured client CA's trust domain.
Any accepted client has access to the existing Rose API; this update does not
introduce per-client roles, endpoint authorization or tenant isolation. Keep that
CA narrow, restrict network reachability, protect client keys and rotate/revoke
access through your certificate provisioning process. Automatic CRL/OCSP
revocation checking is not implemented here; use short-lived certificates and
explicit trust-store/key rotation appropriate to your deployment.

Go environment clients and Rose's editor curl transport disable proxies and
redirects. The editor does not read curlrc or inherit TLS key logging and CA
override environment variables for HTTPS. Explicit custom Go HTTP clients and
the legacy Ollama provider remain their callers' responsibility.

## Resource and lifecycle limits

The backend transport applies these limits:

| Boundary | Limit |
| --- | --- |
| Each PEM file, including a CA bundle | 1 MiB |
| Inbound HTTP headers | 64 KiB, plus Go's parser allowance |
| Header read / TLS handshake | 10 seconds |
| Idle keep-alive connection | 60 seconds |
| Accepted server connections | 256 |
| Go client connections per host | 32 |
| Go client response headers | 1 MiB |
| Go client response-header wait | 10 minutes |
| Buffered Go API JSON response | 32 MiB |

Streams and model uploads retain request-context cancellation rather than a
blanket whole-model size or lifetime cap. These bounds are not comprehensive
per-user quotas or protection against every application-level denial of service.
The server uses its own handler instead of exposing the process-global HTTP mux.
Listeners, signal watchers and HTTP shutdown are tied to explicit ownership.

TLS private keys must be regular, non-symlink files. Unix group/other key
permissions are rejected; Windows file ACLs require separate administrative
validation. Public certificate symlinks may be followed only with opened-file
identity checks. No password or private key content belongs in logs or URLs.

## Application signatures and key migration

The old classical verifier accepted structurally plausible signatures without
actually verifying them. The new implementation verifies Ed25519 signatures and
adds trusted-key variants so an embedded attacker-controlled key cannot be
mistaken for a trusted identity.

`auth.Sign` deliberately uses the established classical registry/updater wire
format. It uses `~/.rose/id_ed25519`, the same filename created by the CLI.
This is an explicit compatibility protocol, not fallback after hybrid failure.
External registry interoperability still requires live testing with that registry.

`auth.SignHybrid` is separate and opt-in. It requires both Ed25519 and native
Go ML-DSA-87 signatures. The version, suite, both public keys and message are bound
into both signatures; ML-DSA also uses a domain-separating context. Go 1.27
provides ML-DSA in the standard library
([Go 1.27 release notes](https://go.dev/doc/go1.27),
[crypto/mldsa](https://pkg.go.dev/crypto/mldsa)).

The versioned envelope is:

```text
rose-hybrid-v1|ssh-ed25519+ML-DSA-87|<SSH-public>:<SSH-signature>|<ML-DSA-public>:<ML-DSA-signature>
```

All binary fields use canonical padded base64 with exact expected sizes. Extra
components, unsupported suites, malformed fields or either invalid signature are
rejected. This application framing is not a standardized external registry
protocol and requires explicit peer support.

Use `VerifySignatureWithKey` or `VerifyHybridSignatureWithKeys` at authentication
boundaries with keys provisioned through an independent trusted channel.
`VerifySignature` and `VerifyHybridSignature` check mathematical validity only.
The application still owns freshness, replay protection and authorization.

Migration rules:

- **Classical key:** Keep the CLI's valid `id_ed25519`. If your only key is the old
  `ed25519_dilithium5` file, inspect and deliberately migrate its classical
  Ed25519 identity before use; it is not automatically copied or overwritten.
- **ML-DSA key:** `GenerateKeys` validates the classical key and provisions a
  versioned ML-DSA-87 seed at `~/.rose/quantum_keys/ML-DSA-87.seed`.
  `GenerateQuantumKeys` provisions only the post-quantum half in an existing
  safe `.rose` directory. Existing valid seeds are retained.
- **Legacy OQS keys:** Old `DILITHIUM_3`, `DILITHIUM_5`, `FALCON_1024` keys and
  unversioned hybrid envelopes are unsupported, not aliases for ML-DSA.
  Provision and distribute new trusted public keys; keep backups rather than
  relabeling old binary key material.
- **Atomic publication:** Private seed files are fully written and synced before
  no-replacement publication. Unsafe permissions, symlinks and unsupported
  filesystem operations fail rather than overwrite a key. An I/O failure after
  publication can leave a valid key present; inspect the error and validate that
  key before retrying.

Authentication work is bounded to 1 MiB messages, 16 KiB encoded signatures and
16–1024 bytes per nonce. Callers must use a cryptographic nonce reader.

Private application signing-key storage requires POSIX permissions and rooted
filesystem operations. Native Windows, Plan 9, JavaScript/Wasm and WASI storage
are explicitly unsupported; use a supported POSIX environment such as WSL.
This also limits registry operations that need `auth.Sign` on those platforms.
Pure signature verification does not access the private-key filesystem.
The separate TLS loader's Windows ACL caveat above is not a claim that the
application signing-key store supports native Windows.

## Why no QoreChain dependency

The suggested [QoreChain Go package](https://pkg.go.dev/github.com/qorechain/qorechain-pqc/go)
provides cryptographic primitives and hybrid-signing helpers, not a drop-in TLS
transport. Rose uses Go's native TLS/ML-KEM and ML-DSA implementations instead of
adding another cryptographic wrapper and native liboqs installation requirement.
This reduces integration dependencies; it is not a claim of certification or a
substitute for independent cryptographic and deployment review.
