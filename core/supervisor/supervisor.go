package supervisor

import (
	"context"
	"fmt"
	"iter"

	a2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

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
	// When nil, action intents are not classified (delegation-only mode).
	PolicyEngine *policy.Engine
	// Gateway is the outbound gateway used to execute approved action intents.
	// Required when PolicyEngine is set and intents may be Permitted.
	Gateway port.Gateway
	// Role is the agent role declared in company.yaml (e.g. "ceo", "engineer").
	// Used for policy capability checks.
	Role string
	// PolicyConfig is the full risk_policy map from company.yaml.
	// Keyed by action kind (e.g. "telegram_send").
	PolicyConfig map[string]config.Policy
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
func New(cfg Config) *Supervisor {
	s := &Supervisor{
		cfg: cfg,
		fsm: newFSM(),
	}
	return s
}

// MarkReady transitions the supervisor from STARTING (or RECOVERING) to IDLE.
// The transport layer calls this once the HTTP server is listening.
func (s *Supervisor) MarkReady() {
	s.fsm.ready()
}

// RecoverOpenTasks loads any tasks in non-terminal state from the store and
// re-enters them into the a2asrv task queue by transitioning to RECOVERING.
// Call before MarkReady; a no-op if the store has no open tasks for this address.
func (s *Supervisor) RecoverOpenTasks(ctx context.Context, handler a2asrv.RequestHandler) error {
	records, err := s.cfg.Store.LoadAll(s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("supervisor: load open tasks: %w", err)
	}

	openTasks := filterOpenTasks(records)
	if len(openTasks) == 0 {
		return nil
	}

	// Signal RECOVERING state.
	s.fsm.recover()

	for _, rec := range openTasks {
		// Re-submit each open task as a new SendMessage carrying its TaskID.
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
	return nil
}

// Shutdown starts draining the supervisor's queue. Does not wait for completion.
func (s *Supervisor) Shutdown() {
	s.fsm.drain()
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
		defer s.fsm.taskDone()

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

		// Announce WORKING.
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}

		// If RunTask is available on the provider, use it for local execution.
		// Otherwise fall back to the A2A delegation path (ResolveAgent + SendMessage).
		if s.cfg.PolicyEngine != nil {
			s.executeWithPolicy(ctx, execCtx, yield, rec)
		} else {
			s.executeDelegation(ctx, execCtx, yield, rec)
		}
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
	// Check stored state: was this task parked in INPUT_REQUIRED?
	rec, err := s.cfg.Store.Load(s.cfg.Addr, taskID)
	if err != nil {
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errorMessage(err)), nil) //nolint
		return
	}

	// Parse the pending intent from the stored record.
	if rec.PendingIntentKind == "" {
		// No pending intent — this is a normal task resumption (not an escalation resume).
		// Treat it as a new execution.
		rec.State = string(a2a.TaskStateWorking)
		_ = s.cfg.Store.Save(s.cfg.Addr, rec)
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) //nolint
		s.executeWithPolicy(ctx, execCtx, yield, rec)
		return
	}

	// This is an approval for a pending escalation. Check the approval input.
	// Convention: the approval message body is "approve" or "reject".
	isApproved := approvalInput == "approve"

	if !isApproved {
		// Human rejected → REJECTED.
		rec.State = string(a2a.TaskStateRejected)
		_ = s.cfg.Store.Save(s.cfg.Addr, rec)
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateRejected, nil), nil) //nolint
		return
	}

	// Human approved → mint token and execute the action.
	yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) //nolint

	token := s.cfg.PolicyEngine.MintApprovalToken()
	if err := s.executeAction(ctx, rec.PendingIntentKind, token); err != nil {
		s.markFailed(rec)
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errorMessage(err)), nil) //nolint
		return
	}

	rec.State = string(a2a.TaskStateCompleted)
	_ = s.cfg.Store.Save(s.cfg.Addr, rec)
	yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil) //nolint
}

// executeWithPolicy runs the provider via RunTask, classifies action intents, and routes
// HardDeny → REJECTED, Escalate → INPUT_REQUIRED, Permit → gateway send.
func (s *Supervisor) executeWithPolicy(
	ctx context.Context,
	execCtx *a2asrv.ExecutorContext,
	yield func(a2a.Event, error) bool,
	rec TaskRecord,
) {
	result, err := s.cfg.Provider.RunTask(ctx, rec.TaskID, rec.Input)
	if err != nil {
		s.markFailed(rec)
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errorMessage(err)), nil) //nolint
		return
	}

	// Classify each action intent.
	for _, intent := range result.ActionIntents {
		portIntent := policy.ActionIntent{Kind: intent.Kind}
		classResult := s.cfg.PolicyEngine.Classify(portIntent, s.cfg.Role, s.cfg.PolicyConfig)

		switch classResult.Kind { //nolint:exhaustive
		case policy.HardDeny:
			// REJECTED — not FAILED. Terminal, no escalation, no send, no token.
			rec.State = string(a2a.TaskStateRejected)
			_ = s.cfg.Store.Save(s.cfg.Addr, rec)
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateRejected, nil), nil) //nolint
			return

		case policy.Escalate:
			// Persist as INPUT_REQUIRED with the pending intent kind so the resume path knows what to approve.
			rec.State = string(a2a.TaskStateInputRequired)
			rec.PendingIntentKind = intent.Kind

			payload, _ := policy.MarshalPayload(policy.EscalationPayload{
				ActionKind: intent.Kind,
				TaskID:     rec.TaskID,
			})
			_ = s.cfg.Store.Save(s.cfg.Addr, rec)

			// Use a text part instead of a data part so that the a2asrv in-memory
			// task store (gob-encoded) can serialize the message without needing to
			// register json.RawMessage / jsontext.Value with gob.
			msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(string(payload)))
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateInputRequired, msg), nil) //nolint
			return

		case policy.Permit:
			if err := s.executeAction(ctx, intent.Kind, classResult.ApprovalToken); err != nil {
				s.markFailed(rec)
				yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errorMessage(err)), nil) //nolint
				return
			}
		}
	}

	// All intents handled (or none) → COMPLETED.
	rec.State = string(a2a.TaskStateCompleted)
	_ = s.cfg.Store.Save(s.cfg.Addr, rec)
	yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil) //nolint
}

// executeDelegation is the A2A peer-routing path (used when no PolicyEngine is configured).
// The supervisor resolves a peer agent and delegates the task via SendMessage.
func (s *Supervisor) executeDelegation(
	ctx context.Context,
	execCtx *a2asrv.ExecutorContext,
	yield func(a2a.Event, error) bool,
	rec TaskRecord,
) {
	// Assemble bounded context from prior messages.
	history := buildHistory(execCtx)
	bc := assembleBoundedContext(history, s.cfg.ContextBudget)

	// Dispatch to the provider (A2A network client).
	targetAddr, err := s.cfg.Provider.ResolveAgent(ctx, roleOf(s.cfg.Addr))
	if err != nil {
		// Cannot resolve peer — mark task FAILED.
		s.markFailed(rec)
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errorMessage(err)), nil) //nolint
		return
	}

	taskText := contextText(bc) + "\n" + rec.Input
	_, providerErr := s.cfg.Provider.SendMessage(ctx, targetAddr, taskText, true)
	if providerErr != nil {
		// Non-zero exit or provider error → FAILED (never silently dropped).
		s.markFailed(rec)
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, errorMessage(providerErr)), nil) //nolint
		return
	}

	// Success — mark COMPLETED.
	rec.State = string(a2a.TaskStateCompleted)
	_ = s.cfg.Store.Save(s.cfg.Addr, rec)

	yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil) //nolint
}

// executeAction calls the gateway with the given approval token for the action kind.
// In v1, the only action kind is "telegram_send" → Gateway.Send.
func (s *Supervisor) executeAction(ctx context.Context, actionKind string, token string) error {
	if s.cfg.Gateway == nil {
		return fmt.Errorf("supervisor: gateway required for action %q but none configured", actionKind)
	}
	if err := s.cfg.PolicyEngine.ValidateToken(token); err != nil {
		return fmt.Errorf("supervisor: invalid approval token for action %q: %w", actionKind, err)
	}
	return s.cfg.Gateway.Send(ctx, port.OutboundMessage{
		Channel: "telegram",
		Body:    actionKind,
	})
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
	rec.State = string(a2a.TaskStateFailed)
	_ = s.cfg.Store.Save(s.cfg.Addr, rec)
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

// buildHistory builds a ContextMessage slice from the stored task's message history.
func buildHistory(execCtx *a2asrv.ExecutorContext) []port.ContextMessage {
	if execCtx.StoredTask == nil {
		return nil
	}
	history := make([]port.ContextMessage, 0, len(execCtx.StoredTask.History))
	for _, m := range execCtx.StoredTask.History {
		history = append(history, port.ContextMessage{
			Role:    string(m.Role),
			Content: messageText(m),
		})
	}
	return history
}

// filterOpenTasks returns records whose state is not terminal.
func filterOpenTasks(records []TaskRecord) []TaskRecord {
	var open []TaskRecord
	for _, r := range records {
		state := a2a.TaskState(r.State)
		if !state.Terminal() && state != a2a.TaskStateInputRequired {
			open = append(open, r)
		}
	}
	return open
}

// tenantOf extracts the tenant segment from an A2AAddress ("name/tenant").
func tenantOf(addr address.A2AAddress) string {
	return addr.Tenant()
}

// roleOf extracts the agent-name segment and uses it as the role for ResolveAgent.
// In v1 the agent name is also the role identifier.
func roleOf(addr address.A2AAddress) string {
	return addr.Name()
}

// errorMessage wraps err into an a2a.Message for inclusion in a status event.
func errorMessage(err error) *a2a.Message {
	if err == nil {
		return nil
	}
	return a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(err.Error()))
}
