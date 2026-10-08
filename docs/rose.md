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
2. **Environment identity** — Rose configuration uses `ROSE_*`
   variables only. `OLLAMA_*` variables are not aliases.
3. **Default store (deliberate deviation)** — `envconfig.Models` uses
   `$XDG_DATA_HOME/rose/models`, or `~/.local/share/rose/models` when
   `XDG_DATA_HOME` is unset. `ROSE_MODELS` always wins when set. Rose
   does not fall back to a stock `~/.ollama/models` store.
4. **Default registry** — unqualified model names resolve against
   `harbor.qompass.ai` (Matt's registry), not `registry.ollama.ai`.
5. **Host compat shim (pending deviation ruling)** — manifest handling
   currently recognizes the harbor, Ollama registry, and ollama.com
   public hosts as one identity so existing stock-written stores resolve.
   This behavior is retained pending a separate ruling; it is not
   settled Rose design.
6. **Paper** — `LICENSE-AGPL` and `LICENSE-QCDA` carried from the
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
