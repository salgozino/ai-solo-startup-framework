package fake

import (
	"context"
	"sync"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// DelegateCall records one Delegate invocation.
type DelegateCall struct {
	Role string
	Body string
}

// PeerTaskStateCall records one PeerTaskState invocation.
type PeerTaskStateCall struct {
	Role       string
	PeerTaskID string
}

// Delegator is a fake port.Delegator with per-role scripted results and a
// sequenced PeerTaskState return list.
//
// Thread-safe: mu guards all mutable state.
type Delegator struct {
	mu sync.Mutex

	// Results maps role → DelegationResult returned by Delegate for that role.
	Results map[string]port.DelegationResult
	// Errors maps role → error returned by Delegate for that role. Checked before Results.
	Errors map[string]error
	// BlockUntilCtxDone, when true, makes Delegate block until ctx is done and
	// then return ctx.Err() (simulates a hung peer; lets tests drive the timeout path).
	BlockUntilCtxDone bool
	// StateSequence is returned by successive PeerTaskState calls in order; the
	// last element repeats once exhausted. Empty → zero DelegationResult.
	StateSequence []port.DelegationResult
	// StateErr, when non-nil, is returned by every PeerTaskState call.
	StateErr error

	// Calls records every Delegate invocation in order.
	Calls []DelegateCall
	// StateCalls records every PeerTaskState invocation in order.
	StateCalls []PeerTaskStateCall

	// observedDeadline is the ctx deadline seen by the most recent Delegate call.
	// Unexported and read through ObservedDeadline so mu guards it like the rest.
	observedDeadline time.Time
	// observedHasDeadline reports whether that ctx carried a deadline at all.
	observedHasDeadline bool
}

var _ port.Delegator = (*Delegator)(nil)

// Delegate records the call and returns the scripted result for role. When
// BlockUntilCtxDone is set it blocks until ctx is done instead, simulating a
// hung peer so tests can drive the timeout path.
func (d *Delegator) Delegate(ctx context.Context, role, body string) (port.DelegationResult, error) {
	deadline, hasDeadline := ctx.Deadline()

	d.mu.Lock()
	d.Calls = append(d.Calls, DelegateCall{Role: role, Body: body})
	d.observedDeadline, d.observedHasDeadline = deadline, hasDeadline
	block := d.BlockUntilCtxDone
	d.mu.Unlock()

	if block {
		<-ctx.Done()
		return port.DelegationResult{}, ctx.Err()
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.Errors[role]; err != nil {
		return port.DelegationResult{}, err
	}
	return d.Results[role], nil
}

// PeerTaskState records the call and returns the next element of
// StateSequence in order, repeating the last element once exhausted.
func (d *Delegator) PeerTaskState(_ context.Context, role, peerTaskID string) (port.DelegationResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	idx := len(d.StateCalls)
	d.StateCalls = append(d.StateCalls, PeerTaskStateCall{Role: role, PeerTaskID: peerTaskID})

	if d.StateErr != nil {
		return port.DelegationResult{}, d.StateErr
	}
	if len(d.StateSequence) == 0 {
		return port.DelegationResult{}, nil
	}
	if idx >= len(d.StateSequence) {
		idx = len(d.StateSequence) - 1
	}
	return d.StateSequence[idx], nil
}

// CallCount returns the number of Delegate calls recorded.
func (d *Delegator) CallCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.Calls)
}

// StateCallCount returns the number of PeerTaskState calls recorded.
func (d *Delegator) StateCallCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.StateCalls)
}

// ObservedDeadline returns the context deadline seen inside the most recent
// Delegate call and whether that context carried one. It lets a test observe the
// timeout the caller actually applied, rather than only whether the call returned.
func (d *Delegator) ObservedDeadline() (time.Time, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.observedDeadline, d.observedHasDeadline
}

// LastCall returns the most recent Delegate call, or false if Delegate was never called.
func (d *Delegator) LastCall() (DelegateCall, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.Calls) == 0 {
		return DelegateCall{}, false
	}
	return d.Calls[len(d.Calls)-1], true
}
