# Client follow-up on finished CEO tasks

Let the client answer a finished CEO task with "I don't like this, I want this other change":
the answer becomes a new CEO task linked to the old one that inherits the whole thread.
OpenSpec change: `openspec/changes/client-follow-up/`.

## Specs

- **S1** — User request (verbatim): "El cliente (usuario que le pide algo al ceo) que cuando el
  CEO le diga che termine, el cliente pueda decir, pero esto no me gusta, quier oeste otro
  cambio." A follow-up can be requested on any terminal CEO task: `COMPLETED`, `FAILED`, `REJECTED` or
  `CANCELED` (user decision: "Cualquier terminal del CEO"; A2A's `TaskState.Terminal()`).
- **S2** — "que las tasks creadas puedan resumirse y haya un follow up". A2A forbids messages to
  terminal tasks (`a2asrv/agentexec.go:227`), so a follow-up is a NEW task (user decision:
  "A) Task nueva vinculada"): its message carries `ReferenceTasks=[previous]` and the previous
  task's `ContextID`; the record stores `FollowUpOf`. The previous task is left untouched.
- **S3** — The follow-up inherits the whole chain (user decision: "Toda la cadena"): the previous
  task's transcript, then its outcome (state and final output), then the client's new message.
  Rounds are counted only from the follow-up's own turns, so inherited rounds never exhaust
  `max_rounds`.
- **S4** — Invalid follow-ups are refused before any task is created: empty message → 400,
  unknown or non-CEO task → 404, task not terminal → 409. The supervisor itself fails a follow-up
  whose referenced task is unknown or not terminal.
- **S5** — "Esto tanto en el framework como en la UI": every terminal CEO task card offers
  "Request changes" with a message box; a follow-up is nested under the task it follows.

## Tasks

- [x] T1 — S2, S3, S4 (supervisor part): follow-up execution in `core/supervisor` — inline — commit: 4e4c47c
- [x] T2 — S1, S4: `POST /api/follow-up` + adapter wiring in `ui/handler.go`, `cmd/company` — worker — commit: feat(ui) follow-up commit (this branch, after 4e4c47c)
- [x] T3 — S5: UI card action and threading in `ui/app.js`, `ui/style.css`; README — worker — commit: same commit as T2

## Log

- **L1** — "Me gustaria que agerguemos algo nuevo en el framework. El cliente (usuario que le pide
  algo al ceo) que cuando el CEO le diga che termine, el cliente pueda decir, pero esto no me
  gusta, quier oeste otro cambio. Es decir, que las tasks creadas puedan resumirse y haya un
  follow up. Esto tanto en el framework como en la UI"
- **L2** — Decisions: model "A) Task nueva vinculada (recomendado)"; scope "Cualquier terminal del
  CEO"; context "Toda la cadena".
- **L3** — Evidence: a2asrv rejects messages to terminal tasks (`agentexec.go:227`);
  `ReferencedTasksLoader` reads only the in-memory a2asrv store, so the supervisor reads the
  referenced task from its own persistent `Store` instead (survives restarts).
- **L4** — Verification: `go build ./cmd/company && go vet ./... && go test ./...` all ok;
  `node --check ui/app.js` ok; `TestFollowUp_OverRealWire` proves the follow-up through the real
  a2asrv handler (new task, same ContextID, followed task unchanged). Not verified: the UI in a
  real browser. Known gap: a delegation child whose parent is not listed shows "Request changes";
  the request returns 404 and the UI shows a failure toast.
- **L5** — User: "Ok, arregla lo del boton." / "1 solo pr, no hay problema" / Playwright front
  tests go in a separate worktree, "pero no en este pr". Fix: `/api/tasks` exposes
  `can_follow_up` (terminal CEO tasks only, set in `companyUIAdapter.ListTasks`); the UI shows
  "Request changes" only when it is true. Closes the L4 known gap.
