# Rose Backend Engineering Rules

Read this file before editing and [SKILLS.md](SKILLS.md) before verification.
These rules adapt Tiger Style to Go; they do not claim a repository-wide audit.

## Scope and simplicity

- Inspect the relevant caller, implementation, tests, branch and dirty files first.
- State assumptions and observable acceptance criteria before multi-step changes.
  Ask when ambiguity would change behavior, security boundaries or scope.
- Make the smallest coherent change. Preserve unrelated code, wire formats and
  user work. Remove only dead code introduced by your change.
- Prefer the Go standard library. Do not add cryptographic primitives or custom
  protocols where an authenticated standard protocol already meets the need.

## Safety before performance and convenience

- Validate external input before side effects. Return contextual errors for
  expected operating failures; never treat invalid input as successful work.
- Name limits with units. Bound buffered input, output, retries, concurrency and
  waiting. Streaming model data needs cancellation and backpressure, not an
  arbitrary whole-model memory buffer.
- Give each listener, connection, file, goroutine and cancellation function a clear
  owner. Release resources on error, cancellation and normal exit.
- Keep functions locally understandable, usually within 70 lines. Report cohesive
  exceptions rather than splitting code solely to meet a count.
- Do not fabricate values, swallow errors, weaken verification or suppress tests
  to make a check pass. Measure before claiming a performance improvement.

## Security invariants

- Plain HTTP is loopback-only. Remote serving requires authenticated TLS, not
  merely an HTTPS-looking URL or an unverified certificate.
- Preserve TLS 1.3 and explicit hybrid key-exchange policy. Never add silent
  classical fallback, `InsecureSkipVerify`, curl `--insecure`, proxy inheritance
  or redirects to the Rose editor transport.
- A signature carrying its own public key does not establish identity. Verify
  against trusted keys at authorization boundaries and require both hybrid halves.
- ML-KEM key exchange, ML-DSA signatures and certificate authentication are
  separate mechanisms. Do not describe classical certificates as post-quantum.
- Read secrets only when needed, do not log key contents, and do not overwrite
  existing private keys as a migration strategy. Legacy key formats must fail
  explicitly rather than be reinterpreted.

## Completion evidence

Run focused regression and negative tests, then affected formatting, static and
integration gates on the final diff. Report exact commands, tool versions, results
and omissions. A transport fixture is not an inference test; a Linux build is not
a verified GPU, Windows, macOS or container release.
