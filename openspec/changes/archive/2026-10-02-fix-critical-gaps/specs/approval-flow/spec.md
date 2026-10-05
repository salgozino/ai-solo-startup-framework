# Delta for approval-flow

## ADDED Requirements

### Requirement: Gateway Action Body Carries the Actual Message Text

When the supervisor executes a `telegram_send` action, the message body sent to the gateway
MUST be the text extracted from `ActionIntent.Payload["body"]`, not the action kind string.
The `TaskRecord` MUST persist the body as `PendingIntentBody string` (JSON `omitempty`) so
the resume path can retrieve it after a restart. If `Payload["body"]` is absent or not a
string, the body MUST default to empty string without panicking.

#### Scenario: Permit path sends the actual message body

- GIVEN a `telegram_send` intent carries `Payload["body"] = "Hello from CEO"`
- WHEN the policy permits the action and `executeAction` runs
- THEN the gateway receives `Body = "Hello from CEO"`, not the string `"telegram_send"`

#### Scenario: Resume path sends the persisted body

- GIVEN a `telegram_send` escalation was parked with `PendingIntentBody = "Hello from CEO"`
- WHEN the human approves and `executeResume` runs
- THEN the gateway receives `Body = "Hello from CEO"`, not an empty string or the kind string

#### Scenario: Missing payload body defaults to empty string

- GIVEN a `telegram_send` intent has no `Payload["body"]` key
- WHEN `executeAction` runs
- THEN the gateway receives `Body = ""` and no panic or error occurs

## MODIFIED Requirements

### Requirement: An Escalated Task Survives a Process Restart

A task parked in `INPUT_REQUIRED` MUST remain resumable after the owning supervisor process
restarts; the escalation MUST NOT depend on any in-memory or in-process state that a restart
would destroy. On restart, `RecoverOpenTasks` MUST seed every `INPUT_REQUIRED` task into the
a2a server's in-memory task store via a `registerFn func(ctx context.Context, taskID, input string) error`
injected at the call site. Registration MUST NOT trigger provider execution — it only
re-establishes the task in the store so a subsequent approval routes to the resume path.
`registerFn` returning `ErrTaskAlreadyExists` MUST be treated as idempotent success.

(Previously: `filterOpenTasks` excluded `INPUT_REQUIRED` tasks entirely — the a2a server's
in-memory store was never seeded on restart, so a subsequent approval found no existing task
and silently re-started it instead of resuming it.)

#### Scenario: Escalated task is still resumable after a restart

- GIVEN a task is parked in `INPUT_REQUIRED` awaiting a human verdict
- WHEN the supervisor process is restarted before a verdict is recorded
- THEN the task remains `INPUT_REQUIRED` after restart and can still be resumed by a subsequent
  approval

#### Scenario: INPUT_REQUIRED task is seeded into a2asrv on recovery

- GIVEN the file store contains a task record with `State = INPUT_REQUIRED`
- WHEN `RecoverOpenTasks` runs on restart with a `registerFn` closure over `store.Create`
- THEN `registerFn` is called with the task's ID, creating a minimal `INPUT_REQUIRED` entry
  in the in-memory store without triggering provider execution

#### Scenario: Approval after restart resumes the task, not restarts it

- GIVEN an `INPUT_REQUIRED` task was seeded into a2asrv via `registerFn` on restart
- WHEN the human's approval arrives as a new message carrying the same task ID
- THEN the supervisor routes it to `executeResume` (not a new `Execute`) and the task proceeds
  toward `COMPLETED`

#### Scenario: Double-recovery is idempotent

- GIVEN `RecoverOpenTasks` is called a second time for the same `INPUT_REQUIRED` task
- WHEN `registerFn` calls `store.Create` and receives `ErrTaskAlreadyExists`
- THEN the error is ignored and recovery completes successfully
