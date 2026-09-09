# Rose Backend Engineering Playbook

This file is a repository procedure, not an automatically installed Agent Skill.
Use tool paths available on the current machine.

## Integration or security change

1. Read `AGENTS.md`, the affected caller and tests. Record a baseline and the
   intended compatibility/security boundary.
2. Reproduce the failure or add a negative regression before changing behavior.
3. Implement the minimum fix with explicit bounds, errors and resource ownership.
4. Run the focused package tests and static checks below.
5. Review the final diff. Report passes separately from unavailable platforms,
   accelerators, external registries and real model inference.

From the repository root with the Go toolchain required by `go.mod`:

```sh
go version
go test -p 2 ./auth ./internal/transport ./api ./envconfig
go test -p 2 ./server/internal/internal/backoff ./server/internal/internal/syncs
go test -race -p 2 ./auth ./internal/transport ./api ./envconfig
go vet -p 2 ./auth ./internal/transport ./api ./envconfig
git diff --check
bash -n build.sh
```

Run `gofmt -l` on changed Go files and require empty output. Do not reformat the
whole repository. With the required native build dependencies installed, also run
`go build .` and `go test -p 2 ./...`; record native dependency failures rather than
calling targeted tests a whole-repository pass.

## Cryptographic and transport regressions

Require positive TLS negotiation and inspect the actual TLS version and curve.
Negative tests must reject classical-only key exchange, TLS 1.2, untrusted or
missing client certificates, bad server identity and remote plaintext. Validate
certificate/key pairing, malformed PEM, file bounds and resource cleanup.

For signatures, test modified messages, each tampered signature half, substituted
public keys, truncated/extra fields, unsupported versions/algorithms and input
bounds. Check trusted-key verification separately from self-contained validity.
Exercise private-key permissions and no-overwrite behavior.

Do not contact paid services, download models or send private editor contents as
part of routine tests. Optional live tests must be explicitly enabled and labeled.
