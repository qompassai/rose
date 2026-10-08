# Current Work

Rose (qompassai/rose) — Matt's fork of Ollama, the local model server
phlow talks to. Not rose.nvim (the plugin); this is the Go server.

## Status (2026-10-07, sync branch `sync/upstream-v0.40.1`)

- `main` is a v0.6.3-era (Mar 2025) snapshot series — the fork fell
  ~1,731 upstream commits behind. It cannot load the current specialist
  architectures (Qwen3, Gemma-4, Nemotron-3); it is the rollback point,
  not the future.
- This branch re-imports upstream **v0.40.1** and re-applies the fork
  delta as a patch series: mechanical rebrand
  (`scripts/rebrand-rose.pl`, rerunnable), `ROSE_*` → `OLLAMA_*` env
  alias fallback, existing-`~/.ollama/models` store preference, default
  registry host `harbor.qompass.ai`, fork licenses.
- The hybrid-TLS security layer still lives only on
  `rose-default-hybrid-security-20260908` (based on the old tree).
  Porting it onto this base is the next workstream; do not assume it is
  in this branch.
- Full recon + plan: `~/workspace/rose-fork/UPDATE-PLAN.md` (workspace,
  not committed); identity notes in `docs/rose.md`.

## Standing rules

- Local commits on branches; pushes only per Matt's explicit
  authorization, and only for builds that pass the primo parity check
  (specialists serve, phlow probe answers, structured tool call works).
- Never force-push. `main` is not promoted without Matt's call.
