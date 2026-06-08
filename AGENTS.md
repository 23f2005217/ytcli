# AGENTS.md

Compact orientation for working on `ytcli`.

## What this is

Single-binary Go CLI for searching YouTube and playing results through `mpv`,
with a Bubble Tea TUI and a JSON-output agent mode. Module path: `ytcli`
(`go.mod`). Go 1.26.2+ required.

## Build & verify

- Build: `go build -o ytcli ./cmd/ytcli` (the README uses this; the repo
  already has a pre-built `ytcli` binary in root and a stale `yt` binary that is
  tracked in git — prefer rebuilding over running the checked-in binary).
- Lint/typecheck: `go vet ./...` (clean). No test suite exists; do not invent
  one without being asked.
- There is no Makefile, no CI workflow, no pre-commit, no `opencode.json`, and
  no prior `AGENTS.md`/`CLAUDE.md`.

## Binaries

There are two `main` packages:

- `cmd/ytcli/main.go` — the real CLI/TUI. Almost all work happens here.
- `cmd/testyoutube/main.go` — a tiny developer scratch binary that calls
  `youtube.Search` and dumps JSON. Use it to iterate on the search backend
  without going through the TUI:
  `go run ./cmd/testyoutube <query>`.

## Runtime architecture (where to look when changing behavior)

- `cmd/ytcli/main.go` — all CLI subcommands. `main()` is a `switch` on
  `os.Args[1]`; unknown args fall through to TUI search **only when stdin is a
  TTY** (headless callers get a JSON `unknown_command` error). JSON output
  goes through `outputJSON`; failures go through `exitError`.
- `internal/youtube/youtube.go` — wraps `yt-dlp` with `ytsearch20:<q>` and
  `--flat-playlist`, 10s context timeout, hard cap of 20 results.
- `internal/player/player.go` — owns the `mpv` subprocess. IPC socket lives at
  `/tmp/mpv-socket` (hardcoded; no env override). `Start()` spawns mpv only if
  no socket is reachable; `Connect()` only attaches. `AppendToPlaylist` uses
  `loadfile ... append-play`; `Play` uses `loadfile ... replace`.
- `internal/app/app.go` — Bubble Tea model for the TUI. Keybindings, queue
  handling, and end-of-file auto-advance live here.
- `internal/playlist`, `internal/history` — single-file JSON stores under
  `~/.config/ytcli/`.

## Behavioral quirk to know before editing

The TUI and the CLI play things differently:

- The **TUI** extracts a direct stream URL via
  `yt-dlp -f bestaudio -g <url>` and hands that to `mpv` (see `playCmd` in
  `internal/app/app.go`). So in the TUI, audio-only is effectively baked in
  regardless of `audioOnly` on the player.
- The **CLI** (`play`, `play-search`, `playlist play`, `queue play`) passes the
  raw YouTube URL to `mpv` and relies on mpv to invoke `yt-dlp`. `--audio-only`
  is forwarded as `mpv --no-video` at start.

If you change one path, check the other.

## Data layout (runtime, not in repo)

`~/.config/ytcli/`:
- `playlist.json` — the implicit "default" playlist
- `playlists/<sanitized-name>.json` — named playlists
- `history.json`
- `queue.json`

`/tmp/mpv-socket` — mpv IPC socket, only one instance at a time.

## External requirements

- `yt-dlp` on `PATH` (search + stream extraction; 10s timeout enforced).
- `mpv` on `PATH` (spawned with `--idle=yes --input-ipc-server=/tmp/mpv-socket
  --no-terminal`, plus `--no-video` when audio-only).
- TUI mode requires a TTY. Every CLI subcommand works headless and prints
  JSON to stdout — that is the agent-friendly contract.

## Minor things to ignore

- `.kimchi/` is a personal planning directory (untracked). Ignore unless
  referenced.
- The README's install snippet uses the placeholder
  `https://github.com/username/ytcli.git`; the real remote is
  `git@github.com:23f2005217/ytcli.git`.
