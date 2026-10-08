# Current state — 2026-10-07

- `main` is the v0.6.3-based snapshot line (2025-04 import). It does not
  build with current toolchains and cannot load current model
  architectures (Qwen3, Gemma-4, Nemotron-3). Do not build on it.
- `sync/upstream-v0.40.1` is the maintained line: a fresh root import of
  upstream Ollama v0.40.1 plus the fork patch series (rebrand, ROSE_*
  env fallback to OLLAMA_*, stock-store preference, harbor default
  registry with a public-host union so stock stores resolve, paper).
  Validated on primo (Arch, Go 1.27.1): full cmake build OK; server,
  manifest, types, envconfig, api, create, transfer tests pass; live
  parity against stock Ollama 0.40.0 on a shared store PASSED
  (specialists serve, tool call completes). See docs/rose.md.
- Format rule learned the hard way: application/vnd.ollama.* media
  types are a storage format, never rebrand them. The rebrand script
  protects them.
- The hybrid-TLS security branch (rose-default-hybrid-security-20260908)
  is unported; it targets the old tree. Port deliberately, not by merge.
- Next: promote sync/upstream-v0.40.1 to main (owner decision); CUDA
  build for a GPU perf table; security-transport port.
