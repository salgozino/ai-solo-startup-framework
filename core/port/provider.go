// Package port defines the seam between the framework core and external providers/gateways.
// Nothing in core/ imports adapters/ or gateways/; only cmd/company wires concretes to these ports.
package port

import (
	"context"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
)

// ActionIntent is an action a provider wants to perform (e.g. "telegram_send").
// The policy engine classifies each intent before any external effect occurs.
type ActionIntent struct {
	// Kind is the action identifier declared in risk_policy (e.g. "telegram_send").
	Kind string
	// Payload carries action-specific data (e.g. message body, recipient hint).
	Payload map[string]any
}

// ProviderResult is the outcome of a RunTask call.
// It carries the task output and any action intents the provider wants to perform.
type ProviderResult struct {
	// Output is the human-readable or structured result from the task.
	Output string
	// ActionIntents lists any actions the provider requests; classified by the policy engine.
	ActionIntents []ActionIntent
}

// TaskResult holds the outcome of a completed task.
type TaskResult struct {
	// Output is the human-readable or structured result from the task.
	Output string
	// Metadata carries arbitrary key-value pairs the provider may include.
	Metadata map[string]any
}

// TaskOptions carries optional parameters for SendTask.
type TaskOptions struct {
	Tenant       string
	BudgetTokens int
	SessionID    string
	// Blocking, when true, makes SendTask wait until the task reaches a terminal state.
	Blocking bool
}

// StreamEvent is a single event emitted on the channel returned by SendMessageStream.
type StreamEvent struct {
	// TaskID identifies which task this event belongs to.
	TaskID string
	// Text is the incremental or final text content of the event.
	Text string
	// Done signals that the stream is finished (terminal event).
	Done bool
	// Err carries any error that terminated the stream.
	Err error
}

// Provider is the port that the framework core uses to communicate with the A2A network.
// The supervisor uses Provider to delegate tasks to peer agents and to report terminal outcomes
// back to its own A2A infrastructure. Implementations live in adapters/; cmd/company injects them.
//
// Contract invariants:
//   - Provider is stateless from the caller's perspective; no session state is retained.
//   - SendMessage with wait=true blocks until the remote task reaches a terminal state.
//   - SendMessageStream returns immediately; the caller must drain and close the channel.
//   - ResolveAgent returns an error for any role not registered in the company topology.
type Provider interface {
	// Complete reports that taskID finished successfully with the given result.
	// Idempotent: calling Complete on an already-complete task MUST return nil.
	Complete(taskID string, result TaskResult) error

	// CompleteError reports that taskID finished with a failure.
	// Idempotent: calling CompleteError on an already-failed task MUST return nil.
	CompleteError(taskID string, agentErr error) error

	// SendMessage sends text to target and, when wait is true, blocks until the remote task
	// reaches a terminal state, returning the resulting taskID. When wait is false it returns
	// immediately with the assigned taskID.
	SendMessage(ctx context.Context, target address.A2AAddress, text string, wait bool) (string, error)

	// SendMessageStream sends text to target and returns a channel of incremental events.
	// The channel is closed after a Done event or an error event.
	SendMessageStream(ctx context.Context, target address.A2AAddress, text string) (<-chan StreamEvent, error)

	// ResolveAgent looks up the A2AAddress for the agent that fulfils role.
	// Returns an error if no agent is registered for that role.
	ResolveAgent(ctx context.Context, role string) (address.A2AAddress, error)

	// SendTask dispatches a capability task to target with the given input and options.
	// Returns the assigned taskID. opts may be nil.
	SendTask(ctx context.Context, target address.A2AAddress, capability string, input map[string]any, opts *TaskOptions) (taskID string, err error)

	// RunTask executes the task locally (e.g. by spawning a Claude Code subprocess).
	// Returns a ProviderResult that may include action intents for policy classification.
	// The supervisor calls this when it is acting as the executing agent, not as a router.
	RunTask(ctx context.Context, taskID string, input string) (ProviderResult, error)
}

// BoundedContext carries the assembled context passed into a task invocation.
// It is pre-capped by the supervisor according to ProviderCapabilities.ContextBudget.
type BoundedContext struct {
	// Messages is the ordered slice of prior messages/resolutions, oldest first.
	Messages []ContextMessage
	// Truncated is true when the original context exceeded the budget and was trimmed.
	Truncated bool
	// TruncationMarker is the marker prepended when Truncated is true.
	TruncationMarker string
}

// ContextMessage is a single entry in a BoundedContext.
type ContextMessage struct {
	Role    string
	Content string
	At      time.Time
}

// ResumePoint carries data the supervisor provides when resuming a previously parked task.
// The provider receives it as part of the invocation; no provider-side session is required.
type ResumePoint struct {
	// TaskID is the original task being resumed.
	TaskID string
	// ApprovalToken is the opaque token minted by the policy engine for this resumption.
	ApprovalToken string
	// Input carries the resume-specific input (e.g. human verdict, new message parts).
	Input string
}
