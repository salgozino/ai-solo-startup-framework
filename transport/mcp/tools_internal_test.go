// Package mcp white-box tests for the tool contract advertised to agent CLIs.
package mcp

import (
	"strings"
	"testing"
)

// TestBuildToolDescription_NamesBodyArgument: RED — the description must name the "body"
// argument, since core/supervisor's extractBody reads only Payload["body"]. It must keep
// the acknowledge-without-execute disclosure intact.
func TestBuildToolDescription_NamesBodyArgument(t *testing.T) {
	desc := buildToolDescription("telegram_send")
	lower := strings.ToLower(desc)

	for _, want := range []string{"body", "records", "intent", "does not", "telegram_send"} {
		if !strings.Contains(lower, want) {
			t.Errorf("description missing %q: %q", want, desc)
		}
	}
}
