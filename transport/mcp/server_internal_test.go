// Package mcp white-box test for Server.Err (Serve-death observability).
package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/config"
)

// TestServer_AbnormalDeath_IsObservable: RED — a dead Serve goroutine must become visible via Err.
func TestServer_AbnormalDeath_IsObservable(t *testing.T) {
	reg := NewRegistry()
	srv := New("acme", map[string]config.Policy{"telegram_send": {}}, reg)
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Shutdown(context.Background()) //nolint:errcheck
	if err := srv.Err(); err != nil {
		t.Fatalf("Err before death: expected nil, got %v", err)
	}
	srv.listener.Close() //nolint:errcheck // force abnormal death, bypassing Shutdown
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Err() != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("Err: expected non-nil error after abnormal listener close, got nil")
}
