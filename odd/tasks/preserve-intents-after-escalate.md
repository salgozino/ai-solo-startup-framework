# Preserve action intents across an escalation

## Objective

Stop the supervisor from silently discarding the action intents that follow the first
escalated intent in a single provider turn, and cover the multi-intent turn with tests.

## Problem

`executeWithPolicy` (core/supervisor/supervisor.go) loops over `result.ActionIntents` and
`return`s inside `case policy.Escalate`. Every intent after the first escalation is never
classified and never executed. `executeResume` then runs only `rec.PendingIntentKind`, so
those intents are lost permanently — with no error, no log, and no task-state signal.

The `return` predates this work and was dormant: with `telegram_send` as the only action
kind, the remaining slice was always empty. Adding `delegate_task` as a second action kind
made the drop reachable. A CEO turn that emits `telegram_send` before `delegate_task` parks
for approval and the delegation never happens.

Secondary hole on the same path: `TaskRecord` persists only `PendingIntentKind` and
`PendingIntentBody` — never the target role — so a resumed `delegate_task` cannot execute
and fails explicitly.

## Why

The CEO-delegates-then-notifies flow was validated manually and works only because the
intents happened to arrive in a favourable order. Nothing enforces that order: not the
supervisor, not `agents/ceo.md`, not a test. No test in the repository constructs a
`port.ProviderResult` with more than one intent.

## Scope

Authorized edit roots:

- `core/supervisor/` — supervisor loop, task record schema, tests
- `odd/tasks/preserve-intents-after-escalate.md` — this document

Out of scope: `agents/*.md` prompts, policy engine semantics, gateway, UI, the A2A
transport, and any other open PR in the chain.

## Constraints

- TDD is mandatory (source: project `AGENTS.md`). Runner: `go test ./...`.
- RED must be observed before implementation; no invented evidence.
- `TaskRecord` is persisted as JSON on disk. Any new field must be `omitempty` and
  zero-value safe so records written before this change still load.
- Do not reorder the agent's intents to dodge the problem.
- Behaviour validated manually (delegate first, notify second) must keep working unchanged.

## Approach

Persist the remaining intents instead of dropping them. On resume, execute the approved
intent and then continue the loop over the rest, which naturally re-parks if another risky
intent appears. This also closes the missing-target hole for free.

## Tasks

- [x] **T1** — RED: add a supervisor test proving a turn with `delegate_task` +
      `telegram_send` (in that order) executes the delegation and parks on the telegram
      intent. Must pass before and after; it pins the validated path A.
- [x] **T2** — RED: add a supervisor test proving a turn with `telegram_send` +
      `delegate_task` (risky first) does not discard the delegation. Must fail on current
      `main` of this branch.
- [x] **T3** — Persist the remaining intents on `TaskRecord` (`omitempty`, zero-value safe),
      carrying kind, body and target.
- [x] **T4** — Make the resume path execute the approved intent and then continue over the
      remaining intents, re-parking when another risky intent appears.
- [x] **T5** — Prove the resumed `delegate_task` target survives the round trip (closes the
      `executeResume` empty-target hole).
- [x] **T6** — GREEN: full `go test ./...` clean; work-unit commit on this branch.

## Acceptance criteria

- No intent emitted in a provider turn is dropped without either executing, being rejected,
  or producing an explicit terminal failure.
- A multi-intent turn is covered by tests in both orders.
- Records written before this change still load (backward compatibility proven by a test or
  an explicit zero-value assertion).
- `go build ./cmd/company` and `go test ./...` both clean.

## Checks

- `go build ./cmd/company`
- `go test ./...`
- `go vet ./...`

## Delivery

Branch: `feat/agent-delegation-over-a2a-pr7e-preserve-intents-after-escalate`
Base: `feat/agent-delegation-over-a2a-pr7d-persona-contradiction-fix` (PR #88).
One PR stacked on the existing chain; the tracker is PR #75. Forecast is under the ~400
authored-line heuristic, so a single slice.

## Route

Delegated direct. Writer trigger fired: the change touches `supervisor.go`, `store.go` and
at least one test file — 2+ non-trivial files. One bounded writer, no parallel writers.

## TDD

Mode: on. Source: project `AGENTS.md` ("TDD is mandatory for this project").
Runner: `go test ./...`.

## Progress

- Feature document created; branch cut from PR #88's head at `051457a`.
- Baseline before any change: `go build ./cmd/company` and `go test ./...` both clean;
  all 12 chain PRs `MERGEABLE` with CI `SUCCESS`.
- T1/T2/T5 written first in `core/supervisor/multi_intent_test.go`. RED observed in two
  stages. Stage 1 was the compile failure (`rec.RemainingIntents undefined`,
  `PendingIntentTarget undefined`). After adding the schema fields only, the behavioural
  RED was:
  - `TestMultiIntent_DelegateThenEscalate` — **PASS** on unchanged behaviour, as required:
    it pins the manually validated order.
  - `TestMultiIntent_EscalateThenDelegateIsNotDropped` — FAIL, `the intent that followed
    the escalation must not be dropped; Delegate calls = 0, want 1`. That is the drop.
  - `TestMultiIntent_ResumedDelegationKeepsItsTarget` — FAIL, `PendingIntentTarget = "",
    want engineer`.
  - `TestMultiIntent_SecondRiskyIntentReParks` — FAIL, `a second risky intent must re-park
    the task, got TASK_STATE_COMPLETED`.
- T3: `TaskRecord` gained `PendingIntentTarget string` and `RemainingIntents
  []PendingIntent`, both `omitempty`. Backward compatibility is proved by
  `TestStore_LoadsRecordWrittenBeforeRemainingIntents`, which seeds the literal pre-change
  JSON bytes (not a re-marshalled struct), loads it, asserts the new fields read back as
  zero values, and asserts a re-save keeps both keys out of the file.
- T4: the classification loop moved out of `executeWithPolicy` into `runIntents`, shared by
  the first-run and resume paths, so a resumed intent is classified under the same policy
  rules as a first-run one. `Escalate` now persists `intents[i+1:]` on the record instead of
  returning; `executeResume` clears the spent intent and feeds the remainder back in.
- T5: the escalate branch persists the target and `executeResume` passes
  `rec.PendingIntentTarget` instead of `""`.
- `TestResume_DelegateTaskWithoutPersistedTargetFailsExplicitly` was removed from
  `delegation_test.go`: it asserted the very hole T5 closes (an approved delegation MUST
  fail). A comment in its place names its replacement. This was the only pre-existing test
  that contradicted the new behaviour; every other test in the suite passed untouched.
- Verification on the finished change: `go build ./cmd/company` clean, `go vet ./...` clean,
  `go test ./...` all packages `ok`.

## Next step

Open the PR stacked on PR #88. Nothing is left to implement for this task.
