# UI task tree: delegated tasks nested under their parent

Show each delegated peer task as a child of the task that delegated it in the monitoring UI.
Branch: `feat/ui-task-tree` (from master, independent of the pi PR #103).

## Specs

- S1 — User request, verbatim: "algo que no me gusta, es qeu el task del sw-engineer, parece como algo independiente, estaria bueno qeu este como en un segundo nivel, debajo de la task del ceo.
  - task ceo
     -- task engien
     -- task otro agente que todavia no existe
     -- task del tercer agente que todavia no existe"
- S2 — Link stored in the parent ("En el padre (Recomendado)"): the delegating task's `TaskRecord`
  persists the ordered list of its delegations (`role`, peer `task_id`) every time a
  `delegate_task` produces a peer task — whether the peer COMPLETED synchronously, is still
  running (parked), escalated (chained approval), or ended FAILED/REJECTED/CANCELED. Persisted
  with `omitempty` (records written before the field read back with no delegations) and
  survives a supervisor restart. No A2A protocol change.
- S3 — The UI task list API exposes each task's delegations; the UI renders a delegated task
  nested under its parent (second level), in delegation order, supporting N children from
  different roles and deeper nesting (a child that delegates gets its own children). A task that
  is someone's child is not repeated at the top level. A child whose parent is not in the list
  falls back to the top level. Approve/Reject on a nested child still works.
- S4 — Existing behavior unchanged: delegation outcomes, park/resume, chained approval, verdicts.

## Tasks

- T1 [S2,S4] supervisor: record delegations on the parent record + tests — gentle-ai-worker — commit: pending
- T2 [S3] ui + company_ui: expose delegations, render nested tree + tests — gentle-ai-worker — commit: pending

## Log

- L1 (user): see S1 (verbatim request, with screenshot showing `sw-engineer` and `supervisor` tasks as independent top-level cards).
- L2 (user decisions): link "En el padre (Recomendado)"; "Rama nueva desde master (Recomendado)".
- L3 (evidence): `core/supervisor/supervisor.go` `outcomeForPeer` drops `PeerTaskID` on the
  COMPLETED path (`actionOutcome{Output, Delegated}`); `AwaitingPeerTaskID` exists only while
  parked and is cleared on resume (`supervisor.go:410,456`). UI list aggregation:
  `cmd/company/company_ui.go` `ListTasks` (sets `Agent`), `ui/handler.go` `TaskRecord`,
  rendering `ui/app.js` `renderTasks` (flat, newest first).
