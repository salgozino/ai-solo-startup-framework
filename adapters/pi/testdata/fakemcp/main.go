// fakemcp is a tiny stdio MCP server for the pi adapter's startup probe tests. argv[1]
// selects the tool list it serves:
//
//	basic    — "list-files", "do__thing", "plain"
//	paged    — five tools "t1".."t5" served two per page (exercises nextCursor)
//	collide-free — a single tool "only"
//	collide  — "a-b" and "a_b" (both normalize to a_b)
//	empty    — no tools
//	hang     — never answers (sleeps forever without touching stdio)
//	crash    — exits 1 immediately
package main

import (
	"context"
	"log"
	"os"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	mode := "basic"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	var names []string
	opts := &gomcp.ServerOptions{}
	switch mode {
	case "basic":
		names = []string{"list-files", "plain"}
	case "paged":
		names = []string{"t1", "t2", "t3", "t4", "t5"}
		opts.PageSize = 2
	case "dunder":
		names = []string{"ok", "do__thing"}
	case "leading":
		names = []string{"_x"}
	case "thing":
		names = []string{"thing"}
	case "collide-free":
		names = []string{"only"}
	case "collide":
		names = []string{"a-b", "a_b"}
	case "empty":
	case "hang":
		time.Sleep(time.Hour)
	case "crash":
		os.Exit(1)
	default:
		log.Fatalf("unknown mode %q", mode)
	}
	srv := gomcp.NewServer(&gomcp.Implementation{Name: "fakemcp", Version: "0"}, opts)
	for _, n := range names {
		gomcp.AddTool(srv, &gomcp.Tool{Name: n, Description: n},
			func(context.Context, *gomcp.CallToolRequest, map[string]any) (*gomcp.CallToolResult, any, error) {
				return &gomcp.CallToolResult{}, nil, nil
			})
	}
	if err := srv.Run(context.Background(), &gomcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
