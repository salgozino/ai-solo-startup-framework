# Exploration: Provider Tool Use & Action Intent Declaration

## Current State

### The provider/adapter/supervisor boundary

`core/port/provider.go` defines the `Provider` interface. Its `RunTask(ctx, taskID, input) (ProviderResult, error)`
method is the local-execution path the supervisor uses (as opposed to `SendMessage`/`SendTask`,
the A2A delegation path). `ProviderResult` already carries `ActionIntents []ActionIntent`
(`Kind string`, `Payload map[string]any`), and `ActionIntent` is a first-class type. Nothing about
this type is missing — the contract exists.

`core/supervisor/supervisor.go:executeWithPolicy` (line 314) already fully implements the
consumption side of that contract:

- Calls `s.cfg.Provider.RunTask(...)`.
- Iterates `result.ActionIntents`.
- For each intent, calls `s.cfg.PolicyEngine.Classify(policy.ActionIntent{Kind: intent.Kind}, s.cfg.Role, s.cfg.PolicyConfig)`.
- Routes `HardDeny → REJECTED`, `Escalate → INPUT_REQUIRED` (persists `PendingIntentKind`/`PendingIntentBody`
  via `extractBody`, which reads `intent.Payload["body"]`), `Permit → executeAction` (calls
  `s.cfg.Gateway.Send` with an approval token minted by `PolicyEngine`).
- `core/policy/engine.go:Classify` implements the two-stage capability+risk check and is fully
  tested (`core/policy/policy_test.go`, `core/policy/resume_test.go`, `core/supervisor/policy_test.go`).
- `gateways/telegram/gateway.go` implements `port.Gateway` and is contract-tested against both the
  real Telegram HTTP API (via `httptest`) and `fake.Gateway`.

**The entire policy → escalation → gateway pipeline is implemented, wired, and tested end-to-end**
— but only ever exercised with `core/port/fake.Provider`, whose `RunTask` returns
`ActionIntents` the test supplies directly (e.g. `cmd/company/e2e_test.go:139`,
`core/supervisor/policy_test.go:117`). Every one of those e2e tests literally constructs the
`ActionIntents` slice by hand before calling `RunTask` — none of them exercise a real subprocess.

### Where the chain actually breaks

Both real adapters' `RunTask` implementations are near-identical: spawn a subprocess with an argv
slice, capture stdout up to a byte cap, and return:

```go
return port.ProviderResult{Output: output}, nil   // adapters/claudecode/adapter.go:131
return port.ProviderResult{Output: output}, nil   // adapters/opencode/adapter.go (equivalent)
```

`ActionIntents` is never assigned. There is no code path in either adapter that:
1. Declares available action kinds to the underlying LLM.
2. Parses the CLI's stdout (or any other output channel) for a structured action-intent
   representation.

So issue #50 is precisely correct: `Complete` (COMPLETED state) fires, but zero side effects ever
occur, because the adapters are pure text-in/text-out and `ActionIntents` stays `nil` for every
real invocation. The break is 100% in the two adapter files, not in the port contract or the
supervisor/policy/gateway chain.

### The unimplemented capability spec (C-1)

`openspec/changes/agent-startup-framework-foundation/specs/provider-adapter/spec.md` contains
**Requirement: Providers Declare Their Capabilities** (lines 46–56):

> A conforming provider MUST expose a capability declaration including its context budget and the
> action kinds it can emit as intents, queryable by the supervisor before or independently of any
> specific invocation.
>
> Scenario: Supervisor reads provider capabilities before assembling context — GIVEN a supervisor
> is about to invoke a provider, WHEN it queries the provider's capability declaration, THEN it
> receives a context budget value it can use to cap the context it assembles.

This is a real, distinct requirement from "Providers Return Action Intents, Never Execute Actions
Directly" (also in the same spec, already satisfied by the `ProviderResult` contract itself). The
capability-declaration requirement demands:
- A **queryable, provider-owned** capability object (not a config file value, not a supervisor field).
- It must expose **context budget** and the **action kinds** the provider can emit.
- It must be queryable **independently of invocation** — i.e., the supervisor can ask "what can you
  do" before ever calling `RunTask`.

Verified independently: `grep -rn "Capabilit" --include="*.go" .` across the whole module returns
exactly one hit outside test/support files — a dangling doc comment,
`core/port/provider.go:101: // It is pre-capped by the supervisor according to ProviderCapabilities.ContextBudget.`
There is no `ProviderCapabilities` type anywhere. `ContextBudget` is a field on
`core/supervisor/supervisor.go:29` (`Config.ContextBudget int`) — i.e., it is
**supervisor-owned and operator-configured**, not provider-declared. This inverts the spec's
ownership direction exactly as the verify report states. Zero `_test.go` files reference
`Capabilit` anywhere in the module. `tasks.md:194`'s claim of full requirement coverage for
`provider-adapter` is false for this specific requirement.

## Affected Areas

- `core/port/provider.go` — needs a `ProviderCapabilities` type (or equivalent) and a way to query
  it; currently only the dangling comment references it.
- `core/supervisor/supervisor.go` — `Config.ContextBudget` (operator/config-owned) needs to
  reconcile with a provider-declared budget; `executeWithPolicy` needs no changes to its
  consumption logic (already correct) but may need to query capabilities before invocation.
- `adapters/opencode/adapter.go` — `RunTask` never populates `ActionIntents`; no capability
  declaration exists; no mechanism to pass tool/action schemas to the `opencode` CLI.
- `adapters/claudecode/adapter.go` — same gap, `claude` CLI.
- `core/policy/engine.go` — unaffected; `Classify` already consumes `ActionIntent.Kind` correctly
  and requires no changes.
- `gateways/telegram/gateway.go` — unaffected; already the terminal executor for `Permit`ted intents.
- `config/schema.go` — `Policy` (risk_policy entries) is keyed by action kind string
  (`telegram_send`) but there is no schema for what payload shape each action kind expects; this
  may need extension if a formal tool-declaration schema is introduced.
- `cmd/company/e2e_test.go` — all `ActionIntents`-driven e2e tests hand-construct intents via
  `fake.Provider`; none exercise a real adapter. A real e2e ("send a telegram saying hello" →
  actual delivery) requires either a live CLI or a much higher-fidelity fake that exercises the
  new declare/parse path.

## Approaches

### 1. MCP-server-backed tool declaration (native tool use)

Both `claude` and `opencode` CLIs support declaring custom tools via MCP (`claude --mcp-config`,
`opencode mcp add`). The adapter would start a local MCP server (stdio transport, in-process, no
network) exposing one tool per action kind declared in `risk_policy` (e.g. `telegram_send`), spawn
the CLI with `--mcp-config <ephemeral-config>` (`claude -p --output-format stream-json ...`) or
the opencode equivalent, and parse `tool_use` events from the structured output stream
(`--output-format stream-json` for Claude; `opencode run --format json` for OpenCode) to build
`ActionIntent`s from the MCP tool-call arguments.

- Pros: This is each CLI's actual, documented tool-use mechanism — the LLM genuinely "declares" it
  wants to invoke a tool via the provider's own protocol, satisfying issue #50's acceptance
  criterion literally. Tool schemas are enforced by the model provider's function-calling machinery,
  not by prompt convention.
- Cons: Significant new surface: an in-process MCP server (stdio JSON-RPC) per adapter, per
  invocation; lifecycle management (start server before spawning CLI, tear down after); parsing
  `stream-json`/`--format json` event streams instead of a single stdout blob (breaks the current
  "capture bytes up to a cap" read loop); the exact event schema for MCP-tool `tool_use` blocks in
  each CLI's structured output is **not yet verified against real transcripts** in this repo.
  Confirmed via `opencode --help`-equivalent docs that `opencode run --format json` exists and
  emits "raw JSON events," and Claude's `--output-format stream-json` is real and documented — but
  neither CLI's tool-use event schema has been captured firsthand here.
- Effort: High.

### 2. Prompt-convention structured output parsing

Keep the adapters as pure subprocess spawn + stdout capture (current architecture unchanged).
Append an instruction to the system prompt (already prepended for opencode via
`systemPromptContent`; Claude already supports `--system-prompt-file`/`--append-system-prompt`)
telling the model to emit a delimited JSON block describing desired actions
(e.g. `<<<ACTIONS>>>{"actions":[{"kind":"telegram_send","body":"..."}]}<<<END>>>`) at the end of
its response. The adapter parses stdout for that block after the CLI exits, strips it from
`Output`, and converts entries into `ActionIntent`s.

For Claude specifically, `--json-schema` (print mode only) can force the *entire* response to
validate against a JSON Schema — this could replace ad-hoc delimiter parsing with schema-enforced
output, but only for Claude; no equivalent flag was found in OpenCode's documented CLI surface, so
using it would mean the two adapters diverge in mechanism, not just in flags.

- Pros: No new subprocess-within-subprocess complexity; fits the adapters' existing "spawn,
  capture-to-limit, return" shape almost unchanged; works uniformly across both CLIs (delimiter
  parsing is CLI-agnostic even if `--json-schema` is Claude-only); testable today with the existing
  fake-binary test-double pattern (`contract_test.go` style).
- Cons: Relies on prompt compliance, not a guaranteed mechanism — the model can ignore the
  instruction, malform the JSON, or emit it mid-stream if truncated by the output byte cap; is not
  "true" tool use in the sense the issue's acceptance criteria implies ("LLM can invoke actions"),
  though it does satisfy the criteria's observable behavior (adapter parses tool-call-like output
  into `ActionIntent`s).
- Effort: Low–Medium.

### 3. Hybrid: attempt MCP/native tool use where available, fall back to structured-output parsing

Use approach 1 when the CLI's structured-output mode reliably surfaces MCP tool calls (verify per
CLI in the research lane below), and fall back to approach 2 for the other CLI, or as a resilience
net when the model doesn't invoke the declared tool but does comply with the fallback prompt
convention.

- Pros: Gets the "real" mechanism where available while not blocking on both CLIs supporting it
  identically; adds a safety net against model non-compliance.
- Cons: Two code paths to maintain and test per adapter; highest overall complexity; the
  fallback's non-determinism (which path actually fired) complicates observability and testing.
- Effort: High.

## Recommendation

**Approach 2 (prompt-convention structured output parsing), built directly against the
`ProviderCapabilities` type from C-1.**

Reasoning:
- The issue's underlying acceptance criteria are behavioral ("adapter can declare available
  actions", "adapter parses them into ActionIntents", "supervisor classifies and executes") — they
  do not require the MCP protocol specifically. Approach 2 satisfies every acceptance criterion
  without introducing an in-process MCP server, stdio JSON-RPC framing, or per-invocation server
  lifecycle management into two subprocess-spawning adapters that are currently simple and
  well-tested with fake-binary doubles.
- Approach 1's core premise — that both CLIs' structured-output modes cleanly surface MCP
  tool-call events in a way this framework can parse deterministically — is **unverified**. This
  is exactly the kind of claim that needs source-backed evidence (a captured real transcript)
  before being load-bearing for an architecture decision, not something to assume from CLI `--help`
  text.
- Approach 2 keeps both adapters symmetric (same parsing mechanism for both CLIs), which matters
  because the existing `provider-adapter` spec explicitly requires provider-agnostic conformance
  ("any conforming provider ... plugs into the same supervisor lifecycle without the framework core
  knowing which provider it is").
- If real-world testing during implementation shows the delimiter-based parsing is too fragile,
  approach 3's fallback shape is a natural incremental upgrade — it does not require re-architecting
  approach 2's foundation, only adding an additional parse attempt before the fallback.

**Do not build approach 1 in this change.** Flag it as a candidate for a later "real MCP tool use"
change, gated on the research lane below actually producing transcript evidence.

### Where capability declaration should live

**Provider-owned**, per the spec's explicit wording ("A conforming provider MUST expose a
capability declaration ... queryable by the supervisor"). Concretely:

- Add `ProviderCapabilities{ ContextBudget int; ActionKinds []string }` to `core/port/provider.go`.
- Add a method to the `Provider` interface — e.g. `Capabilities() ProviderCapabilities` — so it is
  queryable independently of `RunTask`, matching the spec's "before or independently of any
  specific invocation" language.
- Each adapter (`opencode.Adapter`, `claudecode.Adapter`) implements `Capabilities()` returning its
  own context budget and the action kinds it supports declaring (derived from `risk_policy` at
  construction time, since the adapter needs to know which action kinds to embed in its
  system-prompt convention for approach 2 anyway).
- `core/supervisor/supervisor.go`'s `Config.ContextBudget` should be **reconsidered**: either (a)
  removed in favor of always querying `Provider.Capabilities().ContextBudget`, or (b) kept as an
  optional operator override that is only used when non-zero, with the provider's declared value as
  the default. Given `assembleBoundedContext(history, s.cfg.ContextBudget)` is called at
  `executeDelegation` time — the A2A delegation path, which does not have a local `Provider` with
  `RunTask` semantics necessarily — full removal needs care; keeping `Config.ContextBudget` as an
  override is the lower-risk path and should be the design's default unless the design phase finds
  a clean reason to fully invert it.

This directly resolves C-1: the type will exist, be tested, and be the source of truth the spec
demanded, while `Config.ContextBudget` stops being the sole/authoritative source.

## Risks

- **Model non-compliance (approach 2)**: the LLM may ignore the structured-output instruction, or
  emit malformed JSON. Mitigate with strict, tested parsing that fails closed (no `ActionIntent` ⇒
  no chance of accidental permit) and add adapter-level unit tests with fake-binary doubles that
  simulate malformed/partial output.
- **Output truncation collision**: the existing byte-cap truncation (`TruncationMarker`) could cut
  a structured-actions block mid-JSON if it appears near the tail of a large response. The parsing
  design must place the actions block in a position/format resilient to truncation, or the adapter
  must special-case: if truncation occurred, do not attempt to parse an actions block (fail closed,
  not fail-open).
- **`ContextBudget` ownership inversion touches the A2A delegation path too**: `executeDelegation`
  reads `s.cfg.ContextBudget` directly; changing this needs to be sequenced so the delegation path
  (which doesn't necessarily have the same "provider" semantics as `RunTask`) isn't broken.
- **Spec drift risk if unified spec isn't written carefully**: the change legitimately touches two
  requirements from two different specs (`provider-adapter`'s two requirements: "Return Action
  Intents" already satisfied at the contract level, and "Declare Capabilities" unimplemented) plus
  new behavior not in either original spec (actually populating `ActionIntents` from a real CLI).
  The delta spec for this change must be explicit about which requirement it is completing
  (capability declaration) vs. which behavior is net-new (adapter-side parsing/emission), so
  `sdd-archive` doesn't silently merge an incomplete picture into `openspec/specs/`.
- **No real e2e coverage exists today** for the adapter → parse → ActionIntent path; all current
  e2e tests bypass the adapters entirely via `fake.Provider`. New tests are needed at the adapter
  level (fake-binary doubles emitting the structured-actions convention) since a true CLI-backed
  e2e is expensive/flaky in CI.

## Research lanes

The following require external, source-backed evidence beyond what static code reading in this
repo can establish — recommend routing to `sdd-research` before the design phase commits to
adapter-level parsing mechanics:

1. **Claude Code `--output-format stream-json` tool-use event schema**: capture a real transcript
   of `claude -p --output-format stream-json --mcp-config <config> "..."` invoking a
   custom/MCP-declared tool, to determine whether `tool_use` blocks for MCP tools are
   distinguishable/parseable from built-in tool calls (Bash, Edit, etc.) in the event stream. This
   directly informs whether approach 1 or approach 3 is viable later.
2. **OpenCode `opencode run --format json` event schema**: same question for OpenCode — does the
   "raw JSON events" format expose MCP tool-call arguments in a parseable shape, and does
   `opencode mcp add` support project-scoped/ephemeral config suitable for per-invocation tool
   declaration, or only a persistent global/user config?
3. **Claude `--json-schema` interaction with system-prompt-based action instructions**: whether
   `--json-schema` (print mode) can be combined with the existing `--system-prompt-file` flag this
   adapter already uses, and whether forcing the entire response to validate against a schema is
   compatible with also returning free-form task `Output` text (the schema may need to include an
   `output` field alongside an `actions` field, rather than being used for actions alone).
4. **Version pinning**: the CLI reference confirms many flags are version-gated (e.g.
   `--forward-subagent-text` requires Claude Code v2.1.211+). The adapters must document a minimum
   supported CLI version once a mechanism is chosen, since `--output-format`/`--input-format` for
   print mode are not confirmed available in every historical Claude Code version this framework
   might be run against.
5. **OpenCode structured-output truncation behavior**: whether `opencode run --format json` streams
   incrementally (so the existing `io.LimitReader` byte-cap read loop needs restructuring to parse
   line-delimited JSON events rather than a single blob) or buffers and prints once at the end.

## Ready for Proposal

**Yes.** The investigation is conclusive: the port contract (`ActionIntent`/`ProviderResult`) and
the consumption chain (`Supervisor.executeWithPolicy` → `policy.Engine.Classify` → `Gateway.Send`)
are already fully implemented and tested; the sole gap is that neither real adapter ever populates
`ActionIntents`, and the `ProviderCapabilities` type the spec demanded was never created. The
orchestrator should proceed to `sdd-propose` with:
- Scope: add `ProviderCapabilities` (provider-owned, queryable) to `core/port/provider.go` and the
  `Provider` interface; implement approach 2 (structured-output-convention parsing) in both
  `adapters/opencode/adapter.go` and `adapters/claudecode/adapter.go`; add adapter-level unit tests
  with fake-binary doubles proving `ActionIntents` gets populated end-to-end for at least one real
  subprocess spawn; reconcile `Config.ContextBudget` with the new provider-declared value.
- Explicitly out of scope: MCP-server-backed native tool use (approach 1/3) — defer pending the
  research lanes above.
- The delta spec (`sdd-spec`) must revise `provider-adapter`'s "Providers Declare Their
  Capabilities" requirement (MODIFIED, since it exists but is unimplemented) and likely add a new
  requirement for adapter-side action-intent emission (ADDED), since that specific behavior — "the
  adapter parses CLI output into ActionIntents" — was never actually specified anywhere, only
  implied by the port contract's shape.
