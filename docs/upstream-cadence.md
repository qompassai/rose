# Upstream re-import cadence

Rose stays current by repeating the method that produced the v0.40.1 line: import a selected upstream Ollama tag as a fresh line, re-apply the fork as a reviewable patch series, build it, and pass the phlow parity gate before promotion. The standing cadence is **monthly**. A security release, a model-architecture requirement from phlow, or a material upstream defect can justify an out-of-cycle import.

The parity script is a standing gate, not a one-time migration check. A candidate that does not build and pass parity remains a local or branch candidate; it does not become Rose's maintained line.

<details>
<summary>Monthly selection and preparation</summary>

1. Select the newest suitable upstream release tag and record its tag, commit, release date, and any security or compatibility reason for choosing it.
2. Preserve the current maintained line and the prior import line as named references before starting.
3. Create a new sync branch for the selected tag, using the established naming pattern `sync/upstream-vX.Y.Z`.
4. Import the upstream tree verbatim as the first commit. Do not mix local fixes into the import commit; keeping it pristine makes every later difference attributable to Rose.

</details>

<details>
<summary>Re-applying the Rose patch series</summary>

After the pristine import, apply the fork delta in small, ordered commits:

1. Run `scripts/rebrand-rose.pl` to apply the mechanical Rose identity. The script is the reusable source of this layer; improve the script when a new mechanical case is found rather than hand-editing hundreds of files.
2. Re-apply or verify the environment compatibility behavior: `ROSE_*` variables take precedence, with their `OLLAMA_*` counterparts accepted as fallbacks.
3. Re-apply or verify the model-store preference: an existing `~/.ollama/models` store is used when present, `~/.rose/models` is the fresh-install default, and an explicit `ROSE_MODELS` setting wins.
4. Re-apply or verify the default registry behavior and the public-host compatibility that lets stock-written model stores resolve under Rose.
5. Carry forward Rose's paper and repository documentation, including the AGPL and Q-CDA licenses and the protected storage-format namespaces. Manifest media types are a storage and wire format, not branding, and must remain compatible.
6. Port functional fork features only as separate commits, each with its own tests and rationale. Do not fold security, template, or integration changes into the rebrand commit.

Every commit should be independently understandable from `git log` and `git show`. If an upstream change makes a fork patch unnecessary or incompatible, record that decision in the sync report instead of silently dropping or forcing the patch.

</details>

<details>
<summary>Build gate</summary>

Build the candidate on primo using the documented upstream build flow and the toolchain intended for regular Rose work. The build gate requires:

- the pristine upstream tag to build before fork patches are evaluated, when an unexpected failure needs fault isolation;
- the fully patched Rose tree to build cleanly;
- relevant Go package tests to pass, including tests changed by re-applied patches; and
- formatting and other repository checks required by the tree's contributor documentation to pass for changed files.

A build produced through a different backend or runtime configuration must be labeled as such. In particular, a CPU-only build must not be represented as evidence of GPU performance.

</details>

<details>
<summary>Parity gate</summary>

Run `parity_check.py` against the candidate Rose server on port `11435`, with the stock Ollama service on port `11434` left untouched. The candidate must use the same model store and pass the script's gates:

- `/api/version` answers;
- `/api/tags` includes the complete model set phlow relies on;
- chat succeeds for the planner, coder, and reviewer specialists;
- a structured tool call completes with `hf-qwen3-coder-30b`; and
- the script exits with `PARITY PASS`.

Record the server version, model list, tool-call result, test output, and performance diagnostics with the sync report. Diagnostics for models that stock Ollama refuses for tool calls should remain visible, but they do not replace the parity gate unless Rose intentionally changes that behavior and has separate acceptance tests for it.

</details>

<details>
<summary>Promotion and records</summary>

Promote a candidate only after the build and parity gates pass. Keep the previous maintained line reachable; do not rewrite published history to perform a routine sync. After promotion, verify the remote branch points at the exact local commit that passed the gates.

For each monthly cycle, record:

- upstream tag and commit selected;
- sync branch and final commit;
- fork patches re-applied, dropped, or deferred, with reasons;
- build and test evidence;
- parity output and any performance table, with build configuration labeled; and
- remaining conflicts or follow-up work, assigned to a specific future branch or cycle.

If a cycle is skipped, record the reason. The purpose of the cadence is not motion for its own sake; it is to keep each upstream jump small enough that the fork's differences remain explainable, testable, and safe to promote.

</details>
