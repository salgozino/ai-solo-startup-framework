// Package mcp implements the MCP transport server for the framework.
// It registers one tool per risk_policy action kind and records ActionIntents
// in a per-invocation authenticated sink. No external effects are produced by
// this package — tools/call acknowledges and records; Gateway.Send is never called here.
package mcp

import (
	"errors"
	"sync"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// defaultIntentCap is the maximum number of ActionIntents a Sink can hold per invocation.
const defaultIntentCap = 100

// ErrSinkFull is returned by Sink.Record when the intent cap has been reached.
var ErrSinkFull = errors.New("mcp: intent sink is full")

// Sink buffers ActionIntents for a single adapter invocation.
// All methods are safe for concurrent use.
type Sink struct {
	mu      sync.Mutex
	intents []port.ActionIntent
	cap     int
}

// NewSink creates a Sink with the given capacity. When cap is <= 0, defaultIntentCap is used.
func NewSink(cap int) *Sink {
	if cap <= 0 {
		cap = defaultIntentCap
	}
	return &Sink{cap: cap}
}

// Record appends intent to the sink. Returns ErrSinkFull when the capacity is reached;
// the intent is NOT recorded in that case.
func (s *Sink) Record(intent port.ActionIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.intents) >= s.cap {
		return ErrSinkFull
	}
	s.intents = append(s.intents, intent)
	return nil
}

// Read returns a defensive copy of the recorded intents in insertion order.
// Never returns nil — an empty sink returns an empty (non-nil) slice.
func (s *Sink) Read() []port.ActionIntent {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]port.ActionIntent, len(s.intents))
	copy(result, s.intents)
	return result
}
