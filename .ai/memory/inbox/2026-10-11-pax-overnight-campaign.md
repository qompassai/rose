# Overnight campaign pass — 2026-10-11 (Pax)

Primo checkout: fresh clone by the campaign inventory wave; clean and
in sync at f5e58b10 == origin/main.

Gates run on primo (Go set per campaign brief):
- go build ./... -> rc 0
- go vet ./...   -> rc 0
- go test ./...  -> rc 0 (65 packages ok, 0 FAIL, 21 packages with no test files)
CI's CMake native superbuild (test.yaml) was not run in this pass.

SCIP: ~/.local/bin/scip-go generated an index (rc 0, 37,202,622 bytes).
Not committed: this repo has no index.scip pattern. Artifact: /tmp/rose-index.scip on primo (ephemeral).

Sandbox-clone inventory for Matt's landing decisions (read-only; nothing
pushed or rewritten):
- Sandbox ~/workspace/repos/rose is at d29acc7, BEHIND origin/main by 48,
  with zero unpushed commits, no extra branches, worktrees, or stash.
- The D2/D3 security-ruling commits (recorded sha 5aba1bc7) and the LoRA
  restore were NOT found in the sandbox clone or in primo clones searched
  on 2026-10-11. The rose token-middleware work product survives only in
  primo's cleanup archive: ~/archive/2026-10-11-home-cleanup/rose-build-preserve/rose-token/.
  Landing any of that remains Matt's call.

ROSE_* discipline untouched; no ollama aliases or ollama-first defaults added.
