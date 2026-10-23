# Software factory example + the framework fixes it surfaced

A complete software-factory company (examples/software-factory) validated end to end, plus three
framework fixes found while running it. Branch: `feat/software-factory` in worktree
`.worktrees/software-factory` (from master). Commits go on this branch only, never on master.

## Specs

- S1 — User request, verbatim: "Creemos un worktree donde arreglemos 1 y 2.
  Para 2 el framework deberia de rechazar el finish_task si delego!.
  3. el QA deberia tener una tool para validar con navegador, playwrright o chrome dev tool, podria estar definido en ese agente.
  y agrega los agentes creados en el nuevo worktree tambien (el commit en el neuvo worktree nunca en master)"
- S2 — Fix 1 (phantom escalation). Today, when a task that delegated resumes after its peer's
  result and runs its next provider round, its record stays `TASK_STATE_INPUT_REQUIRED` with
  `awaiting_peer` cleared and no `pending_intent_kind`; `ui/app.js:70` then renders
  Approve/Reject for a task nobody needs to approve. Required: while that next round runs, the
  task is reported as working (`TASK_STATE_WORKING`) through the task list API and state
  broadcasts, and the UI shows no verdict controls for it. Real escalations (risky intent,
  `pending_intent_kind` set) and parked tasks (`awaiting_peer`, chained peer approval) behave
  exactly as before.
- S3 — Fix 2: "el framework deberia de rechazar el finish_task si delego!". Within one provider
  turn, `finish_task` and a delegation do not combine:
  - `finish_task` called after the turn already recorded a `delegate_task` intent returns an MCP
    error result (`isError: true`) with exactly the text
    `finish_task rejected: this turn delegated work. The peer's result arrives in your next turn; call finish_task then.`
    and records no finish intent.
  - Backstop in the supervisor: when a turn's intents contain both a delegation and
    `finish_task` (in any order), the finish is discarded, the delegation runs, and the loop
    continues to the next round with the peer result as usual (the task does NOT end after that
    turn). Logged as `finish.rejected`.
  - A turn that only calls `finish_task` ends the task exactly as today. The round limit is
    unchanged.
  - The `finish_task` tool description, `agents/ceo.md` and docs describing finish_task say a
    finish in a delegating turn is rejected.
- S4 — Fix 3 / user decision "mcp_servers por agente (recomendado)": a new optional per-agent
  `mcp_servers` field in company.yaml declares the agent's own stdio MCP servers, pi provider
  only (like `extensions`). Shape:
  ```yaml
  mcp_servers:
    chrome-devtools:
      command: npx
      args: ["chrome-devtools-mcp", "--headless", "--isolated"]
  ```
  - Load-time validation (named errors): rejected for any provider other than `pi`; server name
    must match `^[a-z0-9][a-z0-9_-]*$` and must not be `framework`; `command` required.
  - The pi adapter registers each server for every task invocation and adds
    `mcp__<name>__*` to the `--tools` allowlist, so its tools are callable; other ambient MCP
    servers stay hidden. Agents without `mcp_servers` get byte-identical argv/extension to today.
  - These tools are NOT action intents and bypass the risk policy: README and AGENTS.md document
    the field, the pi-only restriction, and that declaring a server is an explicit grant by the
    company owner.
- S6 — User decision on R1: "Nombres exactos de tools (recomendado)". The pi adapter's
  `--tools` allowlist names MCP tools exactly, never by `mcp__<server>__*` prefix:
  - framework: `mcp__framework__<kind>` for every policy action kind plus the control tools
    (`finish_task`), known statically.
  - each declared `mcp_servers` entry: probed once at materialize (stdio MCP `initialize` +
    `tools/list`, following pagination, bounded timeout), and its tools listed as
    `mcp__<server>__<tool>` using pi's normalization (every char outside `[A-Za-z0-9_]` -> `_`).
  - Materialize fails with a named error when a probe fails/times out, returns zero tools, or two
    tools of one server normalize to the same name (pi would hash-suffix them).
  - No `--exclude-tools`; the `__` declared-name rule from dc9232a is dropped (no longer needed);
    the `-`/`_` collision rule stays. A declared server's tools containing `__` are callable again.
  - An undeclared ambient server (any name, e.g. `github_`, `github__x`) exposes no callable tool.
  - Residual, documented: an ambient `mcp.json` server with exactly the same name as a declared or
    the framework server replaces it in pi (pi: mcp.json wins over registered servers).
- S5 — The example company lives in the worktree under `examples/software-factory/`
  (factory.yaml, agents/*.md for ceo, product-owner, ux-designer, tech-lead, engineer, qa,
  security-reviewer, e2e.sh, e2e_check.py, README.md). The QA agent declares a browser MCP server
  (chrome-devtools-mcp, available offline from the npx cache) and its prompt makes it verify the
  delivered app in a real browser; QA_REPORT records browser evidence. The e2e run passes.

## Tasks

- T1 [S2,S3] supervisor + transport/mcp + agents/ceo.md: phantom escalation fix and finish rejection, TDD — gentle-ai-worker — commit: 976bc81 (done)
- T2 [S4] config + adapters/pi + README/AGENTS.md: per-agent mcp_servers (pi), TDD — gentle-ai-worker — commit: 9967648 (done)
- T4 [S6] adapters/pi + config + docs: exact MCP tool allowlist with startup probe, TDD — gentle-ai-worker — commit: fix(pi) exact allowlist (done)
- T3 [S5] examples/software-factory: add the company, QA browser server + prompt, run e2e — inline — commit: see git log (feat(examples)) (done)

## Log

- L1 (user): see S1.
- L2 (context, earlier run on master): e2e run 2 passed (7 agents, fix cycle, telegram). Findings:
  (1) UI phantom Approve/Reject during the post-delegation round (`ui/app.js:70`, task stays
  INPUT_REQUIRED after `resume.peer.done` -> `runtask.start round N`); (2) product-owner on
  openai/gpt-6-luna called `delegate_task` + `finish_task` in one turn twice, despite the prompt
  forbidding it, so it never reconciled the UX result; (3) QA verified the UI only by reading code.
- L3 (user decision): browser tool via "mcp_servers por agente (recomendado)" — pi only.
- L4 (evidence): pi runs with `--tools read,bash,edit,write,mcp__framework__*`, so pi extension
  tools are hidden; `chrome-devtools-mcp` is in the local npx cache; /usr/bin/google-chrome exists.
- L5 (T1 evidence): root cause S2 = `resumeFromPeer` yielded WORKING but never saved the record.
  RED->GREEN: TestResume_NextRoundReportsWorkingNotInputRequired, TestFinishTask_DiscardedInDelegatingTurn,
  TestFinishTask_RejectedAfterDelegationInSameInvocation, TestFinishTask_DescriptionSaysDelegatingTurnIsRejected.
  Replaced TestFinishTask_EndsLoopAfterDelegatingTurn (asserted the old, now-rejected behavior).
  core/... and transport/... green with -race.
- L6 (T2 evidence): pi API verified at pi 1.2.0 docs/extensions.md:197 (stdio command/args).
  Worker's smoke check showed no browser tools; parent found why: pi sanitizes tool names
  (docs/mcp.md:77, `-` -> `_`), so `mcp__chrome-devtools__*` matched zero tools. Fixed inline
  with RED->GREEN (allowlist maps `-`->`_`; config rejects names colliding after the mapping).
  Live check: with `mcp__chrome_devtools__*` pi lists 32 chrome-devtools tools. go test ./... green.
- L7 (slice boundary): commits 976bc81 + 9967648 = ~690 authored lines, past the ~400 slice;
  delivery as stacked PRs is the user's decision (T1 and T2 are independent commits).
- L8 (user decision): "1 solo pr esta bien, no hay problema, pero cada commit es un working unit"
  -> one PR; each commit stays a self-contained work unit (T1, T2, T3 = one commit each).
- L9 (e2e run 3, failed): PO waited for UX before finishing (S3 confirmed live, 2 PO rounds).
  Chrome closed on start (`Target closed`): reproduced in isolation — TMPDIR of 143 chars
  breaks Chrome's singleton socket (Unix 108-byte limit); e2e.sh now uses a short mktemp TMPDIR.
  Security was never re-reviewed after the fix cycle; CEO prompt now re-verifies with whoever
  failed, both in one turn when both failed. Checker now requires chrome-devtools-mcp log
  evidence (127.0.0.1 navigation, browser resolved, an interaction).
- L10 (e2e run 4, PASS, 9m36s): ceo -> product-owner(-> ux-designer x2) -> tech-lead -> engineer
  -> qa -> security-reviewer -> telegram_send. QA PASS with browser evidence (new_page,
  fill_form, take_snapshot x2, list_console_messages against http://127.0.0.1:8765/); security
  APPROVED; node --test 8/8; telegram delivered. No fix cycle was needed, so the two-delegations-
  in-one-turn re-verification path was NOT exercised live.
- L11 (native review review-424ea93c284fe577, lenses risk/readability/reliability, tier high):
  BLOCKER R1-ambient-prefix (adapters/pi/adapter.go:371): `mcp__<name>__*` also matches tools of an
  undeclared ambient server named `<name>__x`, exposing them outside the risk policy. Verified live
  (31 leaked tools). Bounded correction dc9232a (--exclude-tools mcp__<name>__*__* + reject `__`
  in declared names) cut that to 0, but the targeted validator REJECTED it -> state `escalated`:
  an ambient server named `<name>_` (trailing `_`) still leaks (`mcp__github___delete_repository`),
  and declared tools containing `__` are now hidden (regression). Prefix matching is ambiguous by
  construction: needs a design decision (exact tool ownership). Same latent issue exists on master
  for `mcp__framework__*` (base-only).
- L13 (user decision): "Nombres exactos de tools (recomendado)" -> S6, T4.
- L14 (T4 evidence): ProbeMCP via go-sdk CommandTransport + ClientSession.Tools (paginated);
  exact names for framework (policy kinds + finish_task) and declared servers; named errors for
  probe failure/timeout/zero tools/normalization collision; RunTask fails closed if unprobed.
  RED->GREEN per rule (adapter, config `__` rule removed, wire probe aborts materialize). Live:
  chrome-devtools probe 0.95s, 32 tools, --tools 1204 chars.
- L15 (race): full -race run flagged a data race in T1's TestResume_NextRoundReportsWorkingNotInputRequired:
  test-only (unsynchronized taskID/recDuring between the test goroutine and the state-change
  callback/provider hook, working_state_test.go:69 vs :81). Guarded with the test's mutex; 600
  runs (-race -cpu 1,2,4) clean afterwards (one earlier post-fix batch reported 2 hits whose log
  was not kept and did not reproduce). Full suite -race green.
- L12 (CI): master CI flakes are pre-existing; fixed separately in PR #109 (branch fix/flaky-ci).
