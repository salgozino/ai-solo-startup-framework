# CEO orchestration loop: multi-round delegation with per-task memory

## Objective

Let a single CEO task survive multiple delegation rounds, remember what happened in the
earlier ones, judge each peer's answer, and decide the next delegation — so one human task
like *"plan A, have the engineer draft it, validate the draft, delegate the implementation,
then hand it to QA"* runs to completion without the human re-driving every hop.

## Problem

Every agent interaction is single-shot, and it is structural, not a missing flag.

`executeWithPolicy` (`core/supervisor/supervisor.go:354-390`) calls
`Provider.RunTask` at `:364`, and only *afterwards* runs the action intents at `:378`.
The CLI subprocess has already exited by the time the peer is contacted. The peer's answer
lands in `rec.Output` (`:476`), which is read by the store and the UI — and by **no model,
ever**.

Five distinct blockers, each with its own fix:

| # | Blocker | Evidence |
|---|---------|----------|
| B1 | No transcript on the persistence unit. `TaskRecord` is flat `Input` → `Output`. | `core/supervisor/store.go:18-46` |
| B2 | `RunTask` is called once per `Execute`. The only re-invocation hook (`executeResume:283-293`) re-runs the **original** `rec.Input` and discards the new input. | `supervisor.go:364`, `:283-293` |
| B3 | The model has no vocabulary for "another round" vs "I am done". MCP tools are generated **only** from `risk_policy`, and `buildAckResult` tells the model *"outcome unavailable this turn. Do not call again."* | `transport/mcp/tools.go:142`, `:109-125` |
| B4 | A peer that returns `INPUT_REQUIRED` hard-fails the delegating task (*"chained approval is not yet wired"*). The engineer can never ask a clarifying question, and any risky action in a peer kills the CEO's task. | `supervisor.go:570-571` |
| B5 | Task input is passed as a positional **argv** argument. Linux caps a single argv string at `MAX_ARG_STRLEN` (~131072 bytes). A growing transcript eventually fails `execve` with `E2BIG`. opencode is worse: the system prompt is concatenated into the same string. | `adapters/claudecode/adapter.go:283`, `adapters/opencode/adapter.go:230-234` |

## Why

The framework's whole premise is that the human talks only to the CEO and the CEO runs the
company. Today the CEO can fan out exactly one hop and then dies, so every orchestration
step costs a human round trip. This is the gap between "a company of agents" and "a task
router".

## Seams that already exist and are unplugged

These were designed for exactly this problem and never wired. Reuse them; do not reinvent.

- `core/supervisor/context.go` — `assembleBoundedContext` + `contextText`: complete,
  tested, **zero non-test callers**.
- `core/supervisor/supervisor.go:490` — `effectiveBudget()`: test-only.
- `core/port/provider.go:112-121` — `port.ResumePoint{TaskID, ApprovalToken, Input}`: zero
  usages anywhere. Its doc says *"no provider-side session is required"*.
- `core/port/delegator.go:37` — `PeerTaskState`, implemented at
  `transport/a2a/client.go:147`, no production caller. Half of a peer-wait already exists.
- `a2a.Message` carries unused `Metadata`, `ContextID`, `ReferenceTasks`
  (a2a-go v2.5.0 `a2a/core.go:200-228`); `client.go:118` sets none of them.

## Approach

**Fresh process + re-injected transcript.** Not a persistent CLI session.

Evidence for the fork (both `--help` outputs read directly):

- `claude` supports `--session-id <uuid>` (caller-chosen), `-r/--resume`, `--fork-session`
  — but `--no-session-persistence` states verbatim *"sessions will not be saved to disk and
  cannot be resumed"*. Using CLI sessions means dropping an isolation flag.
- `opencode` supports `-s/--session`, `-c/--continue`, `--fork`; `--pure` only disables
  plugins and does not conflict. It already emits `sessionID` on every NDJSON event and the
  adapter discards it.

Re-injection wins because the transcript stays in **our** store (so crash recovery keeps
working), no isolation flag is surrendered, and it is portable to any future provider. The
accepted cost is re-tokenizing the transcript each round.

**Park/resume, not a blocking loop.** A blocking loop inside `executeWithPolicy` is a
smaller diff, but while the task is `WORKING` the a2asrv execution stays registered, so any
resume is rejected with `ErrExecutionInProgress`
(`a2asrv/local_manager.go:209-211`) and `PostVerdict` returns 409 (`cmd/company/wire.go:136-138`).
That makes the task unapprovable and uncancellable for the whole conversation — and B4 means
the target scenario (engineer implements, QA verifies) cannot run at all. Park/resume is
roughly 4x the code and is the only shape that supports the requested scenario.

## Scope

Authorized edit roots:

- `core/supervisor/` — loop, task record schema, context assembly, tests
- `core/port/` — provider/delegator/resumer contracts
- `adapters/claudecode/`, `adapters/opencode/` — input delivery, session handling
- `transport/mcp/` — termination vocabulary, ack text
- `transport/a2a/` — peer follow-up, non-terminal peer states
- `cmd/company/` — wiring, multi-runtime UI aggregation
- `ui/` — multi-agent task view
- `agents/*.md` — persona rewrites (the current text actively sabotages the loop)
- `odd/tasks/ceo-orchestration-loop.md` — this document

Out of scope: policy engine semantics, the Telegram gateway, adding new gateways, the
`risk` field validation bug (tracked separately), CodeGraph/tooling config.

## Constraints

- TDD is mandatory (source: project `AGENTS.md`). Runner: `go test ./...`.
  RED must be observed before implementation; no invented evidence.
- `TaskRecord` is persisted as JSON. Every new field must be `omitempty` and zero-value
  safe, proven against literal pre-change JSON bytes (precedent:
  `TestStore_LoadsRecordWrittenBeforeRemainingIntents`).
- `Store.Save` rewrites the **entire per-agent array** on every call
  (`core/supervisor/store.go:129-145`), and the supervisor saves 8+ times per task. A
  transcript on `TaskRecord` makes every save O(N tasks x M turns). Measure before assuming
  it is fine.
- Default store base is `os.TempDir()` (`cmd/company/wire.go:288`). Transcripts in `/tmp`
  are a known smell; do not silently make it worse.
- The a2asrv in-memory task store is **gob-encoded**. `DataPart` carrying
  `json.RawMessage`/`jsontext.Value` fails to serialize (`supervisor.go:453-455`, `:720-721`).
  Prefer `Metadata`/`ContextID` plain strings over data parts.
- Output is capped at 1 MiB per adapter; on overflow NDJSON parsing is abandoned and raw
  bytes are returned (`claudecode/adapter.go:336-339`). A round can therefore yield noise.
- `dedupeKey(token, kind, payload)` (`transport/mcp/tools.go:91-104`) swallows an identical
  repeat. A legitimately repeated delegation across rounds must not be deduped away.
- Do not surrender the isolation flags (`--no-session-persistence`, `--pure`) to buy
  convenience.
- Existing behaviour that is already test-asserted must keep passing or be explicitly and
  deliberately overturned, with the replacement named in a comment.

## Tasks

Slice 1 — transcript persistence
- [x] **T1** — RED: test that a `TaskRecord` round-trips a non-empty turn list and that
      literal pre-change JSON still loads with the new field as zero value.
- [x] **T2** — Add `Turns []port.ContextMessage` to `TaskRecord` (`omitempty`, zero-value
      safe), following the `RemainingIntents` precedent.
- [x] **T3** — Measure `Store.Save` cost with a realistic transcript and record the number
      here. If it is bad, decide now whether the transcript moves to a sibling per-task
      file, before any other slice depends on the layout.

Slice 2 — input delivery
- [ ] **T4** — RED: test proving an input larger than the argv ceiling is delivered intact.
- [ ] **T5** — Move the prompt from argv to stdin in `claudecode` (verified: `claude -p`
      reads stdin). Keep `--system-prompt-file` as is.
- [ ] **T6** — Same for `opencode`. **Blocked**: the local `opencode` CLI currently fails on
      every invocation, argv included, so stdin support is unverified. Re-run the spike
      before implementing; do not guess.

Slice 3 — the second turn
- [ ] **T7** — RED: test proving a CEO turn that delegates gets a second `RunTask` call
      whose input contains the peer's output.
- [ ] **T8** — Re-invoke the provider after a delegation round with the assembled
      transcript. Bounded rounds; exhaustion must be an explicit terminal state, not a hang.
      This is where `assembleBoundedContext` / `contextText` / `effectiveBudget` finally get
      production callers — wiring them earlier would change the first turn's input for no
      reason.
- [ ] **T9** — Stop `rec.Output` from being clobbered by the peer's raw text
      (`supervisor.go:476`, `:326-328`). `Output` must end up as the CEO's final answer.
      This deliberately overturns a test-asserted decision; name the replacement.

Slice 4 — termination vocabulary
- [ ] **T10** — RED: test proving the model can end the loop explicitly, and that a missing
      signal hits the round cap instead of looping forever.
- [ ] **T11** — Introduce the "continue / done" signal. Decide and record: a non-policy MCP
      tool (breaks the "every tool is a policy-classified action" invariant) versus a
      `risk: safe` policy kind (pollutes `risk_policy` with non-actions).
- [ ] **T12** — Rewrite `buildAckResult` (`transport/mcp/tools.go:109-125`). Its current
      text becomes false the moment T8 lands.

Slice 5 — personas
- [ ] **T13** — Rewrite `agents/ceo.md` (`:24-36`, `:48-59`) and `agents/engineer.md`
      (`:30-39`, `:50-53`). They currently instruct the model that memory is impossible and
      that outcomes never arrive. Left alone, they will actively sabotage the loop.

Slice 6 — park/resume
- [ ] **T14** — RED: test proving a parked CEO task is resumable and approvable while it
      waits on a peer.
- [ ] **T15** — Add `port.TaskResumer` + `Supervisor.SetResumer` and the awaiting-peer
      fields on `TaskRecord`; replace the blocking wait with park + watcher, reusing
      `PeerTaskState`.
- [ ] **T16** — Prove crash recovery across a round boundary: restart mid-conversation and
      continue.

Slice 7 — chained approval
- [ ] **T17** — RED: test proving a peer escalation reaches the human instead of failing the
      delegating task.
- [ ] **T18** — Remove the hard-fail at `supervisor.go:570-571` and propagate the peer's
      `INPUT_REQUIRED` upward.
- [ ] **T19** — Let the CEO send a follow-up to an existing peer task
      (`msg.TaskID = peerTaskID`); `transport/a2a/client.go:118` never sets it today. The
      SDK already supports this.

Slice 8 — multi-agent UI
- [ ] **T20** — RED: test proving a non-CEO agent's task is listed and approvable.
- [ ] **T21** — Aggregate every runtime in the UI instead of `runtimes[0]`
      (`cmd/company/main.go:67`, `:72`).

## Acceptance criteria

- A single human task drives at least three delegation rounds with the CEO reading and
  judging each peer answer, proven by an integration test.
- The CEO's second and later turns demonstrably receive the earlier rounds' content.
- A parked CEO task is approvable and cancellable while it waits on a peer.
- A peer escalation reaches the human; it does not fail the delegating task.
- A restart mid-conversation resumes without losing earlier rounds.
- Round exhaustion produces an explicit terminal state, never a hang.
- Records written before this change still load (literal pre-change JSON, not a
  re-marshalled struct).
- `go build ./cmd/company`, `go vet ./...` and `go test ./...` all clean.

## Checks

- `go build ./cmd/company`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `gofmt -l .`

## Delivery

Forecast: ~2250 authored changed lines across 8 slices — far past the ~400 heuristic, so
this ships as a chain, never as one PR. Each slice above is one PR boundary and each task
closes with at least one work-unit commit carrying its tests.

Chain strategy: **feature-branch-chain**, matching the convention this repo already uses
(tracker `feat/agent-delegation-over-a2a` with stacked `pr1..prN` children, tracker PR #75).

- Tracker branch: `feat/ceo-orchestration-loop` — draft/no-merge PR, accumulates the whole
  feature, and is the only branch that merges to `master`.
- Child branches: `feat/ceo-orchestration-loop-pr<N>-<slice>`. PR #1 targets the tracker;
  every later child targets the immediate previous child branch, so each review diff shows
  only its own slice.
- Every child PR carries a dependency diagram marking itself with a pin, plus start, end,
  prior dependencies, follow-ups and out-of-scope items.

Slice 6 is the largest and may need splitting once its RED is written.

### Slice 1 PR size: `size:exception`, maintainer-approved

Slice 1 closes at 539 changed lines against the tracker, 35% over the ~400 heuristic. A
cohesive split existed and was offered — PR 1a "add the field" (`store.go` +
`store_test.go`, ~285 lines) and PR 1b "measure the cost and decide the layout"
(`store_bench_test.go` + the T3 verdict, ~255 lines). The maintainer explicitly chose one
PR instead, so this slice ships under `size:exception`.

Recorded because the heuristic exists to protect reviewers, and an exception is only
legitimate when it is deliberate and visible:

| Part | Lines | Review load |
|---|---|---|
| `core/supervisor/store.go` | 11 | production, one additive field |
| `core/supervisor/store_test.go` | 203 | tests |
| `core/supervisor/store_bench_test.go` | 153 | benchmark |
| `odd/tasks/ceo-orchestration-loop.md` | 172 | progress log, read as context |

356 of the 539 lines are Go tests and 172 are a progress log; production code is 11 lines.
This exception applies to Slice 1 only. Later slices carry real production weight and are
expected to split rather than repeat it.

## Route

Delegated direct. The writer trigger fires on every slice: each one touches 2+ non-trivial
files. One bounded writer per slice, no parallel writers in this worktree. Per-slice
exploration only when the slice's blast radius is not already mapped here.

## TDD

Mode: on. Source: project `AGENTS.md` ("TDD is mandatory for this project").
Runner: `go test ./...`.

## Progress

- Architecture mapped through two read-only explorations plus direct `--help` verification
  of both provider CLIs. Findings recorded above with file:line evidence.
- Fork decided: fresh process + re-injected transcript, park/resume rather than a blocking
  loop. Rationale and the rejected alternative are recorded under Approach.
- Spike (Fase 0) partially complete: `claude -p` reads the prompt from **stdin**, verified
  by observed output. `opencode run` could not be verified — it currently fails on every
  invocation including the argv path the adapter uses today, so the failure says nothing
  about stdin. T6 stays blocked on re-running this spike.
- Baseline before any change: `gofmt -l .`, `go build ./cmd/company`, `go vet ./...` and
  `go test ./...` all clean on `master`.
- Chain strategy resolved: feature-branch-chain, matching the repo's existing convention.
- T3 was rewritten. It originally asked for `assembleBoundedContext` / `contextText` /
  `effectiveBudget` to gain production callers in Slice 1, which is wrong: with no second
  turn yet, wiring them would only change the first turn's input for no benefit. That work
  moved to T8, where it is actually needed. Slice 1 stays pure persistence plus the
  `Store.Save` cost measurement that decides the transcript's storage layout.

### Slice 1 — complete (T1, T2, T3)

Branch: `feat/ceo-orchestration-loop-pr1-transcript-persistence`. Commits:

- `4bdc9bb` `feat(supervisor): persist per-task turn transcript on TaskRecord` (T1 + T2)
- `b5f33c3` `test(supervisor): benchmark Store.Save cost with a realistic transcript` (T3)

**T1 — RED observed.** `go test ./core/supervisor/ -run 'TestStore_RoundTripsTurns|TestStore_LoadsRecordWrittenBeforeTurns'`,
verbatim:

```
# github.com/salgozino/ai-solo-startup-framework/core/supervisor [.../core/supervisor.test]
core/supervisor/store_test.go:236:3: unknown field Turns in struct literal of type TaskRecord
core/supervisor/store_test.go:250:13: got.Turns undefined (type TaskRecord has no field or method Turns)
core/supervisor/store_test.go:250:31: rec.Turns undefined (type TaskRecord has no field or method Turns)
core/supervisor/store_test.go:251:53: got.Turns undefined (type TaskRecord has no field or method Turns)
core/supervisor/store_test.go:251:69: rec.Turns undefined (type TaskRecord has no field or method Turns)
core/supervisor/store_test.go:253:27: rec.Turns undefined (type TaskRecord has no field or method Turns)
core/supervisor/store_test.go:254:12: got.Turns undefined (type TaskRecord has no field or method Turns)
core/supervisor/store_test.go:310:9: got.Turns undefined (type TaskRecord has no field or method Turns)
core/supervisor/store_test.go:311:43: got.Turns undefined (type TaskRecord has no field or method Turns)
FAIL	github.com/salgozino/ai-solo-startup-framework/core/supervisor [build failed]
```

A second, self-inflicted RED is worth recording because it nearly produced a lying test: the
backward-compat test first seeded task id `task-preturns` and asserted on the bare substring
`turns`, which the task id itself contains. The assertion failed against correct code. Fixed
in the test (quoted JSON key `"turns"`, task id `task-legacy-schema`), not in `store.go`. No
existing test's assertions were modified.

**T2.** `Turns []port.ContextMessage` with `json:"turns,omitempty"` on `TaskRecord`
(`core/supervisor/store.go`). `core/port` was NOT modified: `port.ContextMessage` already
round-trips through `encoding/json` unchanged (exported fields, `time.Time` carries its own
marshaller). Schema only — nothing writes the field this slice, by design.

**T3 — measured.** `go test -bench . -benchmem -benchtime=3s -count=3 -run '^$' ./core/supervisor/`,
AMD Ryzen 7 PRO 6850U, linux/amd64, go1.27.0. Median of 3, 20 tasks in the agent file, each
record carrying 10 turns x 4 KiB:

| store | on-disk file | ns/op (no transcript) | ns/op (transcript) | B/op (no transcript) | B/op (transcript) |
|---|---|---|---|---|---|
| 20 tasks | 1.97 KiB → 814 KiB | 134,897 | 6,100,565 | 18,015 | 5,624,102 |
| 100 tasks | 9.86 KiB → 4.0 MiB | 225,991 | 25,648,803 | 73,781 | 26,813,881 |
| 500 tasks | 49.3 KiB → 19.9 MiB | 658,464 | 142,288,310 | 339,425 | 150,783,013 |

At the realistic 20-task point the transcript costs **45x more time** (6.10 ms vs 0.135 ms)
and **312x more allocation** (5.4 MiB vs 18 KiB) per save. Cost is linear in file bytes
(5x the tasks → 4.2x then 5.5x the time), confirming the predicted O(N tasks x M turn bytes).
Allocation tracks ~7x the file size — the unmarshal-plus-marshal working set. Across the 8
`Store.Save` call sites in `supervisor.go` (`:247, :288, :303, :347, :387, :430, :450, :609`)
that is ~49 ms and ~43 MiB of garbage per task at 20 tasks, ~1.14 s and ~1.15 GiB at 500.
One 500-task sample hit 492 ms on a GC pause; the median is reported.

**T3 verdict — keep `Turns` on `TaskRecord`. It does not need to move before later slices,
because no later slice can depend on the layout.**

The task framing assumed the on-disk layout is a shared contract. Verified: it is not.
`TaskRecord` is JSON-encoded in exactly two places, both private to `store.go`
(`:116` unmarshal, `:124` marshal). Every consumer reads the Go struct via `Store.Load`/
`LoadAll` — `supervisor.go`, and `ui/handler.go` through its own deliberately decoupled
`ui.TaskRecord`, converted field-by-field at `cmd/company/wire.go:113`. Splitting the
transcript into a sibling per-task file later is therefore a pure `Store` internal refactor
with a zero-line blast radius outside `core/supervisor/store.go`. Slices 3-8 couple to the
**field**, never to where the bytes live. Paying for that refactor now would buy no optionality.

At the realistic operating point the cost is also genuinely noise: ~49 ms of `Save` per task
against a `RunTask` that spawns an LLM CLI subprocess measured in seconds to minutes — well
under 1% of one round.

This is not an unconditional "it is fine". Two unbounded multipliers make it a real future
problem, just not a Slice 1 one:

- `Store.Delete` has exactly ONE production caller, `Cancel` (`supervisor.go:583`). Completed
  tasks are never pruned, so N grows for the life of the process, and the default store base
  is `os.TempDir()` (`wire.go:288`), which nothing prunes between runs either.
- Slice 3 pushes on both axes at once: re-invocation raises saves-per-task above 8, and the
  transcript grows per round so each save costs more within a single task. This benchmark
  held turns fixed at 10; the real loop will not.

Named trigger instead of vibes: revisit in **Slice 3**, when turns are actually written, and
reach for the cheaper lever first — pruning terminal tasks (the missing `Store.Delete` caller)
keeps N small and keeps the shared-array layout viable indefinitely. The sibling-file split
is the second lever, available at any time at zero consumer cost.

**Verification, observed:**

- `gofmt -l .` — no output (clean)
- `go build ./cmd/company` — OK
- `go vet ./...` — OK
- `go test ./...` — all 12 packages `ok`
- `go test -race ./core/supervisor/` — `ok` 1.294s
- `go test -bench . -benchmem ./core/supervisor/` — numbers above

### Slice 1 — native review

Lineage `review-787c47fe13b36fa2`, one lens (`review-reliability`), risk `medium`,
4 files / 384 lines. Result: **approved**, authority acknowledged and burned. No blocker,
no correction opened. Four advisory findings, all non-blocking and recorded here as
follow-ups rather than as reasons to re-review this candidate:

- **R3-toolchain-floor** (SUGGESTION) — `b.Loop()` and range-over-integer need a recent Go
  language version, which the reviewer could not see from the patch. **Closed by evidence**:
  `go.mod` declares `go 1.25.0`; `b.Loop()` landed in 1.24 and range-over-integer in 1.22,
  and CI resolves its toolchain with `go-version-file: go.mod`. No action needed.
- **R3-single-record-store** (SUGGESTION) — both new tests use a store holding exactly one
  record, so nothing proves that saving one task preserves a *different* task's transcript
  in the same per-agent file. Given that `Store.Save` rewrites the whole array, this is the
  most likely silent-clobber failure mode. Real gap; cheap to close at unit level.
  **Closed** by `TestStore_SavePreservesOtherRecordTurns` (`core/supervisor/store_test.go`):
  two tasks with distinct transcripts, one saved, the neighbour asserted intact by whole-value
  comparison plus a `LoadAll` count so a dropped or duplicated record also fails.
- **R3-roundtrip-field-subset** (SUGGESTION) — the round-trip test compares `Role`,
  `Content` and `At` individually instead of the decoded message as a whole, so a future
  field on `port.ContextMessage` could fail to persist and leave the test green.
  **Closed**: `TestStore_RoundTripsTurns` now compares each decoded message as a whole value
  via `reflect.DeepEqual`, so a new field is covered automatically. `reflect.DeepEqual` is
  correct here only because the fixtures are fixed UTC instants — `time.Time` carries a
  monotonic reading and a `*Location` that do not survive JSON — and that property is now
  stated in the test.

**Teeth proven by mutation, not by assertion.** Both tests are coverage, not bug fixes, and
both passed the moment they were written: `Store.Save` was already correct. No RED was
fabricated. Each was instead proven non-vacuous by temporarily breaking the behaviour it
claims to protect, and both mutations were reverted (`git diff core/supervisor/store.go` and
`git diff core/port/provider.go` both empty).

Mutation A — `Save` nils every *other* record's `Turns` before writing the array:

```
=== RUN   TestStore_SavePreservesOtherRecordTurns
    store_test.go:330: neighbour Turns: got [], want [{Role:assistant Content:neighbour round one At:2026-09-23 10:00:00 +0000 UTC} {Role:peer Content:neighbour round two At:2026-09-23 10:00:30 +0000 UTC} {Role:assistant Content:neighbour round three At:2026-09-23 10:01:00 +0000 UTC}]
--- FAIL: TestStore_SavePreservesOtherRecordTurns (0.00s)
```

Under that mutation every *other* `TestStore_*` test — including both Slice 1 tests as
originally written — still passed. That is the finding's whole point, now demonstrated
rather than argued.

Mutation B — a future field added to `port.ContextMessage` that fails to persist
(`Tokens int \`json:"-"\``, set in the fixture):

```
=== RUN   TestStore_RoundTripsTurns
    store_test.go:265: Turns[0]: got {Role:assistant Content:delegating the draft to the engineer At:2026-09-23 10:00:00 +0000 UTC Tokens:0}, want {Role:assistant Content:delegating the draft to the engineer At:2026-09-23 10:00:00 +0000 UTC Tokens:7}
    store_test.go:265: Turns[1]: got {Role:peer Content:draft ready: three phases At:2026-09-23 10:01:30 +0000 UTC Tokens:0}, want {Role:peer Content:draft ready: three phases At:2026-09-23 10:01:30 +0000 UTC Tokens:11}
--- FAIL: TestStore_RoundTripsTurns (0.00s)
```

`Role`, `Content` and `At` all match in that output, so the previous field-subset comparison
would have stayed green while `Tokens` silently vanished.
- **R3-bench-quadratic-seed** (WARNING) — the benchmark seed loop calls `Save` once per
  task, and `Save` rewrites the whole array, so setup is quadratic in file bytes: the
  500-task transcript sweep writes on the order of gigabytes before the timer starts, once
  per sub-benchmark per `-count`. It makes `go test -bench .` slow and temp-dir dependent.
  Seeding the array in one write would fix it without changing what is measured.

## Next step

Slice 2, T4 (RED): input larger than the argv ceiling delivered intact. T6 remains blocked
on re-running the `opencode` stdin spike.

Slice 1's review findings are settled: R3-toolchain-floor closed by evidence,
R3-single-record-store and R3-roundtrip-field-subset folded into this slice and closed above.
R3-bench-quadratic-seed stays an open follow-up — it costs `go test -bench .` time only, not
correctness, and is best paid alongside the Slice 3 benchmark revisit the T3 verdict already
schedules.
