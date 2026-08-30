package supervisor

import (
	"strings"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

const truncationMarker = "[context truncated: oldest messages dropped to fit budget]"

// assembleBoundedContext builds a BoundedContext capped at budget characters.
// Messages are ordered oldest-first; when the assembled total exceeds budget,
// oldest messages are dropped first and the truncation marker is prepended.
//
// budget == 0 means no cap (all messages pass through unmarked).
func assembleBoundedContext(messages []port.ContextMessage, budget int) port.BoundedContext {
	if budget == 0 || len(messages) == 0 {
		return port.BoundedContext{Messages: messages}
	}

	total := 0
	for _, m := range messages {
		total += len(m.Content)
	}

	if total <= budget {
		return port.BoundedContext{Messages: messages}
	}

	// Drop oldest messages until we fit within budget (leave room for the truncation marker).
	markerLen := len(truncationMarker)
	remaining := budget - markerLen
	if remaining < 0 {
		remaining = 0
	}

	// Walk from newest backward, accumulating until we exceed remaining budget.
	kept := 0
	acc := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msgLen := len(messages[i].Content)
		if acc+msgLen > remaining {
			break
		}
		acc += msgLen
		kept++
	}

	trimmed := messages[len(messages)-kept:]

	// Build a synthetic truncation marker message at position zero.
	marker := port.ContextMessage{
		Role:    "system",
		Content: truncationMarker,
	}
	result := make([]port.ContextMessage, 0, kept+1)
	result = append(result, marker)
	result = append(result, trimmed...)

	return port.BoundedContext{
		Messages:         result,
		Truncated:        true,
		TruncationMarker: truncationMarker,
	}
}

// contextText flattens a BoundedContext into a single string for injection into a provider.
func contextText(bc port.BoundedContext) string {
	if len(bc.Messages) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, m := range bc.Messages {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(m.Role)
		sb.WriteString(": ")
		sb.WriteString(m.Content)
	}
	return sb.String()
}
