// Package telegram_test — live integration test.
// Task 6.4: env-gated; skipped in -short mode (and whenever TELEGRAM_BOT_TOKEN or
// TELEGRAM_OWNER_ID are not set). Exercises the real Telegram API path.
package telegram_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/gateways/telegram"
)

// TestIntegration_LiveSend sends a real message to the configured Telegram owner.
// Skipped unless TELEGRAM_BOT_TOKEN and TELEGRAM_OWNER_ID are both set in the
// environment and -short is not passed.
func TestIntegration_LiveSend(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live Telegram integration test in -short mode")
	}

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	ownerID := os.Getenv("TELEGRAM_OWNER_ID")
	if token == "" || ownerID == "" {
		t.Skip("skipping live Telegram integration test: TELEGRAM_BOT_TOKEN and TELEGRAM_OWNER_ID must be set")
	}

	gw, err := telegram.New("TELEGRAM_BOT_TOKEN", "TELEGRAM_OWNER_ID")
	if err != nil {
		t.Fatalf("telegram.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msg := port.OutboundMessage{
		Channel: "telegram",
		Body:    "[ai-solo-startup-framework integration test] Hello from PR 6 live test.",
	}
	if err := gw.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
}
