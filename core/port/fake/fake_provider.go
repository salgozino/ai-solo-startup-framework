// Package fake provides test doubles for the core/port interfaces.
// Fakes record calls and return canned values; they contain no business logic.
// They are the canonical test-double layer used by contract tests and E2E tests.
package fake

import (
	"context"
	"sync"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// CompletedCall records a single call to Complete or CompleteError.
type CompletedCall struct {
	TaskID string
	// Result is non-nil for Complete calls.
	Result *port.TaskResult
	// Err is non-nil for CompleteError calls.
	Err error
}

// SendMessageCall records a single call to SendMessage or SendMessageStream.
type SendMessageCall struct {
	Target address.A2AAddress
	Text   string
	Wait   bool
	Stream bool
}

// SendTaskCall records a single call to SendTask.
type SendTaskCall struct {
	Target     address.A2AAddress
	Capability string
	Input      map[string]any
	Opts       *port.TaskOptions
}

// Provider is a fake implementation of port.Provider.
// All fields are exported so tests can configure returns and inspect records.
//
// Thread-safe: mu guards all mutable state so tests may share one instance across goroutines.
type Provider struct {
	mu sync.Mutex

	// Configurable returns —— set before calling the fake.
	ReturnTaskID   string
	ReturnErr      error // returned by SendMessage, SendTask, ResolveAgent
	ReturnAddress  address.A2AAddress
	ReturnStream   []port.StreamEvent // events emitted by SendMessageStream (Done appended automatically)
	CompleteErr    error              // error returned by Complete (not CompleteError)
	CompleteErrErr error              // error returned by CompleteError

	// Recorded calls — read after exercising the fake.
	Calls     []CompletedCall
	MsgCalls  []SendMessageCall
	TaskCalls []SendTaskCall
}

var _ port.Provider = (*Provider)(nil)

// Complete records the call and returns CompleteErr (nil by default).
func (f *Provider) Complete(taskID string, result port.TaskResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := result
	f.Calls = append(f.Calls, CompletedCall{TaskID: taskID, Result: &r})
	return f.CompleteErr
}

// CompleteError records the call and returns CompleteErrErr (nil by default).
func (f *Provider) CompleteError(taskID string, agentErr error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, CompletedCall{TaskID: taskID, Err: agentErr})
	return f.CompleteErrErr
}

// SendMessage records the call and returns (ReturnTaskID, ReturnErr).
func (f *Provider) SendMessage(_ context.Context, target address.A2AAddress, text string, wait bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.MsgCalls = append(f.MsgCalls, SendMessageCall{Target: target, Text: text, Wait: wait})
	return f.ReturnTaskID, f.ReturnErr
}

// SendMessageStream records the call and returns a channel populated with ReturnStream events.
// A Done sentinel is appended if ReturnStream does not already end with one.
func (f *Provider) SendMessageStream(_ context.Context, target address.A2AAddress, text string) (<-chan port.StreamEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ReturnErr != nil {
		return nil, f.ReturnErr
	}
	f.MsgCalls = append(f.MsgCalls, SendMessageCall{Target: target, Text: text, Stream: true})

	events := make([]port.StreamEvent, len(f.ReturnStream))
	copy(events, f.ReturnStream)
	// Ensure the stream terminates.
	if len(events) == 0 || !events[len(events)-1].Done {
		events = append(events, port.StreamEvent{Done: true})
	}

	ch := make(chan port.StreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// ResolveAgent returns (ReturnAddress, ReturnErr).
func (f *Provider) ResolveAgent(_ context.Context, role string) (address.A2AAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ReturnErr != nil {
		return "", f.ReturnErr
	}
	return f.ReturnAddress, nil
}

// SendTask records the call and returns (ReturnTaskID, ReturnErr).
func (f *Provider) SendTask(_ context.Context, target address.A2AAddress, capability string, input map[string]any, opts *port.TaskOptions) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.TaskCalls = append(f.TaskCalls, SendTaskCall{Target: target, Capability: capability, Input: input, Opts: opts})
	return f.ReturnTaskID, f.ReturnErr
}

// CompleteCallCount returns the number of Complete or CompleteError calls recorded.
func (f *Provider) CompleteCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}

// SendMessageCallCount returns the number of SendMessage/SendMessageStream calls recorded.
func (f *Provider) SendMessageCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.MsgCalls)
}

// Reset clears all recorded calls and resets configurable return values.
func (f *Provider) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = nil
	f.MsgCalls = nil
	f.TaskCalls = nil
	f.ReturnTaskID = ""
	f.ReturnErr = nil
	f.ReturnAddress = ""
	f.ReturnStream = nil
	f.CompleteErr = nil
	f.CompleteErrErr = nil
}
