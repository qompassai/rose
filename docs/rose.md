# Rose — what this fork is

Rose is Qompass AI's fork of [Ollama](https://github.com/ollama/ollama),
the local model server. It exists to serve phlow's local specialists
(planner / coder / reviewer) on Matt's hardware with an identity, a
registry, and a security posture of its own. It is **not** rose.nvim
(the Neovim plugin) — the plugin talks to this server as a backend.

## How this branch was made

Upstream Ollama at tag **v0.40.1**, imported verbatim, with the fork
delta re-applied as discrete commits:

1. **Mechanical rebrand** — `scripts/rebrand-rose.pl` (rerunnable):
   module path `github.com/qompassai/rose`, `ROSE_*` environment
   variables, Rose product strings. Real service domains
   (`registry.ollama.ai`, `cdn.ollama.com`, `ollama.com`) are kept
   verbatim — they are live services, not brand text.
2. **Env compat** — `envconfig.Var`: a `ROSE_*` variable that is unset
   falls back to its legacy `OLLAMA_*` twin, so existing scripts and
   service units keep working. `ROSE_*` wins when both are set.
3. **Store compat** — `envconfig.Models`: an existing stock
   `~/.ollama/models` store is served in place; fresh installs use
   `~/.rose/models`. `ROSE_MODELS` always wins when set.
4. **Default registry** — unqualified model names resolve against
   `harbor.qompass.ai` (Matt's registry), not `registry.ollama.ai`.
5. **Paper** — `LICENSE-AGPL` and `LICENSE-QCDA` carried from the
   fork's main branch (upstream's MIT `LICENSE` is retained for the
   upstream code); `.cache/` and `.zig-cache/` are gitignored.

## Not in this branch (yet)

- The **hybrid-TLS transport layer** (`internal/transport/`,
  `auth/hybrid.go`, `auth/keys.go`) lives on
  `rose-default-hybrid-security-20260908`, based on the old v0.6.3-era
  tree. Porting it forward is planned, not done.
- The HTTP API remains unauthenticated, as upstream's is; the default
  bind is loopback (`127.0.0.1:11434`). The transport layer above is
  the fork's intended answer — see the update plan before exposing
  Rose beyond loopback.

## Building

See `AGENTS.md` / `docs/development.md` (upstream text): `cmake` build
for the native payload, then `./rose serve`.
