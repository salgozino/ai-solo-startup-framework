package supervisor

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"os"
	"time"

	a2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// defaultDelegateTimeout bounds a blocking Delegate call when Config.DelegateTimeout
// is unset (design D10).
const defaultDelegateTimeout = 10 * time.Minute

// Config holds the parameters needed to construct a Supervisor.
type Config struct {
	// Addr is the full A2A address (name/tenant) for this supervisor.
	Addr address.A2AAddress
	// Provider is the A2A-network-client port injected by the composition root.
	Provider port.Provider
	// Store persists task records for crash/restart recovery.
	Store *Store
	// ContextBudget is the maximum number of characters for assembled BoundedContext.
	// Zero means no cap.
	ContextBudget int
	// PolicyEngine classifies action intents emitted by the provider.
	// Required; New returns an error when nil.
	PolicyEngine *policy.Engine
	// Gateway is the outbound gateway used to execute approved action intents.
	// Required when PolicyEngine is set and intents may be Permitted.
	Gateway port.Gateway
	// Delegator sends delegate_task intents to a role-addressed peer over A2A.
	// Optional: a supervisor whose role is never granted delegate_task may leave it
	// nil, in which case a delegate_task intent fails explicitly instead of panicking.
	Delegator port.Delegator
	// DelegateTimeout bounds one blocking Delegate call (design D10). Zero means
	// defaultDelegateTimeout. On expiry the delegating task FAILS naming the peer
	// role and this duration; the peer's own task is never canceled.
	DelegateTimeout time.Duration
	// Role is the agent role declared in company.yaml (e.g. "ceo", "engineer").
	// Used for policy capability checks.
	Role string
	// PolicyConfig is the full risk_policy map from company.yaml.
	// Keyed by action kind (e.g. "telegram_send").
	PolicyConfig map[string]config.Policy
	// Logger is the structured logger for supervisor events.
	// When nil, a default JSON logger writing to stderr is used.
	Logger *slog.Logger
	// OnStateChange, when non-nil, is called after each supervisor or task state
	// transition and after each persisted store write. Wire this at the composition
	// root to push SSE events to connected UI clients.
	// Must be nil-safe to call from any goroutine; the notify() method guards it.
	OnStateChange func()
}

// Status is the observable state of a supervisor at a point in time.
type Status struct {
	Addr  address.A2AAddress
	State State
}

// Supervisor owns a single agent identity, its task queue, and lifecycle FSM.
// It implements a2asrv.AgentExecutor; the transport layer (a2asrv handler) is wired
// in transport/a2a. The supervisor is created in STARTING state and transitions to
// IDLE once the endpoint is registered (via MarkReady).
type Supervisor struct {
	cfg Config
	fsm *fsm
}

// New creates a Supervisor in STARTING state.
// Call MarkReady() after the A2A endpoint is registered.
//
// Config.PolicyEngine is required: there is no supported delegation-only,
// no-policy construction mode. A nil PolicyEngine returns an explicit error
// here, at construction time, rather than nil-pointer-panicking the first
// time a task is executed.
func New(cfg Config) (*Supervisor, error) {
	if cfg.PolicyEngine == nil {
		return nil, fmt.Errorf("supervisor: New: Config.PolicyEngine must not be nil")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	s := &Supervisor{
		cfg: cfg,
		fsm: newFSM(),
	}
	return s, nil
}

// log returns the supervisor's logger, scoped with the task and agent context.
func (s *Supervisor) log(taskID string) *slog.Logger {
	return s.cfg.Logger.With("agent", string(s.cfg.Addr), "task_id", taskID)
}

// SetOnStateChange registers fn to be called after each supervisor or task state
// change. Safe to call before or after construction; replaces any prior hook.
func (s *Supervisor) SetOnStateChange(fn func()) {
	s.cfg.OnStateChange = fn
}

// notify calls cfg.OnStateChange if it is set.
// Must be called outside any FSM lock to avoid lock-order hazards.
func (s *Supervisor) notify() {
	if s.cfg.OnStateChange != nil {
		s.cfg.OnStateChange()
	}
}

// MarkReady transitions the supervisor from STARTING (or RECOVERING) to IDLE.
// The transport layer calls this once the HTTP server is listening.
func (s *Supervisor) MarkReady() {
	s.fsm.ready()
	s.notify()
}

// RecoverOpenTasks loads persisted tasks and re-enters them based on their state:
//   - WORKING tasks: re-submitted via handler.SendMessage (as before).
//   - INPUT_REQUIRED tasks: registered in the a2asrv task store via registerFn so that
//     a subsequent approval message is recognized as a resume (StoredTask != nil).
//
// Call before MarkReady; a no-op if the store has no recoverable tasks for this address.
func (s *Supervisor) RecoverOpenTasks(
	ctx context.Context,
	handler a2asrv.RequestHandler,
	registerFn func(ctx context.Context, taskID, input string) error,
) error {
	records, err := s.cfg.Store.LoadAll(s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("supervisor: load open tasks: %w", err)
	}

	workingTasks := filterWorkingTasks(records)
	inputRequiredTasks := filterInputRequiredTasks(records)

	if len(workingTasks) == 0 && len(inputRequiredTasks) == 0 {
		return nil
	}

	// Signal RECOVERING state.
	s.fsm.recover()

	for _, rec := range workingTasks {
		// Re-submit each working task as a new SendMessage carrying its TaskID.
		// The a2asrv framework recognizes a message with TaskID as a resume.
		msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(rec.Input))
		msg.TaskID = a2a.TaskID(rec.TaskID)
		req := &a2a.SendMessageRequest{
			Tenant:  tenantOf(s.cfg.Addr),
			Message: msg,
		}
		if _, err := handler.SendMessage(ctx, req); err != nil {
			// Log but continue: a single recovery failure should not block others.
			_ = fmt.Errorf("supervisor: recovery send for task %s: %w", rec.TaskID, err)
		}
	}

	for _, rec := range inputRequiredTasks {
		// Register each INPUT_REQUIRED task in the a2asrv store so that the next
		// SendMessage with the same TaskID is recognised as a resume.
		if err := registerFn(ctx, rec.TaskID, rec.Input); err != nil {
			// Log but continue.
			_ = fmt.Errorf("supervisor: recovery register INPUT_REQUIRED task %s: %w", rec.TaskID, err)
		}
	}

	return nil
}

// Shutdown starts draining the supervisor's queue. Does not wait for completion.
func (s *Supervisor) Shutdown() {
	s.fsm.drain()
	s.notify()
}

// Status returns a snapshot of the supervisor's current state.
func (s *Supervisor) Status() Status {
	return Status{
		Addr:  s.cfg.Addr,
		State: s.fsm.current(),
	}
}

// Execute implements a2asrv.AgentExecutor.Execute.
// It is called by the a2asrv runtime in a dedicated goroutine per task.
func (s *Supervisor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		// Panic recovery: if yield() or the provider panics, mark the task as FAILED.
		// The existing defer s.fsm.taskDone() handles the FSM transition.
		// Nested recover() swallows secondary panics from yield() itself.
		defer func() {
			if r := recover(); r != nil {
				func() {
					defer func() { recover() }() // swallow secondary panic from yield
					yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed,
						errorMessage(fmt.Errorf("panic: %v", r))), nil) //nolint
				}()
			}
		}()

		isResume := execCtx.StoredTask != nil

		// Announce SUBMITTED if this is a new task (StoredTask == nil = no prior state).
		// The SDK requires the first event to be a *Task (not *TaskStatusUpdateEvent).
		if !isResume {
			submitted := a2a.NewSubmittedTask(execCtx, execCtx.Message)
			if !yield(submitted, nil) {
				return
			}
		}

		// Transition supervisor FSM: IDLE → WORKING (or RECOVERING → WORKING).
		s.fsm.taskStarted()
		s.notify()
		defer func() { s.fsm.taskDone(); s.notify() }()

		input := messageText(execCtx.Message)
		taskID := string(execCtx.TaskID)

		// Resume path: this is an approval (new SendMessage with matching TaskID).
		// StoredTask != nil means a2asrv recognized it as a resume.
		if isResume {
			s.executeResume(ctx, execCtx, yield, taskID, input)
			return
		}

		// New task path.

		// Persist task as WORKING.
		rec := TaskRecord{
			TaskID: taskID,
			State:  string(a2a.TaskStateWorking),
			Input:  input,
			Owner:  string(s.cfg.Addr),
		}
		if err := s.cfg.Store.Save(s.cfg.Addr, rec); err != nil {
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errorMessage(err)), nil) //nolint
			return
		}
		s.notify()

		// Announce WORKING.
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}

		s.executeWithPolicy(ctx, execCtx, yield, rec)
	}
}

// executeResume handles the resume path when a human approval arrives as a new SendMessage
// carrying the original TaskID. The supervisor recognizes it via execCtx.StoredTask != nil.
func (s *Supervisor) executeResume(
	ctx context.Context,
	execCtx *a2asrv.ExecutorContext,
	yield func(a2a.Event, error) bool,
	taskID string,
	approvalInput string,
) {
	log := s.log(taskID)
	log.Info("resume.start", "approval_input", approvalInput)

	// Check stored state: was this task parked in INPUT_REQUIRED?
	rec, err := s.cfg.Store.Load(s.cfg.Addr, taskID)
	if err != nil {
		log.Error("resume.load_task.failed", "error", err)
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errorMessage(err)), nil) //nolint
		return
	}

	// Parse the pending intent from the stored record.
	if rec.PendingIntentKind == "" {
		// No pending intent — this is a normal task resumption (not an escalation resume).
		// Treat it as a new execution.
		log.Info("resume.no_pending_intent", "action", "re_execute")
		rec.State = string(a2a.TaskStateWorking)
		_ = s.cfg.Store.Save(s.cfg.Addr, rec)
		s.notify()
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) //nolint
		s.executeWithPolicy(ctx, execCtx, yield, rec)
		return
	}

	// This is an approval for a pending escalation. Check the approval input.
	// Convention: the approval message body is "approve" or "reject".
	isApproved := approvalInput == "approve"

	if !isApproved {
		// Human rejected → REJECTED.
		log.Info("resume.rejected", "intent", rec.PendingIntentKind)
		rec.State = string(a2a.TaskStateRejected)
		_ = s.cfg.Store.Save(s.cfg.Addr, rec)
		s.notify()
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateRejected, nil), nil) //nolint
		return
	}

	// Human approved → mint token and execute the action.
	log.Info("resume.approved", "intent", rec.PendingIntentKind)
	yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) //nolint

	token := s.cfg.PolicyEngine.MintApprovalToken()

	// A resumed delegate_task has no persisted target role (TaskRecord carries only
	// PendingIntentKind/PendingIntentBody), so executeDelegation fails it explicitly;
	// the shipped policy classifies delegate_task as safe, so this path is unreachable
	// unless an operator marks it risky.
	outcome, err := s.executeAction(ctx, rec.PendingIntentKind, rec.PendingIntentBody, "", token)
	if err != nil {
		log.Error("resume.action.failed", "intent", rec.PendingIntentKind, "error", err)
		s.markFailed(rec)
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errorMessage(err)), nil) //nolint
		return
	}
	if outcome.Output != "" {
		rec.Output = outcome.Output
	}

	log.Info("resume.completed", "intent", rec.PendingIntentKind)
	rec.State = string(a2a.TaskStateCompleted)
	_ = s.cfg.Store.Save(s.cfg.Addr, rec)
	s.notify()
	yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, outputMessage(rec.Output)), nil) //nolint
}

// executeWithPolicy runs the provider via RunTask, classifies action intents, and routes
// HardDeny → REJECTED, Escalate → INPUT_REQUIRED, Permit → gateway send.
func (s *Supervisor) executeWithPolicy(
	ctx context.Context,
	execCtx *a2asrv.ExecutorContext,
	yield func(a2a.Event, error) bool,
	rec TaskRecord,
) {
	log := s.log(rec.TaskID)

	log.Info("runtask.start", "input", rec.Input)

	result, err := s.cfg.Provider.RunTask(ctx, rec.TaskID, rec.Input)
	if err != nil {
		log.Error("runtask.failed", "error", err)
		s.markFailed(rec)
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errorMessage(err)), nil) //nolint
		return
	}

	log.Info("runtask.done",
		"output", result.Output,
		"action_intents_count", len(result.ActionIntents),
	)
	rec.Output = result.Output

	// Classify each action intent.
	for _, intent := range result.ActionIntents {
		portIntent := policy.ActionIntent{Kind: intent.Kind}
		classResult := s.cfg.PolicyEngine.Classify(portIntent, s.cfg.Role, s.cfg.PolicyConfig)

		log.Info("intent.classified",
			"kind", intent.Kind,
			"classification", string(classResult.Kind),
		)

		switch classResult.Kind { //nolint:exhaustive
		case policy.HardDeny:
			// REJECTED — not FAILED. Terminal, no escalation, no send, no token.
			log.Warn("intent.hard_deny", "kind", intent.Kind)
			rec.State = string(a2a.TaskStateRejected)
			_ = s.cfg.Store.Save(s.cfg.Addr, rec)
			s.notify()
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateRejected, nil), nil) //nolint
			return

		case policy.Escalate:
			// Persist as INPUT_REQUIRED with the pending intent kind and body so the resume path knows what to approve.
			log.Info("intent.escalate", "kind", intent.Kind)
			rec.State = string(a2a.TaskStateInputRequired)
			rec.PendingIntentKind = intent.Kind
			rec.PendingIntentBody = extractBody(intent)

			payload, _ := policy.MarshalPayload(policy.EscalationPayload{
				ActionKind: intent.Kind,
				TaskID:     rec.TaskID,
			})
			_ = s.cfg.Store.Save(s.cfg.Addr, rec)
			s.notify()

			// Use a text part instead of a data part so that the a2asrv in-memory
			// task store (gob-encoded) can serialize the message without needing to
			// register json.RawMessage / jsontext.Value with gob.
			msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(string(payload)))
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateInputRequired, msg), nil) //nolint
			return

		case policy.Permit:
			log.Info("action.execute", "kind", intent.Kind)
			outcome, err := s.executeAction(ctx, intent.Kind, extractBody(intent), extractTarget(intent), classResult.ApprovalToken)
			if err != nil {
				log.Error("action.failed", "kind", intent.Kind, "error", err)
				s.markFailed(rec)
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errorMessage(err)), nil) //nolint
				return
			}
			if outcome.Output != "" {
				// The peer's terminal output becomes the delegating task's output
				// (spec: "The Peer's Terminal Result Is Copied Into the Delegating Task's Output").
				rec.Output = outcome.Output
			}
			log.Info("action.done", "kind", intent.Kind)
		}
	}

	// All intents handled (or none) → COMPLETED. The terminal event carries the
	// task output so a delegating peer can read it back via GetTask (design D9).
	log.Info("task.completed", "state", "COMPLETED")
	rec.State = string(a2a.TaskStateCompleted)
	_ = s.cfg.Store.Save(s.cfg.Addr, rec)
	s.notify()
	yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, outputMessage(rec.Output)), nil) //nolint
}

// effectiveBudget returns the context budget to use for context assembly.
// When Config.ContextBudget is non-zero (operator override), it takes precedence.
// When zero, the provider's declared ContextBudget is used (zero means no cap).
// Spec: provider-adapter – "An operator-configured override takes precedence";
// "The provider-declared budget is the default absent an override".
func (s *Supervisor) effectiveBudget() int {
	if s.cfg.ContextBudget != 0 {
		return s.cfg.ContextBudget
	}
	return s.cfg.Provider.Capabilities().ContextBudget
}

// actionOutcome reports what executeAction produced beyond succeeding or failing.
type actionOutcome struct {
	// Output is the peer's terminal output for a completed delegation; empty for
	// every other action kind.
	Output string
}

// executeAction executes a Permit-classified (or human-approved) action intent.
// delegate_task routes to the Delegator port (design D6); every other kind
// routes to the Gateway exactly as before.
func (s *Supervisor) executeAction(ctx context.Context, actionKind, body, target, token string) (actionOutcome, error) {
	if err := s.cfg.PolicyEngine.ValidateToken(token); err != nil {
		return actionOutcome{}, fmt.Errorf("supervisor: invalid approval token for action %q: %w", actionKind, err)
	}
	if actionKind == port.KindDelegateTask {
		return s.executeDelegation(ctx, target, body)
	}
	if s.cfg.Gateway == nil {
		return actionOutcome{}, fmt.Errorf("supervisor: gateway required for action %q but none configured", actionKind)
	}
	return actionOutcome{}, s.cfg.Gateway.Send(ctx, port.OutboundMessage{
		Channel: "telegram",
		Body:    body,
	})
}

// executeDelegation runs one blocking delegation to the peer fulfilling role and
// maps the peer's observed state onto the delegating task's fate:
//   - COMPLETED → success, peer output returned in the outcome;
//   - any other terminal state (FAILED, REJECTED, CANCELED) → error naming the role;
//   - INPUT_REQUIRED (the peer escalated) → immediate error stating the peer escalated
//     and chained approval is not yet wired (spec agent-delegation, interim
//     requirement). Chained approval is delivered by the follow-up change
//     agent-delegation-chained-approval; nothing here polls or parks;
//   - any other non-terminal state → error naming it as a port.Delegator protocol
//     violation, because that port requires such a state to arrive as an error, never
//     as a result. It is a bug in the Delegator, not an escalation, and must not be
//     described as one.
//
// The call is bounded by DelegateTimeout; on expiry the error names the role and
// the configured duration. The peer's task is never canceled (design D10).
func (s *Supervisor) executeDelegation(ctx context.Context, role, body string) (actionOutcome, error) {
	if s.cfg.Delegator == nil {
		return actionOutcome{}, fmt.Errorf("supervisor: delegate_task requires a delegator but none configured")
	}
	if role == "" {
		return actionOutcome{}, fmt.Errorf("supervisor: delegate_task intent has no target role")
	}
	timeout := s.cfg.DelegateTimeout
	if timeout <= 0 {
		timeout = defaultDelegateTimeout
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res, err := s.cfg.Delegator.Delegate(dctx, role, body)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return actionOutcome{}, fmt.Errorf("supervisor: delegation to role %q timed out after %s; peer task left running", role, timeout)
		}
		return actionOutcome{}, fmt.Errorf("supervisor: delegation to role %q failed: %w", role, err)
	}

	state := a2a.TaskState(res.State)
	switch {
	case state == a2a.TaskStateCompleted:
		return actionOutcome{Output: res.Output}, nil
	case state.Terminal():
		return actionOutcome{}, fmt.Errorf("supervisor: delegation to role %q failed: peer task %q ended in state %s", role, res.PeerTaskID, res.State)
	case state == a2a.TaskStateInputRequired:
		return actionOutcome{}, fmt.Errorf("supervisor: delegation to role %q: peer escalated (peer task %q is %s) and chained approval is not yet wired; the peer task is left running for its own human verdict", role, res.PeerTaskID, res.State)
	default:
		// Not COMPLETED, not terminal, not the one legal non-terminal state: the
		// Delegator implementation broke its own contract. Reporting this as an
		// escalation would invent a human verdict that no peer is waiting for.
		return actionOutcome{}, fmt.Errorf("supervisor: delegation to role %q: peer returned unrecognized state %q; port.Delegator requires any non-terminal state other than %s to be returned as an error, never as a result", role, res.State, a2a.TaskStateInputRequired)
	}
}

// Cancel implements a2asrv.AgentExecutor.Cancel.
func (s *Supervisor) Cancel(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		_ = s.cfg.Store.Delete(s.cfg.Addr, string(execCtx.TaskID))
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil) //nolint
	}
}

// Addr returns the full A2A address of this supervisor.
func (s *Supervisor) Addr() address.A2AAddress {
	return s.cfg.Addr
}

// ListTasks returns all persisted task records for this supervisor.
// The UI handler uses this to populate /api/tasks.
func (s *Supervisor) ListTasks() ([]TaskRecord, error) {
	return s.cfg.Store.LoadAll(s.cfg.Addr)
}

// StatusStr returns the current supervisor lifecycle state as a string (e.g. "IDLE", "WORKING").
func (s *Supervisor) StatusStr() string {
	return string(s.fsm.current())
}

// helpers

func (s *Supervisor) markFailed(rec TaskRecord) {
	s.log(rec.TaskID).Warn("task.failed", "state", "FAILED")
	rec.State = string(a2a.TaskStateFailed)
	_ = s.cfg.Store.Save(s.cfg.Addr, rec)
	s.notify()
}

// messageText extracts the first text part from a message, or empty string.
func messageText(msg *a2a.Message) string {
	if msg == nil {
		return ""
	}
	for _, part := range msg.Parts {
		if t := part.Text(); t != "" {
			return t
		}
	}
	return ""
}

// extractBody reads the "body" key from intent.Payload as a string.
// Returns "" if the key is absent, the map is nil, or the value is not a string.
func extractBody(intent port.ActionIntent) string {
	if intent.Payload == nil {
		return ""
	}
	v, ok := intent.Payload["body"]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// extractTarget reads the port.TargetArg key from intent.Payload as a string.
// Returns "" if the key is absent, the map is nil, or the value is not a string.
func extractTarget(intent port.ActionIntent) string {
	if intent.Payload == nil {
		return ""
	}
	v, ok := intent.Payload[port.TargetArg]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// filterWorkingTasks returns records whose state is not terminal and not INPUT_REQUIRED.
// Used during recovery to re-submit tasks that were actively running when the supervisor last stopped.
func filterWorkingTasks(records []TaskRecord) []TaskRecord {
	var working []TaskRecord
	for _, r := range records {
		state := a2a.TaskState(r.State)
		if !state.Terminal() && state != a2a.TaskStateInputRequired {
			working = append(working, r)
		}
	}
	return working
}

// filterInputRequiredTasks returns only records in INPUT_REQUIRED state.
// Used during recovery to re-register parked tasks with the a2asrv task store.
func filterInputRequiredTasks(records []TaskRecord) []TaskRecord {
	var parked []TaskRecord
	for _, r := range records {
		if a2a.TaskState(r.State) == a2a.TaskStateInputRequired {
			parked = append(parked, r)
		}
	}
	return parked
}

// tenantOf extracts the tenant segment from an A2AAddress ("name/tenant").
func tenantOf(addr address.A2AAddress) string {
	return addr.Tenant()
}

// outputMessage wraps the task output into an agent-role a2a.Message carried by
// the terminal COMPLETED status event, so the output crosses the wire and a
// delegating peer can read it from Task.Status.Message (design D9). Returns nil
// when there is no output, so an empty text part is never fabricated. A text
// part is used (not a data part) so the gob-encoded in-memory task store can
// serialize it without extra type registration.
func outputMessage(output string) *a2a.Message {
	if output == "" {
		return nil
	}
	return a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(output))
}

// errorMessage wraps err into an a2a.Message for inclusion in a status event.
// Returns a generic message to avoid leaking internal details (file paths, env vars).
// The real error is logged by the structured logger at the call site.
func errorMessage(err error) *a2a.Message {
	if err == nil {
		return nil
	}
	return a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("task execution failed"))
}
