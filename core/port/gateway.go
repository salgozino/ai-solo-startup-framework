package port

import (
	"context"
	"fmt"
)

// allowedChannels is the closed set of outbound channel names the Gateway accepts.
// Any other channel name is rejected at the port boundary — before any adapter sees it.
// PR 4 (policy engine) gates the approval token; this guard is the structural barrier.
var allowedChannels = map[string]struct{}{
	"telegram": {},
	"email":    {},
}

// ValidateChannel returns an error if ch is not on the allow-list.
func ValidateChannel(ch string) error {
	if _, ok := allowedChannels[ch]; !ok {
		return fmt.Errorf("gateway: unknown channel %q; allowed: telegram, email", ch)
	}
	return nil
}

// OutboundMessage is the payload delivered to a Gateway.
type OutboundMessage struct {
	// Channel is the delivery channel. Must be "telegram" or "email".
	Channel string
	// Body is the message text or encoded content to send.
	Body string
	// Metadata carries optional channel-specific fields (e.g. parse mode).
	Metadata map[string]any
}

// Receipt is returned by a successful Gateway.Send call.
type Receipt struct {
	// MessageID is the provider-assigned identifier for the sent message, if available.
	MessageID string
	// Channel echoes the channel over which the message was delivered.
	Channel string
}

// Gateway is the outbound-only port for external notifications.
// Implementations live in gateways/; cmd/company injects them.
//
// Contract invariants:
//   - Send MUST validate msg.Channel before any network call.
//   - Send MUST return an error on any delivery failure; it MUST NOT panic.
//   - The recipient is always resolved from configuration, never from msg itself.
//   - Gateway is inbound-blind: it has no receive or poll path.
type Gateway interface {
	// Send delivers msg over msg.Channel.
	// Returns an error if the channel is unknown, the delivery failed, or configuration
	// is missing. On error the caller maps the task to FAILED.
	Send(ctx context.Context, msg OutboundMessage) error
}
