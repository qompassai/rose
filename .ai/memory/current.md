# Current state — 2026-10-08

- `main` is the maintained line: upstream Ollama v0.40.1 selectively
  imported, with Rose's patch series re-applied as discrete commits
  (rebrand, `ROSE_*`-only environment configuration, XDG default
  store, harbor default registry, paper, and the promotion of the sync
  line).
- Matt's Rose identity ruling (2026-10-08): Rose is his own product,
  loosely following Ollama. The command is `rose` and the environment
  namespace is `ROSE_*` in lieu of Ollama; do not add an `ollama`
  command alias, an `OLLAMA_*` fallback, or Ollama-first defaults.
- Matt's store ruling (2026-10-08): Rose's default store is
  `$XDG_DATA_HOME/rose/models`, or `~/.local/share/rose/models` when
  `XDG_DATA_HOME` is unset. `ROSE_MODELS` remains an explicit override.
- Compatibility shim pending Matt's deviation ruling: the public-host
  union is retained for now, but it is not settled Rose design.
- Validated on primo (Arch, Go 1.27.1): full cmake build OK; server,
  manifest, types, envconfig, api, create, and transfer tests pass;
  live acceptance checks against the phlow consumption pattern PASSED
  (specialists serve, structured tool call completes). See docs/rose.md.
- Format rule learned the hard way: application/vnd.ollama.* media
  types are a storage format, never rebrand them. The rebrand script
  protects them.
- The hybrid-TLS security branch (rose-default-hybrid-security-20260908)
  is unported; it targets the old tree. Port deliberately, not by merge.
- Next: CUDA build for a GPU perf table; Jinja template support; API
  token middleware; security-transport port.
