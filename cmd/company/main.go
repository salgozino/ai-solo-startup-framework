// Package main is the composition root for the company agent framework CLI.
// It provides the `materialize` command that reads company.yaml and starts
// one supervised agent goroutine per agent entry, serving the monitoring UI
// on the CEO supervisor's HTTP mux.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/ui"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: company <command> [args]\n")
		fmt.Fprintf(os.Stderr, "commands:\n  materialize <company.yaml>\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "materialize":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: company materialize <company.yaml>\n")
			os.Exit(1)
		}
		if err := runMaterialize(os.Args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "materialize: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(1)
	}
}

// runMaterialize parses the company.yaml at path, starts one Supervisor goroutine per
// agent with injected Provider and Gateway, and serves the monitoring UI on the CEO's mux.
// It blocks until SIGINT or SIGTERM is received.
func runMaterialize(yamlPath string) error {
	cfg, err := config.Load(yamlPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	runtimes, err := materializeAgents(cfg, wireOptions{})
	if err != nil {
		return err
	}

	if len(runtimes) == 0 {
		return fmt.Errorf("no agents defined in %s", yamlPath)
	}

	// Wire monitoring UI on the CEO's (first) supervisor mux.
	// The CEO is always first; company.yaml convention puts ceo first.
	ceoBind := ":8080"
	ceoHandler := ui.NewUIHandler(runtimes[0].uiAdap)
	uiMux := http.NewServeMux()
	ceoHandler.Register(uiMux)

	uiSrv := &http.Server{Addr: ceoBind, Handler: uiMux}
	go func() {
		fmt.Fprintf(os.Stderr, "ui: monitoring server listening on %s\n", ceoBind)
		if err := uiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "ui: server error: %v\n", err)
		}
	}()

	// Block until signal.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	fmt.Fprintf(os.Stderr, "shutting down...\n")
	ctx := context.Background()
	_ = uiSrv.Shutdown(ctx)
	for _, rt := range runtimes {
		_ = rt.srv.Shutdown(ctx)
	}
	return nil
}
