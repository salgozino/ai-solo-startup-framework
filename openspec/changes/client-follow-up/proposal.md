# Proposal: Client Follow-Up on Finished CEO Tasks

Let the human client answer a finished CEO task — "I don't like this, I want this other change" —
without losing what already happened. Today a CEO task that reaches `COMPLETED`, `FAILED` or
`REJECTED` is closed for good: the client can only send a brand-new task that knows nothing of
the previous one.

**Affected agent roles**: `ceo` (the only role the human talks to).

## Intent

A2A forbids messages to a task in a terminal state (`a2asrv/agentexec.go` rejects them with
`ErrInvalidParams`), so a terminal task cannot be reopened. A2A's own answer is a new task that
references the old one (`Message.ReferenceTasks`) inside the same conversation
(`Message.ContextID`). This change adopts that model:

- the follow-up is a new CEO task, linked to the previous one (`FollowUpOf`);
- it inherits the whole chain's transcript, the previous outcome and the client's new message;
- its round budget counts only its own rounds;
- the monitoring UI offers "Request changes" on every terminal CEO task and nests follow-ups
  under the task they follow.

## Scope

- `core/supervisor`: follow-up detection, transcript seeding, round counting, persisted links.
- `ui`, `cmd/company`: `POST /api/follow-up`, validation, CEO-only routing, card action, threading.

## Non-goals

- Reopening a terminal task in place (forbidden by A2A).
- Follow-ups on delegated (non-CEO) tasks.
- Follow-ups from Telegram or any gateway other than the monitoring UI.

## Risks

- Transcript growth: a long chain grows the provider input. It is bounded by the existing
  context budget (`assembleBoundedContext`, oldest turns dropped first) and delivered over stdin
  by the claude and pi adapters; opencode keeps its documented argv ceiling.
- Persisted schema: new `omitempty` fields only (`FollowUpOf`, `ContextID`, `BaseTurns`); records
  written before this change read back unchanged.
