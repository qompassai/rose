# API-token middleware landing — 2026-10-13 (Pax)

Matt ruled "go with your recs" (2026-10-13); the api-token branch was
the push/merge item.

- Branch pax/api-token-20261012 @ 58d6ed02 (single commit on top of
  8611b396): opt-in ROSE_API_TOKEN Bearer auth for the HTTP API —
  middleware/apitoken.go (+tests), envconfig APIToken(), api client
  support, server/routes.go wiring, docs/api-token.md.
  8 files, +463/-6.
- ROSE_* discipline verified in the diff: the only OLLAMA_* token in
  the change is an adversarial test asserting OLLAMA_API_TOKEN is NOT
  honored. No ollama alias or fallback added.
- Gates on primo (go1.27.2), each tree: go build ./... rc 0;
  go test ./... rc 0, 65 packages ok, 0 FAIL — baseline main
  (8611b396), branch tree (58d6ed02), and merged tree all green.
  Main has no pre-existing drift failures today (the four drift
  tests were fixed and landed in c1856505).
- Landed as merge commit 388f9b32 "merge: land API-token middleware
  (pax/api-token-20261012)", repo merge style (--no-ff). Pushed
  plain (8611b396..388f9b32); ls-remote verified origin/main ==
  388f9b32.
- Branch ref left local (repo habit: landed feature branches are
  not pushed as refs); worktree ~/workspace/rose-apitoken-wt kept.
