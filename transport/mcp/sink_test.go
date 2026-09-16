// Package mcp tests for the Sink type.
package mcp

import (
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

func TestSink_RecordAndRead(t *testing.T) {
	s := NewSink(10)

	intents := []port.ActionIntent{
		{Kind: "telegram_send", Payload: map[string]any{"body": "msg1"}},
		{Kind: "email_send", Payload: map[string]any{"to": "user@example.com"}},
		{Kind: "telegram_send", Payload: map[string]any{"body": "msg2"}},
	}

	for _, intent := range intents {
		if err := s.Record(intent); err != nil {
			t.Fatalf("Record: unexpected error: %v", err)
		}
	}

	got := s.Read()
	if len(got) != len(intents) {
		t.Fatalf("Read: expected %d intents, got %d", len(intents), len(got))
	}

	// Verify insertion order.
	for i, want := range intents {
		if got[i].Kind != want.Kind {
			t.Errorf("intent[%d]: expected Kind=%q, got %q", i, want.Kind, got[i].Kind)
		}
	}
}

func TestSink_CapEnforced(t *testing.T) {
	s := NewSink(2)

	first := port.ActionIntent{Kind: "a", Payload: nil}
	second := port.ActionIntent{Kind: "b", Payload: nil}
	third := port.ActionIntent{Kind: "c", Payload: nil}

	if err := s.Record(first); err != nil {
		t.Fatalf("Record first: unexpected error: %v", err)
	}
	if err := s.Record(second); err != nil {
		t.Fatalf("Record second: unexpected error: %v", err)
	}
	if err := s.Record(third); err == nil {
		t.Error("Record third: expected cap-exceeded error, got nil")
	}

	got := s.Read()
	if len(got) != 2 {
		t.Fatalf("Read after cap: expected 2 intents, got %d", len(got))
	}
	if got[0].Kind != "a" || got[1].Kind != "b" {
		t.Errorf("Read: unexpected contents: %v", got)
	}
}

func TestSink_EmptyRead(t *testing.T) {
	s := NewSink(10)
	got := s.Read()
	if got == nil {
		t.Error("Read on empty sink: expected empty slice (not nil)")
	}
	if len(got) != 0 {
		t.Errorf("Read on empty sink: expected 0 intents, got %d", len(got))
	}
}
