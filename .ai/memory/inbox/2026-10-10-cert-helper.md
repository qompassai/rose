# 2026-10-10 — `rose cert` certificate helper (security-port decision D5, option b)

Matt ruled D5 = build the helper (Oct 10), overriding the docs-only
recommendation. Landed locally on branch `feat/cert-helper` off main
`5438273a`; **not pushed** — landing is Matt's separate call.

## What was built

- `internal/certhelper`: a store under `~/.rose/tls` (dir 0700, keys
  0600 single unencrypted PKCS#8 PEM, certs 0644). Ed25519 CA
  (default 365 d) + server/client leaves (default 90 d, leaf+CA chain
  files, server SANs required). Operations: InitCA, IssueServer,
  IssueClient, List, RotateCA. Nothing ever overwrites: re-issue is
  refused, rotation archives the old CA and writes `trust-bundle.crt`
  (new+old) — trust-store rotation is the revocation mechanism, per
  the D5 design note.
- `cmd/cert.go`: `rose cert {init-ca, issue-server, issue-client,
  list, rotate-ca, env}` wired into NewCLI. `env` prints the
  ROSE_TLS_* path assignments (bundle preferred during rotation).
- Docs: `docs/certificates.mdx`, registered in `docs/docs.json`.

## Premise conflict found (recorded, worked around)

`internal/transport` and the `ROSE_TLS_*` envconfig surface do NOT
exist on main — they live only on `sync/security-transport`
(`13f8a9ca`). So on this branch there is no in-tree consumer to wire
into; the helper conforms to the transport policy as written on that
branch (cert-only PEM, single PKCS#8 key, CA roots must be able to
sign, leaf-first chain, TLS 1.3 + X25519MLKEM768, RequireAndVerify
ClientCert). Proof: on primo, the branch's unmodified transport
package was copied into the gate scratch tree and
`ServerTLSConfig`/`ClientTLSConfig`/`WrapListener` accepted the
helper's output and completed a real handshake (throwaway test, not
committed). When stage wiring lands the transport on main, no helper
changes should be needed.

## Gates (primo, Go 1.27.1)

- `go build ./...` green; gofmt clean; `go vet` clean on touched pkgs.
- `go test -race ./internal/certhelper/`: all pass (provision/chain
  verification, transport-loader shape, list+rotate semantics,
  permissions, no-overwrite, invalid inputs, tampered store,
  wide-permission dir refusal, mTLS handshake accept + wrong-CA /
  no-cert / expired rejects).
- Full suite: failures are exactly the 4 known pre-existing
  fork-vs-test drift tests (TestPushHandler, TestShowInfo/license,
  2× DeepSeek harness) — see run record in the handoff.
- CLI smoke under a scratch HOME on primo: init-ca → issue-server →
  issue-client → list → env all behave; on-disk modes verified.
- Primo's real `~/.rose` was never touched; `go test -race ./auth/`
  passes (hardened key loader unaffected).

## Design forks resolved

- Keys are Ed25519 throughout (matches rose's identity keys; TLS 1.3
  accepts it; smallest/fastest stdlib option).
- One server identity per store; named clients under `clients/`.
  Multiple deployments = multiple `--dir` stores.
- Leaf validity is not clamped to CA expiry in code; operators see
  both expiries in `list`. (x509 verification still fails closed at
  runtime once the CA expires.)
