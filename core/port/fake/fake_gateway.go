package fake

import (
	"context"
	"sync"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// Gateway is a fake implementation of port.Gateway.
// It records every Send call so tests can assert invocation or non-invocation.
// Configurable ReturnErr lets tests simulate delivery failure.
//
// Thread-safe: mu guards all mutable state.
type Gateway struct {
	mu sync.Mutex

	// ReturnErr, when non-nil, is returned by Send.
	ReturnErr error

	// Calls records every payload passed to Send, in order.
	Calls []port.OutboundMessage
}

var _ port.Gateway = (*Gateway)(nil)

// Send validates the channel allow-list, records the call, and returns ReturnErr.
// If ReturnErr is nil the call is considered a successful delivery.
func (g *Gateway) Send(_ context.Context, msg port.OutboundMessage) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	// Channel validation is part of the Gateway contract; enforce it even in the fake.
	if err := port.ValidateChannel(msg.Channel); err != nil {
		return err
	}
	g.Calls = append(g.Calls, msg)
	return g.ReturnErr
}

// CallCount returns the number of Send calls recorded.
func (g *Gateway) CallCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.Calls)
}

// LastCall returns the most recent payload, or false if Send was never called.
func (g *Gateway) LastCall() (port.OutboundMessage, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.Calls) == 0 {
		return port.OutboundMessage{}, false
	}
	return g.Calls[len(g.Calls)-1], true
}

// WasCalled returns true if Send was called with the given channel.
func (g *Gateway) WasCalled(channel string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.Calls {
		if c.Channel == channel {
			return true
		}
	}
	return false
}

// Reset clears recorded calls and configurable return values.
func (g *Gateway) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Calls = nil
	g.ReturnErr = nil
}
