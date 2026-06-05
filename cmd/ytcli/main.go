package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"ytcli/internal/app"
	"ytcli/internal/history"
	"ytcli/internal/player"
	"ytcli/internal/playlist"
	"ytcli/internal/queue"
	"ytcli/internal/youtube"
)

type playOptions struct {
	URL       string
	AudioOnly bool
	JSON      bool
}

type searchOptions struct {
	Query       string
	Limit       int
	MinDuration int
	MaxDuration int
	AudioOnly   bool
	JSON        bool
}

func main() {
	if len(os.Args) < 2 {
		printHelp()
		return
	}

	cmd := os.Args[1]

	switch cmd {
	case "help", "--help", "-h":
		printHelp()
	case "search":
		cmdSearch()
	case "play":
		cmdPlay()
	case "play-search":
		cmdPlaySearch()
	case "playlist":
		cmdPlaylist()
	case "history":
		cmdHistory()
	case "queue":
		cmdQueue()
	case "next":
		cmdControl("playlist-next")
	case "prev":
		cmdControl("playlist-prev")
	case "pause":
		cmdControl("cycle", "pause")
	case "resume":
		cmdResume()
	case "stop":
		cmdControl("stop")
	case "current":
		cmdCurrent()
	case "status":
		cmdStatus()
	case "agent-help":
		printAgentHelp()
	default:
		// In headless mode (non-TTY), unknown commands return JSON error instead of crashing
		if !term.IsTerminal(uintptr(os.Stdin.Fd())) {
			outputJSON(map[string]interface{}{
				"ok":      false,
				"error":   "unknown_command",
				"message": fmt.Sprintf("unknown command: %s", cmd),
			})
			os.Exit(1)
		}
		// Treat everything as a TUI search query
		query := strings.Join(os.Args[1:], " ")
		runTUI(query)
	}
}

func printHelp() {
	fmt.Println(`ytcli - YouTube CLI player

Usage:
  ytcli <search query>                  Open TUI search results
  ytcli search <query>                  Search and print JSON results
  ytcli play-search [opts] <query>      Search, choose first match, and play it
  ytcli play [--audio-only] <url>       Play a URL with mpv

Agent-friendly commands:
  ytcli playlist names
  ytcli playlist create <name>
  ytcli playlist delete <name>
  ytcli playlist add [name] <url> [title]
  ytcli playlist list [name]
  ytcli playlist remove [name] <index>
  ytcli playlist clear [name]
  ytcli playlist play [name] [index] [--audio-only]

  ytcli queue add <url> [title]
  ytcli queue list
  ytcli queue remove <index>
  ytcli queue pop
  ytcli queue play [--audio-only]
  ytcli queue clear

  ytcli history list
  ytcli history clear

Playback control:
  ytcli status
  ytcli current
  ytcli next
  ytcli prev
  ytcli pause
  ytcli stop

Most data commands print JSON to stdout.`)
}

func printAgentHelp() {
	fmt.Println(`ytcli agent commands

Search:
  ytcli search --limit 5 --max-duration 600 <query>
  ytcli play-search --audio-only --max-duration 600 <query>

Play:
  ytcli play --audio-only <url>
  ytcli status
  ytcli pause
  ytcli stop

Queue:
  ytcli queue add <url> [title]
  ytcli queue play --audio-only
  ytcli queue pop
  ytcli queue list

Playlist:
  ytcli playlist names
  ytcli playlist create <name>
  ytcli playlist add <name> <url> [title]
  ytcli playlist play <name> [index] --audio-only
  ytcli playlist list <name>

Data commands return JSON. Prefer play-search for a one-command search-and-play workflow.`)
}

func runTUI(query string) {
	p := player.New()
	if err := p.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting mpv player: %v\n", err)
		os.Exit(1)
	}
	defer p.Disconnect()

	model := app.NewModel(query, p)
	prog := tea.NewProgram(model)
	if _, err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

// --- search ---

func cmdSearch() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: ytcli search [--limit n] [--min-duration sec] [--max-duration sec] <query>")
		os.Exit(1)
	}

	opts, err := parseSearchArgs(os.Args[2:])
	if err != nil {
		exitError(err)
	}
	results, err := searchFiltered(opts)
	if err != nil {
		exitError(err)
	}

	outputJSON(results)
}

// --- play ---

func cmdPlay() {
	if len(os.Args) < 3 {
		// No URL provided: try to connect to existing mpv and toggle pause
		p := player.New()
		if err := p.Connect(); err != nil {
			fmt.Fprintln(os.Stderr, "Usage: ytcli play <url> [--audio-only|--audio|--video]")
			os.Exit(1)
		}
		if err := p.SendCommand("cycle", "pause"); err != nil {
			exitError(err)
		}
		title, _ := p.SendCommandWithResult("get_property", "media-title")
		paused, _ := p.SendCommandWithResult("get_property", "pause")
		outputJSON(map[string]interface{}{
			"ok":     true,
			"action": "toggle-pause",
			"title":  parseStringResult(title),
			"paused": parseBoolResult(paused),
		})
		return
	}

	opts, err := parsePlayArgs(os.Args[2:])
	if err != nil {
		exitError(err)
	}
	if opts.URL == "" {
		fmt.Fprintln(os.Stderr, "Usage: ytcli play <url> [--audio-only|--audio|--video]")
		os.Exit(1)
	}

	opts.URL = normalizeURL(opts.URL)
	playItem := youtube.Item{Title: opts.URL, URL: opts.URL, ID: extractID(opts.URL)}
	if err := playAndReport(playItem, opts.AudioOnly, "play"); err != nil {
		exitError(err)
	}
}

func cmdPlaySearch() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: ytcli play-search [--audio-only] [--limit n] [--min-duration sec] [--max-duration sec] <query>")
		os.Exit(1)
	}

	opts, err := parseSearchArgs(os.Args[2:])
	if err != nil {
		exitError(err)
	}
	if opts.Query == "" {
		exitError(errors.New("query is required"))
	}
	results, err := searchFiltered(opts)
	if err != nil {
		exitError(err)
	}
	if len(results) == 0 {
		outputJSON(map[string]interface{}{"ok": false, "error": "no_results", "query": opts.Query})
		os.Exit(1)
	}
	if err := playAndReport(results[0], opts.AudioOnly, "play-search"); err != nil {
		exitError(err)
	}
}

// --- playlist ---

func cmdPlaylist() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: ytcli playlist [names|create <name>|delete <name>|add [name] <url> [title]|list [name]|remove [name] <index>|clear [name]]")
		os.Exit(1)
	}

	sub := os.Args[2]
	switch sub {
	case "names":
		names, err := listPlaylistNames()
		if err != nil {
			exitError(err)
		}
		outputJSON(map[string]interface{}{"playlists": names})

	case "create":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: ytcli playlist create <name>")
			os.Exit(1)
		}
		name := os.Args[3]
		if err := saveNamedPlaylist(name, []youtube.Item{}); err != nil {
			exitError(err)
		}
		outputJSON(map[string]interface{}{"ok": true, "playlist": playlistDisplayName(name), "items": []youtube.Item{}})

	case "delete":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: ytcli playlist delete <name>")
			os.Exit(1)
		}
		name := os.Args[3]
		if isDefaultPlaylist(name) {
			if err := playlist.Save([]youtube.Item{}); err != nil {
				exitError(err)
			}
		} else if err := os.Remove(namedPlaylistPath(name)); err != nil && !os.IsNotExist(err) {
			exitError(err)
		}
		outputJSON(map[string]interface{}{"ok": true, "playlist": playlistDisplayName(name), "deleted": true})

	case "add":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: ytcli playlist add [name] <url> [title]")
			os.Exit(1)
		}
		name, url, title := parseNamedItemArgs(os.Args[3:])
		if url == "" {
			fmt.Fprintln(os.Stderr, "Usage: ytcli playlist add [name] <url> [title]")
			os.Exit(1)
		}
		url = normalizeURL(url)
		item := youtube.Item{
			Title: title,
			URL:   url,
			ID:    extractID(url),
		}
		items := loadNamedPlaylist(name)
		items = append(items, item)
		if err := saveNamedPlaylist(name, items); err != nil {
			exitError(err)
		}
		outputJSON(map[string]interface{}{"ok": true, "playlist": playlistDisplayName(name), "index": len(items) - 1, "item": item})

	case "list":
		name := ""
		if len(os.Args) > 3 {
			name = os.Args[3]
		}
		outputJSON(map[string]interface{}{"playlist": playlistDisplayName(name), "items": loadNamedPlaylist(name)})

	case "remove":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: ytcli playlist remove [name] <index>")
			os.Exit(1)
		}
		name, idxArg := parseNamedIndexArgs(os.Args[3:])
		idx, err := strconv.Atoi(idxArg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: index must be a number")
			os.Exit(1)
		}
		items := loadNamedPlaylist(name)
		if idx < 0 || idx >= len(items) {
			fmt.Fprintf(os.Stderr, "Error: index %d out of range (0-%d)\n", idx, len(items)-1)
			os.Exit(1)
		}
		removed := items[idx]
		items = append(items[:idx], items[idx+1:]...)
		if err := saveNamedPlaylist(name, items); err != nil {
			exitError(err)
		}
		outputJSON(map[string]interface{}{"ok": true, "playlist": playlistDisplayName(name), "removed": removed, "items": items})

	case "clear":
		name := ""
		if len(os.Args) > 3 {
			name = os.Args[3]
		}
		if err := saveNamedPlaylist(name, []youtube.Item{}); err != nil {
			exitError(err)
		}
		outputJSON(map[string]interface{}{"ok": true, "playlist": playlistDisplayName(name), "items": []youtube.Item{}})

	case "play":
		name, idx, audioOnly, err := parsePlaylistPlayArgs(os.Args[3:])
		if err != nil {
			exitError(err)
		}
		items := loadNamedPlaylist(name)
		if len(items) == 0 {
			outputJSON(map[string]interface{}{"ok": false, "error": "empty_playlist", "playlist": playlistDisplayName(name)})
			os.Exit(1)
		}
		if idx < 0 || idx >= len(items) {
			fmt.Fprintf(os.Stderr, "Error: index %d out of range (0-%d)\n", idx, len(items)-1)
			os.Exit(1)
		}
		if err := playPlaylistAndReport(items, idx, audioOnly, "playlist-play"); err != nil {
			exitError(err)
		}

	default:
		fmt.Fprintln(os.Stderr, "Usage: ytcli playlist [names|create|delete|add|list|remove|clear|play]")
		os.Exit(1)
	}
}

// --- history ---

func cmdHistory() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: yt history [list --json|clear]")
		os.Exit(1)
	}

	sub := os.Args[2]
	switch sub {
	case "list":
		items := history.Load()
		outputJSON(items)

	case "clear":
		if err := history.Save([]youtube.Item{}); err != nil {
			exitError(fmt.Errorf("clearing history: %w", err))
		}
		outputJSON(map[string]interface{}{"ok": true, "history": []youtube.Item{}})

	default:
		fmt.Fprintln(os.Stderr, "Usage: yt history [list|clear]")
		os.Exit(1)
	}
}

// --- queue ---

func cmdQueue() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: yt queue [add <url>|list --json|clear]")
		os.Exit(1)
	}

	sub := os.Args[2]
	switch sub {
	case "add":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: yt queue add <url>")
			os.Exit(1)
		}
		url := os.Args[3]
		url = normalizeURL(url)
		title := url
		if len(os.Args) > 4 {
			title = strings.Join(os.Args[4:], " ")
		}
		items := queue.Load()
		item := youtube.Item{
			Title: title,
			URL:   url,
			ID:    extractID(url),
		}
		items = append(items, item)
		queue.Save(items)

		// Append to background mpv if running
		p := player.New()
		if err := p.Connect(); err == nil {
			p.AppendToPlaylist(url)
		}

		outputJSON(map[string]interface{}{"ok": true, "index": len(items) - 1, "item": item})

	case "list":
		items := queue.Load()
		outputJSON(map[string]interface{}{"queue": items})

	case "remove":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: ytcli queue remove <index>")
			os.Exit(1)
		}
		idx, err := strconv.Atoi(os.Args[3])
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: index must be a number")
			os.Exit(1)
		}
		items := queue.Load()
		if idx < 0 || idx >= len(items) {
			fmt.Fprintf(os.Stderr, "Error: index %d out of range (0-%d)\n", idx, len(items)-1)
			os.Exit(1)
		}
		removed := items[idx]
		items = append(items[:idx], items[idx+1:]...)
		queue.Save(items)

		// Remove from background mpv if running
		p := player.New()
		if err := p.Connect(); err == nil {
			p.SendCommand("playlist-remove", idx)
		}

		outputJSON(map[string]interface{}{"ok": true, "removed": removed, "queue": items})

	case "pop":
		items := queue.Load()
		if len(items) == 0 {
			outputJSON(map[string]interface{}{"ok": true, "item": nil, "queue": items})
			return
		}
		item := items[0]
		items = items[1:]
		queue.Save(items)

		// Pop from background mpv if running
		p := player.New()
		if err := p.Connect(); err == nil {
			p.SendCommand("playlist-remove", 0)
		}

		outputJSON(map[string]interface{}{"ok": true, "item": item, "queue": items})

	case "play":
		_, audioOnly := parseAudioFlags(os.Args[3:])
		items := queue.Load()
		if len(items) == 0 {
			outputJSON(map[string]interface{}{"ok": false, "error": "empty_queue", "queue": items})
			os.Exit(1)
		}
		if err := playPlaylistAndReport(items, 0, audioOnly, "queue-play"); err != nil {
			exitError(err)
		}
		// Clear the queue file after loading all items
		queue.Save([]youtube.Item{})

	case "clear":
		queue.Save([]youtube.Item{})

		// Clear background mpv if running
		p := player.New()
		if err := p.Connect(); err == nil {
			p.SendCommand("playlist-clear")
		}

		outputJSON(map[string]interface{}{"ok": true, "queue": []youtube.Item{}})

	default:
		fmt.Fprintln(os.Stderr, "Usage: ytcli queue [add <url>|list|remove <index>|pop|play|clear]")
		os.Exit(1)
	}
}

// --- player control ---

func cmdControl(args ...interface{}) {
	p := player.New()
	if err := p.Connect(); err != nil {
		exitError(errors.New("no running mpv instance found; start playback with 'ytcli play <url>' first"))
	}
	if err := p.SendCommand(args...); err != nil {
		exitError(err)
	}
	// Query current track info for feedback
	title, _ := p.SendCommandWithResult("get_property", "media-title")
	paused, _ := p.SendCommandWithResult("get_property", "pause")
	outputJSON(map[string]interface{}{
		"ok":      true,
		"command": args,
		"title":   parseStringResult(title),
		"paused":  parseBoolResult(paused),
	})
}

func cmdResume() {
	p := player.New()
	if err := p.Connect(); err != nil {
		outputJSON(map[string]interface{}{
			"ok":      false,
			"error":   "mpv_not_running",
			"message": "no running mpv instance found; start playback with 'ytcli play <url>' first",
		})
		os.Exit(1)
	}
	if err := p.SendCommand("set", "pause", "no"); err != nil {
		exitError(err)
	}
	title, _ := p.SendCommandWithResult("get_property", "media-title")
	paused, _ := p.SendCommandWithResult("get_property", "pause")
	outputJSON(map[string]interface{}{
		"ok":     true,
		"action": "resume",
		"title":  parseStringResult(title),
		"paused": parseBoolResult(paused),
	})
}

// --- current ---
func cmdCurrent() {
	current, err := getStatus()
	if err != nil {
		exitError(err)
	}

	state := "Playing"
	if current.Paused {
		state = "Paused"
	}

	fmt.Printf("Title: %s\n", current.Title)
	fmt.Printf("Status: %s\n", state)
	if current.Position >= 0 && current.Duration > 0 {
		fmt.Printf("Position: %s / %s\n", formatTime(current.Position), formatTime(current.Duration))
		progress := (current.Position / current.Duration) * 100
		fmt.Printf("Progress: %.1f%%\n", progress)
	}
}

func cmdStatus() {
	status, err := getStatus()
	if err != nil {
		outputJSON(map[string]interface{}{"ok": false, "running": false, "error": "mpv_not_running", "message": err.Error()})
		os.Exit(1)
	}
	outputJSON(status)
}

func parseStringResult(result string) string {
	var resp map[string]interface{}
	json.Unmarshal([]byte(result), &resp)
	if data, ok := resp["data"].(string); ok {
		return data
	}
	return "Unknown"
}

func parseBoolResult(result string) bool {
	var resp map[string]interface{}
	json.Unmarshal([]byte(result), &resp)
	if data, ok := resp["data"].(bool); ok {
		return data
	}
	return false
}

func parseFloatResult(result string) float64 {
	var resp map[string]interface{}
	json.Unmarshal([]byte(result), &resp)
	if data, ok := resp["data"].(float64); ok {
		return data
	}
	return -1
}

func formatTime(seconds float64) string {
	m := int(seconds) / 60
	s := int(seconds) % 60
	return fmt.Sprintf("%d:%02d", m, s)
}

// --- helpers ---

type playerStatus struct {
	OK       bool    `json:"ok"`
	Running  bool    `json:"running"`
	Title    string  `json:"title"`
	URL      string  `json:"url"`
	Paused   bool    `json:"paused"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	Progress float64 `json:"progress"`
}

func parsePlayArgs(args []string) (playOptions, error) {
	remaining, audioOnly := parseAudioFlags(args)
	opts := playOptions{AudioOnly: audioOnly}
	for _, arg := range remaining {
		switch arg {
		case "--json":
			opts.JSON = true
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown play flag: %s", arg)
			}
			if opts.URL == "" {
				opts.URL = arg
			}
		}
	}
	return opts, nil
}

func parseSearchArgs(args []string) (searchOptions, error) {
	opts := searchOptions{Limit: 20, AudioOnly: true}
	queryParts := []string{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json":
			opts.JSON = true
		case "--audio-only", "--audio":
			opts.AudioOnly = true
		case "--video":
			opts.AudioOnly = false
		case "--limit":
			i++
			if i >= len(args) {
				return opts, errors.New("--limit requires a value")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				return opts, errors.New("--limit must be a positive number")
			}
			opts.Limit = n
		case "--min-duration":
			i++
			if i >= len(args) {
				return opts, errors.New("--min-duration requires seconds")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return opts, errors.New("--min-duration must be a non-negative number")
			}
			opts.MinDuration = n
		case "--max-duration":
			i++
			if i >= len(args) {
				return opts, errors.New("--max-duration requires seconds")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				return opts, errors.New("--max-duration must be a non-negative number")
			}
			opts.MaxDuration = n
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown search flag: %s", arg)
			}
			queryParts = append(queryParts, arg)
		}
	}

	opts.Query = strings.Join(queryParts, " ")
	if opts.Query == "" {
		return opts, errors.New("query is required")
	}
	return opts, nil
}

func searchFiltered(opts searchOptions) ([]youtube.Item, error) {
	results, err := youtube.Search(opts.Query)
	if err != nil {
		return nil, err
	}

	filtered := make([]youtube.Item, 0, len(results))
	for _, item := range results {
		if opts.MinDuration > 0 && item.Duration < opts.MinDuration {
			continue
		}
		if opts.MaxDuration > 0 && item.Duration > opts.MaxDuration {
			continue
		}
		filtered = append(filtered, item)
		if opts.Limit > 0 && len(filtered) >= opts.Limit {
			break
		}
	}
	return filtered, nil
}

func playAndReport(item youtube.Item, audioOnly bool, action string) error {
	p := player.NewWithOptions(player.Options{AudioOnly: audioOnly})
	if err := p.Start(); err != nil {
		return fmt.Errorf("starting mpv: %w", err)
	}
	if err := p.Play(item.URL); err != nil {
		return fmt.Errorf("playing media: %w", err)
	}

	outputJSON(map[string]interface{}{
		"ok":         true,
		"action":     action,
		"audio_only": audioOnly,
		"item":       item,
	})
	return nil
}

func playPlaylistAndReport(items []youtube.Item, startIndex int, audioOnly bool, action string) error {
	p := player.NewWithOptions(player.Options{AudioOnly: audioOnly})
	if err := p.Start(); err != nil {
		return fmt.Errorf("starting mpv: %w", err)
	}

	urls := make([]string, len(items))
	for i, item := range items {
		urls[i] = item.URL
	}

	if err := p.PlayList(urls, startIndex); err != nil {
		return fmt.Errorf("playing playlist: %w", err)
	}

	outputJSON(map[string]interface{}{
		"ok":         true,
		"action":     action,
		"audio_only": audioOnly,
		"items":      items,
		"index":      startIndex,
	})
	return nil
}

func lookupTitle(url string) string {
	id := extractID(url)
	if id == "" {
		return ""
	}

	for _, item := range queue.Load() {
		if item.ID == id || item.URL == url {
			return item.Title
		}
	}

	for _, item := range playlist.Load() {
		if item.ID == id || item.URL == url {
			return item.Title
		}
	}

	for _, item := range history.Load() {
		if item.ID == id || item.URL == url {
			return item.Title
		}
	}

	names, _ := listPlaylistNames()
	for _, name := range names {
		for _, item := range loadNamedPlaylist(name) {
			if item.ID == id || item.URL == url {
				return item.Title
			}
		}
	}

	return ""
}

func getStatus() (playerStatus, error) {
	p := player.New()
	if err := p.Connect(); err != nil {
		return playerStatus{}, err
	}

	titleResult, err := p.SendCommandWithResult("get_property", "media-title")
	if err != nil {
		return playerStatus{}, err
	}
	pauseResult, err := p.SendCommandWithResult("get_property", "pause")
	if err != nil {
		return playerStatus{}, err
	}
	durationResult, err := p.SendCommandWithResult("get_property", "duration")
	if err != nil {
		return playerStatus{}, err
	}
	positionResult, err := p.SendCommandWithResult("get_property", "time-pos")
	if err != nil {
		return playerStatus{}, err
	}
	pathResult, _ := p.SendCommandWithResult("get_property", "path")

	duration := parseFloatResult(durationResult)
	position := parseFloatResult(positionResult)
	progress := 0.0
	if duration > 0 && position >= 0 {
		progress = (position / duration) * 100
	}

	title := parseStringResult(titleResult)
	path := parseStringResult(pathResult)
	if path != "" && path != "Unknown" {
		if cleanT := lookupTitle(path); cleanT != "" {
			title = cleanT
		}
	}

	if strings.Contains(title, "clen=") || strings.Contains(title, "lmt=") || strings.Contains(title, "&sig=") || strings.Contains(title, "videoplayback") {
		title = "YouTube Audio Stream"
	}

	return playerStatus{
		OK:       true,
		Running:  true,
		Title:    title,
		URL:      path,
		Paused:   parseBoolResult(pauseResult),
		Duration: duration,
		Position: position,
		Progress: progress,
	}, nil
}

func parseAudioFlags(args []string) ([]string, bool) {
	filtered := make([]string, 0, len(args))
	audioOnly := true
	for _, arg := range args {
		switch arg {
		case "--audio-only", "--audio":
			audioOnly = true
		case "--video":
			audioOnly = false
		default:
			filtered = append(filtered, arg)
		}
	}
	return filtered, audioOnly
}

func parsePlaylistPlayArgs(args []string) (string, int, bool, error) {
	remaining, audioOnly := parseAudioFlags(args)
	name := ""
	idx := 0
	positional := []string{}
	for _, arg := range remaining {
		if arg == "--json" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return "", 0, audioOnly, fmt.Errorf("unknown playlist play flag: %s", arg)
		}
		positional = append(positional, arg)
	}
	if len(positional) > 0 {
		if n, err := strconv.Atoi(positional[0]); err == nil {
			idx = n
		} else {
			name = positional[0]
		}
	}
	if len(positional) > 1 {
		n, err := strconv.Atoi(positional[1])
		if err != nil {
			return "", 0, audioOnly, errors.New("playlist play index must be a number")
		}
		idx = n
	}
	return name, idx, audioOnly, nil
}

func outputJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func exitError(err error) {
	outputJSON(map[string]interface{}{"ok": false, "error": "command_failed", "message": err.Error()})
	os.Exit(1)
}

func parseNamedItemArgs(args []string) (string, string, string) {
	if len(args) == 0 {
		return "", "", ""
	}
	if looksLikeURL(args[0]) {
		title := args[0]
		if len(args) > 1 {
			title = strings.Join(args[1:], " ")
		}
		return "", args[0], title
	}
	if len(args) < 2 {
		return args[0], "", ""
	}
	title := args[1]
	if len(args) > 2 {
		title = strings.Join(args[2:], " ")
	}
	return args[0], args[1], title
}

func parseNamedIndexArgs(args []string) (string, string) {
	if len(args) == 1 {
		return "", args[0]
	}
	return args[0], args[1]
}

func looksLikeURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || isValidVideoID(s)
}

func isValidVideoID(s string) bool {
	if len(s) != 11 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func normalizeURL(s string) string {
	s = strings.TrimSpace(s)
	if isValidVideoID(s) {
		return "https://youtu.be/" + s
	}
	return s
}

func isDefaultPlaylist(name string) bool {
	return name == "" || name == "default"
}

func playlistDisplayName(name string) string {
	if isDefaultPlaylist(name) {
		return "default"
	}
	return name
}

func loadNamedPlaylist(name string) []youtube.Item {
	if isDefaultPlaylist(name) {
		return playlist.Load()
	}
	return loadJSON(namedPlaylistPath(name))
}

func saveNamedPlaylist(name string, items []youtube.Item) error {
	if isDefaultPlaylist(name) {
		return playlist.Save(items)
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(namedPlaylistPath(name), data, 0644)
}

func namedPlaylistPath(name string) string {
	return filepath.Join(configDir(), "playlists", sanitizeName(name)+".json")
}

func listPlaylistNames() ([]string, error) {
	names := []string{"default"}
	dir := filepath.Join(configDir(), "playlists")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return names, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".json"))
	}
	sort.Strings(names)
	return names, nil
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' || r == '.' {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

func extractID(url string) string {
	// Try common youtube URL formats
	for _, prefix := range []string{
		"https://youtu.be/",
		"http://youtu.be/",
		"https://www.youtube.com/watch?v=",
		"http://www.youtube.com/watch?v=",
	} {
		if strings.HasPrefix(url, prefix) {
			id := strings.TrimPrefix(url, prefix)
			if idx := strings.IndexAny(id, "&?#"); idx != -1 {
				id = id[:idx]
			}
			return id
		}
	}
	return ""
}

func getQueuePath() string {
	return filepath.Join(configDir(), "queue.json")
}

func configDir() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "ytcli")
	os.MkdirAll(filepath.Join(dir, "playlists"), 0755)
	return dir
}

func loadJSON(path string) []youtube.Item {
	data, err := os.ReadFile(path)
	if err != nil {
		return []youtube.Item{}
	}
	var items []youtube.Item
	json.Unmarshal(data, &items)
	return items
}

func saveJSON(path string, items []youtube.Item) {
	data, _ := json.MarshalIndent(items, "", "  ")
	os.WriteFile(path, data, 0644)
}
