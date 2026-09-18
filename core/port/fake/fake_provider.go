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

// SendTaskCall records a single call to SendTask.
type SendTaskCall struct {
	Target     address.A2AAddress
	Capability string
	Input      map[string]any
	Opts       *port.TaskOptions
}

// RunTaskCall records a single call to RunTask.
type RunTaskCall struct {
	TaskID string
	Input  string
}

// Provider is a fake implementation of port.Provider.
// All fields are exported so tests can configure returns and inspect records.
//
// Thread-safe: mu guards all mutable state so tests may share one instance across goroutines.
type Provider struct {
	mu sync.Mutex

	// Configurable returns —— set before calling the fake.
	ReturnTaskID       string
	ReturnErr          error                     // returned by SendTask
	CompleteErr        error                     // error returned by Complete (not CompleteError)
	CompleteErrErr     error                     // error returned by CompleteError
	ReturnRunResult    port.ProviderResult       // returned by RunTask
	ReturnRunErr       error                     // error returned by RunTask
	ReturnCapabilities port.ProviderCapabilities // returned by Capabilities

	// Recorded calls — read after exercising the fake.
	Calls     []CompletedCall
	TaskCalls []SendTaskCall
	RunCalls  []RunTaskCall
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

// RunTask records the call and returns (ReturnRunResult, ReturnRunErr).
func (f *Provider) RunTask(_ context.Context, taskID string, input string) (port.ProviderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RunCalls = append(f.RunCalls, RunTaskCall{TaskID: taskID, Input: input})
	return f.ReturnRunResult, f.ReturnRunErr
}

// Capabilities returns ReturnCapabilities (thread-safe).
// Zero value is valid: zero ContextBudget means no cap.
func (f *Provider) Capabilities() port.ProviderCapabilities {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ReturnCapabilities
}

// RunTaskCallCount returns the number of RunTask calls recorded.
func (f *Provider) RunTaskCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.RunCalls)
}

// Reset clears all recorded calls and resets configurable return values.
func (f *Provider) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = nil
	f.TaskCalls = nil
	f.RunCalls = nil
	f.ReturnTaskID = ""
	f.ReturnErr = nil
	f.CompleteErr = nil
	f.CompleteErrErr = nil
	f.ReturnRunResult = port.ProviderResult{}
	f.ReturnRunErr = nil
	f.ReturnCapabilities = port.ProviderCapabilities{}
}
