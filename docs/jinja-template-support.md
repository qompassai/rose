# Jinja chat template support

Rose models get their chat formatting from a template. Two template
languages exist in the wild: Go `text/template` (Rose's historical
Modelfile `TEMPLATE`) and jinja2 (the HuggingFace `chat_template.jinja`
format). Before this change, a jinja template could only reach a model
by being embedded in GGUF metadata (`tokenizer.chat_template`) — either
by the GGUF author or by rewriting the GGUF file out-of-band. Supplying
one through `TEMPLATE` at create time was rejected, which left whole
model families (granite, devstral, gpt-oss, phi-4) unable to serve tool
calls without a GGUF splice. This document describes how templates flow
through the tree today, and the surgical change that lets a jinja
template be supplied at create time and rendered natively at serve
time, with tools.

The short version: classification and validation happen in the
`template` package, storage is one new manifest layer
(`application/vnd.ollama.image.chat_template`), and rendering reuses
the existing native llama-server path — llama.cpp already ships a
jinja engine (minijinja) and a template-derived tool-call parser. No
Go jinja interpreter is added, no GGUF is rewritten, and Go templates
keep working byte-for-byte.

<details>
<summary>Where templates are parsed and rendered today</summary>

- **Create path.** `create.ApplyModelfileLayers`
  (`create/manifest.go`) is the single choke point for Modelfile
  overlays: both the GGUF create (`server/create.go`) and the
  safetensors manifest writer call it. It validated `opts.Template`
  with `template.Parse` (Go `text/template` only) and stored it as an
  `application/vnd.ollama.image.template` layer. A jinja `TEMPLATE`
  died here (`ErrBadTemplate`: `malformed character constant`,
  `function "message" not defined` — measured in the 2026-10-07
  tool-call cure record, `docs/specialists.md` lineage).
- **Serve/chat path.** `server.GetModel` (`server/images.go`) reads
  manifest layers back: a template layer becomes `Model.Template`
  (Go), and the GGUF's `tokenizer.chat_template` metadata becomes
  `Model.HasChatTemplate`. `chatModeForModel` (`server/routes.go`)
  then picks one of two execution modes:
  - *Rendered*: Rose renders the prompt itself with the Go template
    (`chatPrompt`), optionally parsing tool calls back with
    `tools.Parser` or a built-in renderer/parser pair
    (`model/renderers`, `model/parsers`). Chosen when a renderer,
    parser, harmony, or Go template is selected
    (`usesOllamaRenderedChat`).
  - *Native*: the request goes to llama-server's
    `/v1/chat/completions`; llama.cpp renders the GGUF-embedded jinja
    chat template and parses tool calls with the parser it derives
    from that template. `llm.ApplyChatTemplate` exposes the same
    rendering through llama-server's `/apply-template` for
    debug/truncation paths.
- **Template package.** `template/template.go` wraps Go
  `text/template` (`Parse`, `Execute`, `Vars`, plus the built-in
  `.gotmpl` library matched by `template.Named` in
  `server.detectChatTemplate`). The `harmony/` package is a response
  parser for gpt-oss harmony output, not a template engine; it engages
  via `shouldUseHarmony` when a Go template contains harmony tags.
- **Existing jinja in the tree.** No Go jinja library is vendored
  (`go.mod` has no gonja/pongo/minijinja binding). Jinja rendering
  exists only inside llama.cpp (b11351), reached through the native
  path. HF directory imports already *read* `chat_template.jinja`
  (`create/metadata.go: readChatTemplateStrict`) for safetensors
  models, but GGUF creates had no equivalent intake.

</details>

<details>
<summary>How capability detection decides "supports tools"</summary>

`Model.Capabilities()` (`server/images.go`) assembles capabilities
from config, GGUF metadata, projector, template, and parser sources.
The template-relevant rules:

- GGUF chat template: `chatTemplateCapabilities` grants
  `CapabilityTools` when the jinja source contains `tools` or
  `tool_call` (`chatTemplateHasToolSupport`), and
  `CapabilityThinking` via `thinking.TemplateSupportsThinking`.
- Go template: `goTemplateCapabilities` grants tools when the parsed
  template's variable set contains `tools`.
- When both a Go template and a GGUF chat template exist,
  `shouldPreferChatTemplate` decides whether the GGUF template wins
  (`Model.PreferChatTemplate`); `shouldUseGoTemplate` also honours the
  `ROSE_GO_TEMPLATE` operator override. The chat handler requires
  `CapabilityTools` in the schedule request when the caller sends tool
  schemas, and refuses with `"<model>" does not support tools`
  otherwise — the exact refusal the four spliced models hit in 2026-10.

Consequence for this design: a supplied jinja template must feed the
*same* capability derivation as a GGUF-embedded one, or the model will
render correctly yet still be refused for tool calls.

</details>

<details>
<summary>Design: supplied jinja templates, end to end</summary>

1. **Classify and validate** (`template/jinja.go`, new).
   `ClassifyTemplate(s)` returns `KindGo` or `KindJinja`:
   - A template containing a jinja statement tag (`{%`, including the
     `{%-` whitespace-control form) or comment tag (`{#`), or one of
     a small explicit set of expression-only jinja markers
     (`raise_exception(`, `namespace(`, ` is defined`,
     ` is not defined`), is jinja. All
     four blocked production templates (upstream granite, devstral,
     gpt-oss, and the authored phi-4 template) contain `{%`.
   - Anything else is parsed as a Go template, exactly as before; a
     Go parse failure is returned unchanged, so existing error text
     and behaviour for broken Go templates do not move.
   - Jinja candidates are checked by `ValidateJinja`, a bounded
     structural validator (no interpreter): delimiter balance for
     `{{ }}`, `{% %}`, `{# #}` with string-literal awareness,
     block-stack matching for `for`/`if`/`block`/`macro`/`filter`/
     `raw` against their `end*` tags, `elif`/`else` placement, and
     named limits (`MaxJinjaTemplateBytes` = 4 MiB,
     `MaxJinjaStatements` = 10 000). Malformed jinja fails create
     with a clean, offset-carrying error — never a panic, never a
     silently accepted template that llama-server rejects at load.
     Full jinja *semantic* validation (unknown filters, bad
     expressions) remains llama.cpp's job at runner start, the same
     trust boundary as GGUF-embedded templates today.
2. **Store** (`manifest.MediaTypeImageChatTemplate` =
   `application/vnd.ollama.image.chat_template`, new constant).
   `ApplyModelfileLayers` classifies `opts.Template`: Go templates go
   to the existing template layer unchanged; jinja templates go to
   the new layer, and any inherited template/prompt/chat-template
   layers are replaced. The GGUF blob is never modified — this is the
   property the GGUF-splice cure lacked. Manifests carrying the new
   layer degrade gracefully under older readers: the layer is
   ignored and the model falls back to its GGUF template, exactly
   like a model with no `TEMPLATE` today.
3. **Load** (`server/images.go: GetModel`). A chat-template layer sets
   `Model.ChatTemplate` (source text) and `Model.ChatTemplatePath`
   (its blob path), and marks `HasChatTemplate`. A supplied template
   takes precedence over both the GGUF-embedded template and any Go
   template: it is the most recent explicit operator instruction.
   Capability derivation reads the *effective* chat template
   (supplied first, else GGUF metadata) through the existing
   `chatTemplateCapabilities`, so tools/thinking detection is
   identical for supplied and embedded templates.
   `selectedModelTemplate` (`/api/show`) likewise reports the
   supplied template on the native path.
4. **Render** (`llm.LlamaServerConfig.ChatTemplateFile`, new field).
   `llamaServerConfigForModel` passes the supplied template's blob
   path; `appendJinjaArgs` emits `--chat-template-file <path>` when
   jinja is not disabled. llama-server then uses that template for
   `/v1/chat/completions` and `/apply-template`, deriving its
   tool-call parser from it — the same mechanism that made the
   GGUF-spliced variants parse `<tool_call>` XML (granite),
   `[TOOL_CALLS]` (devstral), harmony `to=functions.*` (gpt-oss), and
   inline `functools[...]` (phi-4) emissions. No per-request template
   field is used, so there is exactly one template source per runner.
   Renderer/parser-configured and harmony models keep their existing
   rendered path (`DisableJinja` wins; a supplied template is stored
   and reported but not rendered) — changing that precedence is out
   of scope.

Identity rule: nothing here introduces an `OLLAMA_*` alias, an
`ollama` command alias, or an Ollama-first default. The one new name,
the layer media type, follows the tree's existing
`application/vnd.ollama.image.*` manifest vocabulary, which is a wire
format shared with the registry protocol, not a user-facing alias.

</details>

<details>
<summary>Scope limits</summary>

- GGUF models on the llama-server runner only. Safetensors/MLX models
  accept and store a jinja `TEMPLATE` at create, but the MLX runner's
  `ApplyChatTemplate` does not consume it yet; their behaviour is
  otherwise unchanged.
- Per-request template overrides (`/api/generate`'s `template`
  field) remain Go-only.
- The Go-rendered path gains no jinja support: if an operator forces
  rendered mode (`ROSE_GO_TEMPLATE=1` with a Go template, or a
  renderer/parser config), a supplied jinja template does not render
  there. Native mode is the jinja path, mirroring how GGUF-embedded
  templates already behave.
- `{%` or `{#` in a Go template's literal text now classifies the
  template as jinja and will fail jinja validation. No built-in
  `.gotmpl` uses either, and chat Go templates do not emit them; the
  marker had to be unambiguous, and jinja owns both by definition.
- gpt-oss: a supplied harmony jinja template renders through the
  native path; `shouldUseHarmony` (which inspects the *Go* template)
  is untouched, so harmony response parsing for existing Go-template
  gpt-oss models is unchanged.

</details>

<details>
<summary>Validation plan</summary>

- Unit tests (`template/jinja_test.go`): classification and
  validation — the four production template shapes in miniature
  (granite-style tool block, ChatML/phi-4 style, harmony style,
  expression-only markers), plus adversarial cases: unclosed blocks,
  stray/mismatched `end*` tags, unterminated delimiters and strings,
  `else` outside a block, oversized input, and delimiters hidden in
  string literals. Go templates — including every built-in `.gotmpl`
  shape — must classify as Go and render byte-for-byte as before.
- Unit tests (`create/manifest_test.go` or the package's existing
  manifest tests): a jinja `TEMPLATE` lands in the chat-template
  layer and removes inherited template layers; a Go `TEMPLATE` lands
  in the template layer exactly as before; malformed jinja returns
  `ErrBadTemplate`.
- Unit tests (`llm`): `appendJinjaArgs` emits
  `--chat-template-file` for a supplied template and keeps the
  `--no-jinja` placeholder behaviour when jinja is disabled.
- Unit tests (`server`): a model whose only template is the supplied
  layer reports `CapabilityTools` when the template references
  tools, and `llamaServerConfigForModel` carries the template file.
- Primo gates: `gofmt -l`, `go vet`, targeted `go test` for
  `template`, `create`, `llm`, `server`; full build.
- Live probe (port 11436, explicit `ROSE_HOST`/`ROSE_MODELS`, only
  after the CUDA-perf marker clears): create a model from the
  template-less granite-4.2-8b Q4_K_M GGUF with the upstream
  `chat_template.jinja` as `TEMPLATE` — no GGUF splice — then a
  structured `/api/chat` request with one `get_weather` tool must
  return HTTP 200 with a parsed `tool_calls` entry, and a plain chat
  round-trip must still answer. This is the exact request that
  returned HTTP 400 for the base model in 2026-10.

</details>

<details>
<summary>Validation record (2026-10-08, primo)</summary>

All gates ran on primo (Go 1.27.1) against a copy of this worktree in
`/home/phaedrus/rose-build/rose-jinja`:

- `gofmt -l` on `template`, `create`, `manifest`, `llm`, `server`:
  clean. `go vet` on the same packages: clean.
- `go test`: `template`, `create`, `manifest`, `llm` pass; full
  `server` suite passes (6.0 s); `go test -race` passes for
  `template` and `create`. `go build ./...` succeeds (one pre-existing
  cgo warning in the sqlite3 binding, unrelated).
- Live probe, after the CUDA-perf marker cleared: this build served
  on `127.0.0.1:11436` with
  `ROSE_MODELS=/home/phaedrus/.ollama/models`. The runner was the CPU
  llama-server build already present on primo, so timings are
  CPU-bound and say nothing about GPU performance.
  - `POST /api/create` for `hf-granite-4.2-8b-jinja` (base
    granite-4.2-8b Q4_K_M GGUF, digest
    `sha256:fee3f831…`, plus the upstream 9,013-byte
    `chat_template.jinja` as `template`): HTTP 200.
  - Tool chat (one `get_weather` schema, "What is the weather in
    Paris?"): **HTTP 200** with a parsed
    `tool_calls: [{name: "get_weather", arguments: {"city": "Paris"}}]`
    and a separate `thinking` field — the granite behaviour the
    GGUF-splice cure produced in 2026-10, now with no splice. The
    llama-server command line shows the mechanism:
    `--chat-template-file /home/phaedrus/.ollama/models/blobs/sha256-f0ba43f7…`
    (the supplied template's blob).
  - Control: the same GGUF created with no template
    (`hf-granite-4.2-8b-notmpl`) answered the identical request with
    **HTTP 400** `does not support tools` — the supplied template is
    what flipped the outcome.
  - Plain chat on the jinja model: HTTP 200, content `pong` for an
    exact-reply prompt. `/api/show`: capabilities
    `[tools, thinking, completion]`, template reported verbatim.
  - Both probe models were deleted and the probe server stopped
    afterwards; stock ollama on 11434 was never touched. Full logs:
    `/home/phaedrus/rose-build/rose-jinja/probe/` on primo.

Not covered by the probe: devstral, gpt-oss (harmony), and phi-4
templates (unit tests cover their shapes in miniature; only granite
ran live), multi-turn tool-result round-trips, and the CUDA
llama-server build (the probe used the CPU build because the CUDA
build tree was another workstream's in-progress artifact).

</details>
