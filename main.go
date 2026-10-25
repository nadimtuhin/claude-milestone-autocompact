package main

import (
	"context"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type compactInput struct {
	Milestone string   `json:"milestone" jsonschema:"what was just completed"`
	Keep      []string `json:"keep" jsonschema:"what the summary must preserve: decisions, open TODOs, files being edited, failing tests, next step"`
	Drop      []string `json:"drop" jsonschema:"what the summary may discard: raw tool output, dead ends, superseded plans, file dumps"`
}

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "worker":
		if len(os.Args) < 3 {
			err = fmt.Errorf("usage: worker <surface>")
			break
		}
		NewWorker().Run(os.Args[2])
	case "hook":
		err = HandleHook(os.Stdin, os.Stdout, DefaultStore(), os.Getenv("CMUX_SURFACE_ID"), spawnWorker)
	default:
		err = fmt.Errorf("unknown command %q (serve | worker | hook)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "claude-compact-milestone:", err)
		os.Exit(1)
	}
}

func serve() error {
	server := mcp.NewServer(&mcp.Implementation{Name: "claude-compact-milestone", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "compact_at_milestone",
		Description: "Queue a /compact with an explicit keep/drop focus. It is typed into this pane after the current turn ends, so finish your turn after calling it.",
	}, queueCompaction)
	return server.Run(context.Background(), &mcp.StdioTransport{})
}

func queueCompaction(_ context.Context, _ *mcp.CallToolRequest, in compactInput) (*mcp.CallToolResult, any, error) {
	command, err := BuildCommand(in.Milestone, in.Keep, in.Drop)
	if err != nil {
		return nil, nil, err
	}
	surface := os.Getenv("CMUX_SURFACE_ID")
	if _, err := DefaultStore().Enqueue(surface, command); err != nil {
		return nil, nil, err
	}
	if err := spawnWorker(surface); err != nil {
		return nil, nil, fmt.Errorf("queued, but the worker failed to start: %w", err)
	}
	text := "Queued. The /compact will be typed once this turn ends. Finish your turn now."
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}
