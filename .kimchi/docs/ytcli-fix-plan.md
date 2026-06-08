# ytcli Fix Plan

## Bugs
1. Unknown commands (e.g. `ytcli resume`) fall through to TUI search mode, which crashes in headless environments with `open /dev/tty: no such device or address`.
2. `ytcli queue play` loads only a *single* item into mpv. `ytcli next` sends `playlist-next` to mpv but mpv only has one item loaded — nothing happens.
3. `ytcli play` without a URL shows usage instead of resuming/pausing an active mpv instance.

## Fix Overview

### Chunk 1: Graceful headless fallback + resume command (cmd/ytcli/main.go)
- Import `github.com/charmbracelet/x/term` (already in go.mod indirect) or use `golang.org/x/sys/unix` to check `isatty`.
- In the `default` case before `runTUI`, check `term.IsTerminal(int(os.Stdin.Fd()))`.
  - If NOT a terminal: print `{"ok":false,"error":"unknown_command","message":"unknown command: <cmd>"}` and exit 1.
- Add `resume` case in switch: if mpv running, send `set pause no`; else error.
- Modify `cmdPlay`:
  - If `len(os.Args) < 3`, try to connect to mpv and send `cycle pause` instead of showing usage.
  - If mpv not running, show usage.

### Chunk 2: Load full playlist into mpv (internal/player/player.go + cmd/ytcli/main.go)
- Add `AppendToPlaylist(url string)` method to `Player` that sends `loadfile url append-play`.
- Instead of `loadfile url replace`, we'll continue using `replace` for the first item.
- Rename `playAndReport` to `playSingleAndReport` for single-URL play.
- Add new `playPlaylistAndReport(items []youtube.Item, startIndex int, audioOnly bool, action string)`:
  - Calls `p.Start()`.
  - Calls `p.Play(items[startIndex].URL)` with `replace`.
  - For each subsequent item, calls `p.AppendToPlaylist(item.URL)`.
  - Returns JSON with `{"ok":true,"action":action,"audio_only":audioOnly,"items":items,"index":startIndex}`.
- Change `cmdQueuePlay` to use `playPlaylistAndReport` with ALL queue items. After loading, clear the queue file.
- Change `cmdPlaylistPlay` to use `playPlaylistAndReport` with ALL playlist items.

### Chunk 3: next/prev return current track info (cmd/ytcli/main.go)
- After `p.SendCommand` in `cmdControl`, query `media-title` from mpv and include it in the JSON output.
  - e.g. `{"ok":true,"command":["playlist-next"],"title":"Song Name","paused":false}`

## File Changes
- `cmd/ytcli/main.go` — headless detection, resume command, play-with-no-args behavior, playlist loading, next/prev feedback
- `internal/player/player.go` — add `AppendToPlaylist` method

## Acceptance Criteria
- [ ] `ytcli resume` in a headless session returns JSON `ok:true` and resumes mpv, or `ok:false` if no mpv.
- [ ] `ytcli unknowncommand` in a headless session returns JSON `ok:false` with `error: unknown_command`, no TTY crash.
- [ ] `ytcli play` with no URL toggles pause if mpv is running.
- [ ] `ytcli queue play` loads the entire queue into mpv's internal playlist.
- [ ] `ytcli next` advances to the next item in the mpv playlist and returns the new title.
- [ ] `ytcli prev` goes to the previous item and returns the title.
- [ ] Code builds with `go build ./cmd/ytcli`.
