# Upstream re-import cadence

Rose is Qompass AI's own product. It loosely follows upstream Ollama, but upstream is an input to Rose's design—not a specification Rose must match. Intake is therefore **selective**: adopt upstream changes that fit Rose's identity, hardware, security posture, and phlow use cases, and deliberately deviate where an Ollama-specific choice does not.

The monthly cadence repeats the method that produced the v0.40.1 line: evaluate a selected upstream tag, import what Rose accepts as a fresh line, re-apply Rose's decisions as a reviewable patch series, build it, and pass the phlow acceptance gate before promotion. A security release, a model-architecture requirement from phlow, or a material upstream defect can justify an out-of-cycle review.

The parity script is a standing acceptance gate for the behavior phlow consumes, not a requirement of lockstep parity with Ollama. A candidate that does not build and pass that gate remains a local or branch candidate; it does not become Rose's maintained line.

<details>
<summary>Monthly selection and preparation</summary>

1. Evaluate the newest suitable upstream release tag and record its tag, commit, release date, and any security or compatibility reason for accepting it. Record material upstream changes Rose deliberately rejects or defers, with the reason.
2. Preserve the current maintained line and the prior import line as named references before starting.
3. Create a new sync branch for the selected tag, using the established naming pattern `sync/upstream-vX.Y.Z`.
4. Import the upstream tree verbatim as the first commit. Do not mix local fixes into the import commit; keeping it pristine makes every later difference attributable to Rose.

</details>

<details>
<summary>Re-applying the Rose patch series</summary>

After the pristine import, apply the fork delta in small, ordered commits:

1. Run `scripts/rebrand-rose.pl` to apply the mechanical Rose identity. The script is the reusable source of this layer; improve the script when a new mechanical case is found rather than hand-editing hundreds of files.
2. Verify Rose's identity rule: the command is `rose` and configuration uses `ROSE_*` variables only. Do not introduce an `ollama` command alias, `OLLAMA_*` environment alias, or an Ollama-first default.
3. Preserve Rose's deliberate XDG default store: `$XDG_DATA_HOME/rose/models`, or `~/.local/share/rose/models` when `XDG_DATA_HOME` is unset. `ROSE_MODELS` remains an explicit override. Do not restore a stock-store preference or another Ollama-derived default during intake.
4. Re-apply Rose's default registry behavior. Preserve the existing public-host union during intake only as a **compat shim pending Matt's deviation ruling**, not settled design; any change belongs in a separate compatibility and migration decision.
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
<summary>Phlow acceptance gate</summary>

Run `parity_check.py` against the candidate Rose server on port `11435`, with the stock Ollama service on port `11434` left untouched. Configure Rose explicitly with `ROSE_*` variables. The candidate must use the designated model store and pass the script's gates:

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
- fork patches and upstream changes accepted, rejected, dropped, or deferred, with reasons;
- build and test evidence;
- parity output and any performance table, with build configuration labeled; and
- remaining conflicts or follow-up work, assigned to a specific future branch or cycle.

If a cycle is skipped, record the reason. The purpose of the cadence is not motion for its own sake; it is to keep each upstream jump small enough that the fork's differences remain explainable, testable, and safe to promote.

</details>
