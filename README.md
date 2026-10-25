# claude-milestone-autocompact

Let Claude Code compact its own context at a milestone, with an explicit keep/drop focus.

Claude cannot run `/compact` itself (slash commands are user-only). This MCP server exposes one tool,
`compact_at_milestone(milestone, keep, drop)`. It queues the request, and a detached worker types
`/compact Milestone: … KEEP: … DROP: …` into the [cmux](https://cmux.com) pane once the prompt is idle.
It never uses `--force`, so it will not overwrite a draft you are typing.

Optional `resume`: a one-line message typed after compaction is confirmed, so work continues without you.
Omit it and the pane idles after compaction.

## Install

```sh
go build -o claude-milestone-autocompact ./cmd/claude-milestone-autocompact
claude mcp add --scope user claude-milestone-autocompact -- "$PWD/claude-milestone-autocompact" serve
```

Add the hook to the `Stop`, `PostCompact` and `SessionStart` (matcher `compact`) lists in `~/.claude/settings.json`:

```json
{ "hooks": [{ "type": "command", "command": "/path/to/claude-milestone-autocompact hook", "timeout": 10 }] }
```

## How it works

`queued -> in_flight -> confirmed | failed`, one active request per pane, state in
`~/.claude/compact-pending` (override with `COMPACT_MILESTONE_DIR`). A request is `confirmed` only by
a post-compaction hook signal, never by the send succeeding.

Requires Claude Code running inside cmux (`CMUX_SURFACE_ID` set). Test with `go test -race ./...`.

## Layout

- `cmd/claude-milestone-autocompact`: entrypoint and CLI/MCP wiring
- `internal/command`: command builder and input sanitization
- `internal/hook`: hook event handling and worker dispatch
- `internal/pending`: file-backed state store, request queue, and worker locks
- `internal/worker`: prompt idle detection, command transmission, and detached worker lifecycle

## Troubleshooting

- Renaming or moving the binary breaks sessions that were already running, because their MCP server and hooks hold the old path. Restart those sessions.
- The worker decides the pane is idle by reading the screen. Claude Code draws the empty prompt as the heart glyph followed by a non-breaking space (U+00A0), so any idle regex must accept U+00A0. If the compaction is never typed, read the `reason` field in `~/.claude/compact-pending/<surface>.failed.json`, which ends with the last lines the worker saw.
