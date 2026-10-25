# claude-compact-milestone

Let Claude Code compact its own context at a milestone, with an explicit keep/drop focus.

Claude cannot run `/compact` itself (slash commands are user-only). This MCP server exposes one tool,
`compact_at_milestone(milestone, keep, drop)`. It queues the request, and a detached worker types
`/compact Milestone: … KEEP: … DROP: …` into the [cmux](https://cmux.com) pane once the prompt is idle.
It never uses `--force`, so it will not overwrite a draft you are typing.

## Install

```sh
go build -o claude-compact-milestone ./cmd/claude-compact-milestone
claude mcp add --scope user claude-compact-milestone -- "$PWD/claude-compact-milestone" serve
```

Add the hook to the `Stop`, `PostCompact` and `SessionStart` (matcher `compact`) lists in `~/.claude/settings.json`:

```json
{ "hooks": [{ "type": "command", "command": "/path/to/claude-compact-milestone hook", "timeout": 10 }] }
```

## How it works

`queued -> in_flight -> confirmed | failed`, one active request per pane, state in
`~/.claude/compact-pending` (override with `COMPACT_MILESTONE_DIR`). A request is `confirmed` only by
a post-compaction hook signal, never by the send succeeding.

Requires Claude Code running inside cmux (`CMUX_SURFACE_ID` set). Test with `go test -race ./...`.

## Layout

- `cmd/claude-compact-milestone`: entrypoint and CLI/MCP wiring
- `internal/command`: command builder and input sanitization
- `internal/hook`: hook event handling and worker dispatch
- `internal/pending`: file-backed state store, request queue, and worker locks
- `internal/worker`: prompt idle detection, command transmission, and detached worker lifecycle
