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

Marker legend: `[ ]` not started · `[x]` done · `[~]` deliberately not done (won't do), with
the decision recorded under Progress.

Slice 1 — transcript persistence
- [x] **T1** — RED: test that a `TaskRecord` round-trips a non-empty turn list and that
      literal pre-change JSON still loads with the new field as zero value.
- [x] **T2** — Add `Turns []port.ContextMessage` to `TaskRecord` (`omitempty`, zero-value
      safe), following the `RemainingIntents` precedent.
- [x] **T3** — Measure `Store.Save` cost with a realistic transcript and record the number
      here. If it is bad, decide now whether the transcript moves to a sibling per-task
      file, before any other slice depends on the layout.

Slice 2 — input delivery
- [x] **T4** — RED: test proving an input larger than the argv ceiling is delivered intact.
- [x] **T5** — Move the prompt from argv to stdin in `claudecode` (verified: `claude -p`
      reads stdin). Keep `--system-prompt-file` as is.
- [~] **T6** — Same for `opencode`. **WON'T DO** — maintainer decision, see the Slice 2
      entry under Progress. `opencode` stays on argv with no stdin support and no preflight
      size guard.

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

Slice 1 closes well over the ~400-line heuristic. A cohesive split existed and was offered
— PR 1a "add the field" (`store.go` + `store_test.go`) and PR 1b "measure the cost and
decide the layout" (`store_bench_test.go` + the T3 verdict), each comfortably under budget.
The maintainer explicitly chose one PR instead, so this slice ships under `size:exception`.

Recorded because the heuristic exists to protect reviewers, and an exception is only
legitimate when it is deliberate and visible:

| Part | Lines | Review load |
|---|---|---|
| `core/supervisor/store.go` | 11 | production, one additive field |
| `core/supervisor/store_test.go` | 203 | tests |
| `core/supervisor/store_bench_test.go` | 153 | benchmark |
| `odd/tasks/ceo-orchestration-loop.md` | see below | progress log, read as context |

367 lines are Go, and only 11 of those are production code: one additive, `omitempty`,
zero-value-safe struct field. The rest of the diff is this progress log.

This note deliberately carries **no frozen total**, and that is the fix for native-review
finding `R3-size-accounting-stale`. The first version stated 539 changed lines with 172 in
this document — numbers that were already wrong when committed, because writing the note
grew the very document it was counting. Any total stated here is stale the moment the next
progress entry lands. The Go counts above are stable and are the actual review load; the
document's own line count is self-referential and is left to `git diff --stat` to answer at
read time.

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

### Slice 2 — complete (T4, T5; T6 won't do)

Branch: `feat/ceo-orchestration-loop-pr2-input-delivery`. Commits:

- `dd1ae17` `fix(claudecode): deliver task input on stdin instead of argv` (T4 + T5)
- `1fa1b0d` `docs(odd): record Slice 2 outcome and the opencode argv won't-do` (this entry,
  plus the `AGENTS.md` provider-compatibility note)
- `79f16ee` `docs(odd): record the Slice 2 docs commit SHA as evidence`
- `bf53ff5` `fix(claudecode): make the input-delivery tests and notes say what the code does`
  (post-review follow-up; closes four advisory findings — see the native review entry below)
- `a7701b3` `docs(odd): record the Slice 2 native review outcome and its follow-ups`
  (the `### Slice 2 — native review` entry below)

**T4 — RED observed.** New file `adapters/claudecode/input_delivery_test.go`, two tests,
added with no production change. `go test ./adapters/claudecode/ -run 'TestRunTask_DeliversInputLargerThanArgvCeiling|TestRunTask_OversizedInputIsNotOnArgv' -v`,
verbatim:

```
=== RUN   TestRunTask_DeliversInputLargerThanArgvCeiling
    input_delivery_test.go:63: RunTask with a 262168-byte input (argv ceiling is 131072): claudecode: start: fork/exec /tmp/TestRunTask_DeliversInputLargerThanArgvCeiling807467973/001/fakeclaude: argument list too long
--- FAIL: TestRunTask_DeliversInputLargerThanArgvCeiling (0.42s)
=== RUN   TestRunTask_OversizedInputIsNotOnArgv
    input_delivery_test.go:91: prompt found at argv[9]="the CEO asks the engineer for a draft"; the prompt must travel on stdin, because a single argv string is capped at 131072 bytes on Linux; argv=[/tmp/TestRunTask_OversizedInputIsNotOnArgv3108784410/001/fakeclaude -p --no-session-persistence --setting-sources  --disable-slash-commands --output-format stream-json --verbose the CEO asks the engineer for a draft]
--- FAIL: TestRunTask_OversizedInputIsNotOnArgv (0.42s)
FAIL
FAIL	github.com/salgozino/ai-solo-startup-framework/adapters/claudecode	0.853s
```

This is B5 reproduced exactly: `E2BIG` at `execve`, surfaced by Go as *"argument list too
long"*, before the CLI runs at all. The fixture is 2x `MAX_ARG_STRLEN` rather than a few
bytes over, so the first test stays meaningful on a kernel with a larger `PAGE_SIZE`; the
second test is structural and holds on any page size.

**T5.** `RunTask` no longer appends `input` to argv; it sets `cmd.Stdin =
strings.NewReader(input)` (`adapters/claudecode/adapter.go`). Nothing else about the
invocation changed: `--system-prompt-file` keeps its own flag and path, and every isolation
flag (`--no-session-persistence`, `--setting-sources ""`, `--disable-slash-commands`,
`--strict-mcp-config`) is untouched. `os/exec` copies the reader into the child's stdin pipe
from a goroutine that `cmd.Wait()` joins, so an input past the 64 KiB pipe buffer cannot
deadlock against the adapter's own stdout read.

Threat-matrix case (a) is preserved and slightly strengthened: the remaining argv is still
an exec slice, never a shell string, and the input is now opaque bytes on a pipe rather than
an element of a command line.

**The test double had to learn stdin, and that is exactly where a lying test was possible.**
`fakeclaude` now mirrors the real CLI's dual prompt source: a positional argument wins when
present, stdin is read only when argv carries no positional. Keeping argv authoritative is
load-bearing — it is what stops the fake from making the suite green over a broken adapter.

Proven by mutation rather than argued. With the fake's stdin support in place, `RunTask` was
temporarily reverted to `args = append(args, input)`:

```
=== RUN   TestRunTask_DeliversInputLargerThanArgvCeiling
    input_delivery_test.go:63: RunTask with a 262168-byte input (argv ceiling is 131072): claudecode: start: fork/exec /tmp/TestRunTask_DeliversInputLargerThanArgvCeiling942504138/001/fakeclaude: argument list too long
--- FAIL: TestRunTask_DeliversInputLargerThanArgvCeiling (0.40s)
=== RUN   TestRunTask_OversizedInputIsNotOnArgv
    input_delivery_test.go:91: prompt found at argv[9]="the CEO asks the engineer for a draft"; the prompt must travel on stdin, because a single argv string is capped at 131072 bytes on Linux; argv=[...]
--- FAIL: TestRunTask_OversizedInputIsNotOnArgv (0.41s)
```

The mutation was reverted and `git diff adapters/claudecode/adapter.go` against the commit
is clean of it. The failure survives the fake's new capability because it happens at
`execve`, before the fake has any say.

**One test-asserted decision deliberately overturned.**
`TestClaudeAdapter_AllowedTools_NeverSwallowsThePrompt` asserted
`argv[len(argv)-1] == prompt`. That is false by design now. The assertion was removed and
the replacement is named in the test's own comment:
`TestRunTask_OversizedInputIsNotOnArgv` asserts the opposite and stronger property (the
prompt appears nowhere in argv), and `TestRunTask_DeliversInputLargerThanArgvCeiling`
proves an over-ceiling input still arrives intact. The rest of that test — the structural
guard that the variadic `--allowedTools` list is terminated by another flag — is kept,
because a future trailing positional would reintroduce the original hazard.

**T6 — WON'T DO. Maintainer decision: `opencode` stays on argv, without stdin support.**

Consequence, stated plainly rather than softened: on the `opencode` adapter, a task whose
input grows past the Linux per-argument ceiling (`MAX_ARG_STRLEN`, 131072 bytes) fails at
`execve` with `E2BIG`. The invocation dies before the CLI starts; there is no partial
result and no graceful degradation. `opencode` hits this **sooner than claude did**, because
the system prompt is concatenated into the very same argv string
(`adapters/opencode/adapter.go:230-234`: `effectiveInput = "[SYSTEM]\n" + systemPromptContent
+ "\n\n" + input`), so the agent's own persona eats part of the budget before the task input
does.

Deliberately **not** implemented as part of this decision: no preflight length check, no
size guard, no truncation, no stdin fallback. The decision is "leave it unsupported", not
"handle it gracefully" — a guard would convert a loud `execve` failure into a quieter
framework error without making any task succeed, and would cost code in an adapter the
maintainer chose not to invest in.

Practical consequence for later slices: the CEO orchestration loop's re-injected transcript
is the exact workload that crosses this ceiling, so an agent expected to run multi-round
conversations must be configured on the `claude` adapter. Recorded in `AGENTS.md` under
`## Provider CLI compatibility` so it is visible at configuration time, not at failure time.
`adapters/opencode/` was not touched by this slice.

**Verification, observed:**

- `gofmt -l .` — no output (clean)
- `go build ./cmd/company` — OK
- `go vet ./...` — OK
- `go test ./...` — all 12 packages `ok`, 1 `[no test files]`
- `go test -race ./adapters/...` — `claudecode` ok 26.119s, `opencode` ok 13.587s

### Slice 2 — native review

Lineage `review-d4381466f584182d`, four lenses (`review-risk`, `review-resilience`,
`review-readability`, `review-reliability`), risk `high`, 6 files / 333 lines. Result:
**approved**, authority acknowledged and burned. **Zero blockers, zero corrections opened.**
All 18 findings are advisory and non-blocking.

`review-risk` returned **no findings at all**, and explicitly recorded that the change
*improves* data-exposure posture: the task input no longer appears in `/proc/<pid>/cmdline`
or in `ps` output, because it is no longer an element of the child's command line.

Follow-up commit: `bf53ff5` `fix(claudecode): make the input-delivery tests and notes say
what the code does`.

**Closed — three findings where a comment asserted something the code did not do.**

- **R2-agents-claude-note-garbled** (WARNING, `AGENTS.md:59-60`) — **closed**. The sentence
  *"This is what keeps a growing input below no ceiling at all"* was a garbled double
  negative, and it is the one sentence a configurator reads to learn why stdin was chosen.
  Rewritten to say plainly that stdin imposes no per-argument ceiling at all, while argv caps
  a single argument at 131072 bytes, so a growing input stays deliverable on stdin and not on
  argv.
- **R2-pagesize-claim-not-supported-by-fixture** (WARNING) + **R3-ceiling-constant-platform-bound**
  (WARNING), `adapters/claudecode/input_delivery_test.go` — **closed together**, because they
  are two faces of one hardcoded constant. The comment claimed the 2x sizing kept the test
  meaningful on a kernel with a larger `PAGE_SIZE`. It did not: the fixture *and* its own
  self-check guard both derived from the same hardcoded `32 * 4096`, so on a 16 KiB-page
  kernel the fixture would sit *under* the real ceiling and the test would degrade into a
  silent no-op instead of failing. The same constant was also plainly Linux-only — macOS and
  the BSDs budget argv as a total with no per-argument cap, so
  `TestRunTask_DeliversInputLargerThanArgvCeiling` would pass there even with the prompt back
  on argv.

  Both halves of the honest fix were taken rather than one: `maxArgStrLen` now derives from
  the host (`32 * os.Getpagesize()`), and the delivery test carries a
  `runtime.GOOS != "linux"` skip that names the premise it needs. The comments now describe
  exactly that and nothing more. `TestRunTask_OversizedInputIsNotOnArgv` is structural — it
  inspects the argv the child received and depends on no kernel ceiling — and keeps running
  on every GOOS, as required.

  One consequence surfaced while fixing it and was fixed too: the delivery test pinned
  `OutputLimit: 1 << 20` while the fixture is `2 * maxArgStrLen`. On a 16 KiB-page host the
  fixture becomes 1 MiB plus markers, so the echoed output would be *truncated* and
  truncation — not delivery — would decide the byte-for-byte comparison. `OutputLimit` now
  derives from the fixture length.
- **R2-argv-slice-test-name-now-misleading** (WARNING, `adapters/claudecode/adapter_test.go:102-105`)
  — **closed**. `TestArgvSlice_ShellMetacharactersAreLiteral` was named after a mechanism it
  can no longer exercise, since the metacharacter string travels on stdin. Renamed to
  `TestInput_ShellMetacharactersAreNeverInterpreted`, which is what it actually proves: no
  shell ever sees the input bytes. Its comment and the file's threat-matrix header were
  corrected to match, and the old name is recorded in the comment so the rename is traceable.
- **R2-undefined-checkbox-marker** (SUGGESTION) — **closed**. The `[~]` marker on T6 was
  undefined beside `[ ]` and `[x]`. A marker legend is now stated under `## Tasks`; `[~]`
  means "deliberately not done, decision recorded under Progress", which is exactly T6.

**Evidence for the scoping change.** The rename and the comment corrections change no
assertion, so no RED is claimed for them. The `runtime.GOOS` skip *does* change where a test
runs, so it was proven reachable rather than asserted — the guard was temporarily inverted to
`runtime.GOOS != "plan9"`:

```
=== RUN   TestRunTask_DeliversInputLargerThanArgvCeiling
    input_delivery_test.go:67: MAX_ARG_STRLEN is a Linux per-argument ceiling; on linux argv is budgeted as a total, so an oversized single argument does not fail at execve and this test cannot detect argv delivery
--- SKIP: TestRunTask_DeliversInputLargerThanArgvCeiling (0.00s)
PASS
ok  	github.com/salgozino/ai-solo-startup-framework/adapters/claudecode	0.005s
```

The mutation was reverted; the guard reads `runtime.GOOS != "linux"` again, and the committed
diff contains no `plan9`.

The derived ceiling was measured on the host rather than assumed:
`os.Getpagesize()=4096  32*PAGE_SIZE=131072`, with `getconf PAGESIZE` agreeing at `4096`.
That 131072 is the same boundary the T4 RED above hit with `E2BIG`, so the derived value is
the real ceiling on this host, not a number that happens to match.

Renamed test green and both delivery tests still green:

```
=== RUN   TestInput_ShellMetacharactersAreNeverInterpreted
--- PASS: TestInput_ShellMetacharactersAreNeverInterpreted (0.42s)
=== RUN   TestRunTask_DeliversInputLargerThanArgvCeiling
--- PASS: TestRunTask_DeliversInputLargerThanArgvCeiling (0.43s)
=== RUN   TestRunTask_OversizedInputIsNotOnArgv
--- PASS: TestRunTask_OversizedInputIsNotOnArgv (0.43s)
PASS
ok  	github.com/salgozino/ai-solo-startup-framework/adapters/claudecode	1.288s
```

The old name is gone rather than shadowed —
`go test ./adapters/claudecode/ -run '^TestArgvSlice_ShellMetacharactersAreLiteral$' -v`:

```
testing: warning: no tests to run
PASS
ok  	github.com/salgozino/ai-solo-startup-framework/adapters/claudecode	0.004s [no tests to run]
```

**The premise itself — was unproven against the real CLI, now closed by observation.** This
is the group that mattered most, because the whole slice rests on it.

- **R3-real-cli-stdin-contract-unproven** (WARNING) — **closed by observation**. The premise
  that `claude -p` reads the prompt from stdin when no positional is given was proven only
  against `fakeclaude`, whose stdin support was authored in this same candidate — a
  self-consistent suite, not evidence about the real CLI. It has now been run against the
  installed `claude` 2.1.280 and holds. Full results, including a defect the spike found in
  `fakeclaude`'s own comments, are recorded under `### Slice 2 — real-CLI spike` below.

  Closed honestly, with the limit stated: a one-off spike proves the premise on 2.1.280
  **today**, and cannot notice a future CLI build changing it. The mitigation is a recorded,
  copy-paste **reproducible recipe** (in that section) to re-run at the next `claude`
  upgrade. A build-tagged, opt-in integration test was considered and **deliberately not
  added** — maintainer decision: an opt-in test nobody runs earns nothing over a recorded
  recipe, while costing a permanent maintenance surface.
- **R4-stdin-delivery-has-no-failure-signal** (WARNING) — **open**, and now better
  characterised rather than closed. If a future CLI build stops reading stdin under `-p`,
  `RunTask` either returns a *successful* `ProviderResult` built from an empty prompt, or
  blocks until the ctx deadline. There is no runtime confirmation that the child consumed the
  bytes, so the failure mode is a confident wrong answer or a timeout, not a diagnosable
  error. The spike above confirms the premise **is** honoured on 2.1.280, so this is a risk of
  future drift, not present breakage — which lowers its urgency without changing its shape.

**Open follow-up — the failure moved, it did not disappear.**

- **R4-input-size-now-unbounded** (WARNING) — **open**. The old 131072-byte argv ceiling
  failed locally in milliseconds at zero token cost, before the CLI ran. The same oversized
  transcript now ships to the provider and fails at the model's context limit, after a full
  upload and real token spend. Nothing caps the input any more. This is a deliberate
  consequence of the slice, not a regression to fix blindly, and it belongs with the Slice 3
  transcript work — T8 is what actually makes the input grow.

**Open but deferred — the GOOS skip is unreachable in CI, so it costs nothing today.**

- **R3-goos-skip-drops-large-input-roundtrip** / **R4-delivery-integrity-check-skipped-off-linux**
  (WARNING) — **open, deferred with the reason attached.** The `runtime.GOOS != "linux"` guard
  added to `TestRunTask_DeliversInputLargerThanArgvCeiling` in `bf53ff5` is correct about the
  kernel premise (only Linux has a *per-argument* cap), but it means the only byte-for-byte
  delivery-integrity assertion in the suite does not run off Linux. On macOS or a BSD the
  round-trip is never checked and `TestRunTask_OversizedInputIsNotOnArgv` — structural, and
  running on every GOOS — is all that remains.

  Deferred on a checked CI fact, not on a guess: `.github/workflows/ci.yml` declares a single
  `build-and-test` job on `runs-on: ubuntu-latest`, with **no OS matrix**. The skip is
  therefore unreachable in CI entirely, and the gap affects only a developer working on
  macOS/BSD. Trigger that would make closing it worthwhile: **adding a non-Linux job to the CI
  matrix.** At that point the skip stops being theoretical and the integrity check should be
  reworked into a platform-independent form (assert delivery via the round-trip without
  depending on a per-argument ceiling) rather than skipped.

**Open follow-up — the test double can lie, and one path makes it lie silently.**

- **R2-fake-positional-catchall-controls-stdin** / **R3-fake-prompt-source-fragile** (WARNING)
  — **open**. `positionalSeen` is set by the argv loop's `default` catch-all, so *any* token
  the fake does not recognise — including the value of a flag added later — marks a positional
  and silently suppresses the stdin read. The result is a fake that reports an empty prompt
  while the adapter is correct, and the failure points at the wrong side. An explicit
  unknown-argument fatal in the fake would keep it diagnosable.
- **R2-fake-stdin-read-error-dropped** / **R3-fake-stdin-read-errors-swallowed** /
  **R4-fake-swallows-stdin-read-error** (WARNING) — **open**. The fake discards both the error
  and any partial bytes returned by `io.ReadAll`, which collapses "truncated delivery" into
  "missing delivery". That is precisely the distinction the `HEAD-MARKER`/`TAIL-MARKER`
  bookends exist to expose.
- **R4-oversized-input-test-has-no-deadline** (SUGGESTION) — **open**. The oversized test
  drives the blocking stdin path with `context.Background()`, so the very regression it guards
  — a child that never drains stdin — would hang the suite rather than fail it.

**Open follow-up — cosmetic, idiomatic, or pre-existing.**

- **R3-off-argv-guard-name-and-shape** (SUGGESTION) — `TestRunTask_OversizedInputIsNotOnArgv`
  uses no oversized input; the name overstates what the body does.
- **R2-iso-prefix-presence-check-idiom** (SUGGESTION) — the `strings.TrimPrefix` +
  `delivered == result.Output` comparison is an indirect way to assert prefix presence.
- **R2-arg-ceiling-constant-duplicated** (SUGGESTION) — the ceiling is expressed in the test
  and again in prose in `AGENTS.md`; the two can drift.
- **R4-opencode-e2big-doc-only-mitigation** (SUGGESTION) — pre-existing and already recorded
  as the T6 won't-do above: `opencode`'s `E2BIG` is mitigated by documentation only.

**Disclosed, not fixed — outside this slice's authorized edit roots.**
`openspec/changes/agent-startup-framework-foundation/verify-report.md:139` still names
`TestArgvSlice_ShellMetacharactersAreLiteral` in its compliance table. That reference is now
stale for the `claudecode` package. It was left untouched deliberately: it is an archived
verify report for a different change, and `openspec/` was not in scope here. The identically
named test in `adapters/opencode/adapter_test.go:153` is unaffected and still exists.

**Verification after the follow-up commit, observed:**

- `gofmt -l .` — no output (clean)
- `go build ./cmd/company` — OK
- `go vet ./...` — OK
- `go test ./...` — 12 packages `ok`, 1 `[no test files]`; `claudecode` ok 23.096s
- `go test -race ./adapters/claudecode/` — `ok ... 24.845s`

### Slice 2 — real-CLI spike

**Purpose.** Close `R3-real-cli-stdin-contract-unproven`, the native-review follow-up that
said the whole slice rests on an unverified premise: that `claude -p` reads the prompt from
stdin when no positional is given, proven only against a fake this same candidate authored.
The spike ran that premise against the **installed `claude` 2.1.280**. It is now **closed by
observation**. Everything below was observed, not inferred.

**1. The premise holds.** `claude -p` with the adapter's exact flag set, **no positional
argument**, prompt on stdin → exit 0, `result.subtype=success`, `result='STDIN_OK'`. The
adapter's delivery channel is the one the real CLI actually reads.

**2. `E2BIG` reproduced against the real CLI, not just the fake.** A **145204-byte** payload
passed as a positional argv argument → **exit 127, `argument list too long`**. This is the
same `MAX_ARG_STRLEN` wall the T4 RED hit through `fakeclaude`, now confirmed on the real
binary. The argv ceiling was never an artifact of the test double.

**3. The same bytes on stdin arrive intact, at both ends.** The identical **145204 bytes**
piped to stdin → **exit 0**, success, and the model echoed **both** bookend markers verbatim:
`HEAD_MARKER_7Q4Z` and `TAIL_MARKER_9X2W`. `cache_creation_input_tokens=58076` confirms the
full payload was genuinely uploaded, not silently truncated on the way in. Both ends present
is the point: a head-only echo would have proven truncation, which is exactly the failure the
bookends exist to expose.

**4. The CLI documents the contract itself.** Its own error text names both sources verbatim:

```
Error: Input must be provided either through stdin or as a prompt argument when using --print
```

stdin is a first-class, documented input source under `--print`, not an accident of
implementation.

**5. `ProbeModel`'s premise also holds on 2.1.280.** `claude -p "" < /dev/null` → exit 1,
"Input must be provided..."; the same with `--model no-such-model-xyz` → exit 1 with a
model-catalog error. The two outcomes are distinguishable **by stderr content**, which is
exactly what `ProbeModel` assumes when it classifies the failure.

**6. The spike found a defect — in `fakeclaude`'s comments, not in the adapter.** The fake's
header claimed it "mirrors the real CLI, which accepts the prompt either way: when a
positional prompt argument is present on argv it wins". **That is false.** Observed on
2.1.280, the real CLI does not pick a winner — it receives **both**:

| argv positional | stdin | real CLI `result` |
|---|---|---|
| `BRAVO. ...reply with the ones you actually received` | `ALPHA` | `ALPHA BRAVO` |
| `FROM_ARGV` | `/dev/null` | `FROM_ARGV` |
| *(none)* | `FROM_STDIN` | `FROM_STDIN` |

What this does **not** affect, stated precisely:

- **Production is unaffected.** `RunTask` passes **no positional at all**, so only the stdin
  path is ever exercised — and on that path the fake and the real CLI agree exactly (row 3).
  `ProbeModel` passes an empty positional and leaves stdin at `/dev/null` (row 2), where they
  also agree. The dual-source merge case is unreachable in this codebase today.
- **The earlier mutation proof still stands.** Reverting `RunTask` to
  `args = append(args, input)` still fails the oversized-input test, because `execve` dies
  with `E2BIG` **before the CLI parses anything** — item 2 above confirms that on the real
  binary. What was wrong was the stated *mechanism*, not the conclusion: the old comment
  justified the guard by "that argv value would be used", which rested on the false precedence
  claim. The guard survives its own justification being wrong.

The fake's **behaviour is deliberately left unchanged** — a single-prompt model is all the
suite needs, and keeping argv authoritative is what stops its stdin support from masking an
adapter that regressed to argv. Only the comments were corrected, to state plainly that this
is a **simplification, not a mirror**.

**Reproducible recipe.** Recorded so re-running at the next `claude` upgrade is copy-paste
rather than re-invention. This recipe *is* the mitigation for the closure caveat: a one-off
spike proves 2.1.280 today and cannot notice a future CLI changing the contract.

Premise check — stdin under `-p` with no positional:

```
printf 'Reply with exactly the word STDIN_OK and nothing else.' | \
  claude -p --no-session-persistence --setting-sources '' \
    --disable-slash-commands --output-format stream-json --verbose
```

Ceiling check — the same oversized payload down each channel:

```
# argv path: expected to die at execve, costs no tokens
BIG=$(cat big_prompt.txt); claude -p ... "$BIG"
# stdin path: expected to succeed and echo both bookend markers
claude -p ... < big_prompt.txt
```

Where `big_prompt.txt` is an instruction line, a `HEAD_MARKER_7Q4Z` line, ~145 KB of filler,
and a final `TAIL_MARKER_9X2W` line; the model is asked to reply with the first and last
markers only. The argv run costs nothing (it never reaches the model); only the stdin run
spends tokens.

**Deliberately not added: a build-tagged integration test.** Maintainer decision. An opt-in
test nobody runs earns nothing over a recorded recipe, while adding a permanent maintenance
surface and a standing temptation to believe the premise is covered by CI when it is not.

**Commits:**

- `da9eb40` `docs(claudecode): correct the fakeclaude prompt-source claim and re-verify CLI
  facts` (the `fakeclaude` comment fix plus the four evidence-bound version bumps)
- `docs(odd): record the Slice 2 real-CLI spike and close the stdin-premise follow-up` —
  this entry, plus the follow-up-list updates above. Recorded by name rather than SHA
  because a commit cannot contain its own hash; same convention as the Slice 2 entry.

**Version-drift bookkeeping, partial on purpose.** Four of the six `2.1.268` references were
re-verified on 2.1.280 and bumped; two were **left at 2.1.268** because bumping them would
fabricate a verification nobody performed:

| Reference | Claim | Action |
|---|---|---|
| `adapters/claudecode/adapter.go` | stdin under `-p` | → **2.1.280** (observed run, items 1–4) |
| `adapters/claudecode/adapter.go` | `--safe-mode` bundle | → **2.1.280** (`claude --help`) |
| `AGENTS.md` | `--safe-mode` bundle | → **2.1.280** (`claude --help`) |
| `adapters/claudecode/adapter_test.go` | `--safe-mode` bundle | → **2.1.280** (`claude --help`) |
| `adapters/claudecode/adapter.go` | MCP tool calls denied client-side | **stays 2.1.268** |
| `adapters/claudecode/adapter_test.go` | MCP tool calls denied client-side | **stays 2.1.268** |

The two that stay were reproduced with a **live MCP server driving a real tool call**.
`claude --help` cannot re-verify a runtime permission decision, and that spike was not re-run,
so `2.1.268` remains the truthful record of when the behaviour was observed. A clarifying
sentence saying exactly that was added at both sites; the version numbers were not touched.

One `--help` fact was re-confirmed in passing and matters to an existing guard:
`--allowedTools, --allowed-tools <tools...>` is **still variadic** on 2.1.280, which is the
property `TestClaudeAdapter_AllowedTools_NeverSwallowsThePrompt` depends on.

This spike changed comments and documentation only. No behaviour changed, no assertion
changed, so **no RED is claimed** for it.

## Next step

**Slice 2 is closed.** T4 and T5 are done, T6 is a recorded won't-do, and the native review
on lineage `review-d4381466f584182d` is approved with its authority acknowledged and burned,
zero blockers and zero corrections. Its three lying-comment findings are fixed in `bf53ff5`;
the rest are advisory follow-ups listed above.

**Next: Slice 3, T7 (RED)** — a CEO turn that delegates gets a second `RunTask` call whose
input contains the peer's output. Input delivery is no longer a ceiling for that transcript
on the `claude` adapter; agents that need the loop must not be configured on `opencode` (see
the T6 won't-do above).

Carried into Slice 3 deliberately, because T8 is what makes these bite:

- **R4-input-size-now-unbounded** (Slice 2) — the argv ceiling is gone, so the growing
  transcript now fails at the model's context limit after real token spend instead of locally
  at `execve`. Size it where the transcript is assembled.
- **R3-bench-replace-path-only** and **R3-bench-quadratic-seed** (Slice 1) — both live in the
  benchmark file and pair with the Slice 3 benchmark revisit the T3 verdict already schedules.

Slice 1's review findings are settled. Two native reviews ran on Slice 1:

- `review-787c47fe13b36fa2` — approved. R3-toolchain-floor closed by evidence;
  R3-single-record-store and R3-roundtrip-field-subset folded into this slice and closed
  above with mutation proof.
- `review-00c2c7d549dd7761` — approved on the amended candidate, no blocker. It confirmed
  the two folded-in tests and raised three advisory findings:
  - **R3-size-accounting-stale** (SUGGESTION) — **closed**: the size-exception note stated
    539 changed lines with 172 in this document, while the real diff was 561 with 194.
    Writing the audit note grew the document it audited. Fixed by removing the frozen
    total; see the size-exception section above.
  - **R3-bench-replace-path-only** (SUGGESTION) — **open**. The timed benchmark record
    reuses a `TaskID` the seed already wrote, so every measured `Save` exercises only the
    in-place replace path. The append path — a new task growing an already large array —
    is unmeasured, and the T3 verdict extrapolates from replace-only. Worth closing when
    the Slice 3 benchmark revisit happens, since it may shift the cost curve the verdict
    rests on.
  - **R3-bench-quadratic-seed** (WARNING) — **open**. The seed loop calls `Save` per task,
    so setup is quadratic in file bytes; the 500-task sweep writes gigabytes before the
    timer starts. Costs `go test -bench .` time only, not correctness.

Both open findings live in the benchmark file and pair naturally with the Slice 3 revisit
the T3 verdict already schedules. Neither affects the persisted schema.

Document amendments recorded here — the Slice 1 audit-note correction and this Slice 2
review record — are passive documentation. They do not reopen review on any
already-acknowledged candidate.
