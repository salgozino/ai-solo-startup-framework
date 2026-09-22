// Package mcp white-box tests for the tool contract advertised to agent CLIs.
package mcp

import (
	"slices"
	"strings"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
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

// TestBuildInputSchema_RequiredArgsPerKind — task 7.2/7.3.
// Design D11: delegate_task is the only action kind whose schema requires a
// second argument (target, identifying the delegation's target role). Every
// other kind — including an arbitrary undeclared one — requires only body.
func TestBuildInputSchema_RequiredArgsPerKind(t *testing.T) {
	cases := []struct {
		kind string
		want []string
	}{
		{kind: "telegram_send", want: []string{bodyArg}},
		{kind: port.KindDelegateTask, want: []string{bodyArg, port.TargetArg}},
		{kind: "arbitrary_kind", want: []string{bodyArg}},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			schema := buildInputSchema(tc.kind)
			if !slices.Equal(schema.Required, tc.want) {
				t.Errorf("buildInputSchema(%q).Required = %v, want %v", tc.kind, schema.Required, tc.want)
			}
			if tc.kind == port.KindDelegateTask {
				target, ok := schema.Properties[port.TargetArg]
				if !ok {
					t.Fatalf("buildInputSchema(%q) declares no %q property", tc.kind, port.TargetArg)
				}
				if target.Type != "string" {
					t.Errorf("target property type = %q, want %q", target.Type, "string")
				}
				if !strings.Contains(strings.ToLower(target.Description), "identifies the delegation's target role") {
					t.Errorf("target property description = %q, want it to state it identifies the delegation's target role", target.Description)
				}
			}
		})
	}
}
