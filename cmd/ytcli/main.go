package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"ytcli/internal/app"
	"ytcli/internal/history"
	"ytcli/internal/player"
	"ytcli/internal/playlist"
	"ytcli/internal/youtube"
)

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
	case "stop":
		cmdControl("stop")
	case "current":
		cmdCurrent()
	default:
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
  ytcli play [--audio-only] <url>       Play a URL with mpv

Agent-friendly commands:
  ytcli playlist names
  ytcli playlist create <name>
  ytcli playlist delete <name>
  ytcli playlist add [name] <url> [title]
  ytcli playlist list [name]
  ytcli playlist remove [name] <index>
  ytcli playlist clear [name]

  ytcli queue add <url> [title]
  ytcli queue list
  ytcli queue remove <index>
  ytcli queue pop
  ytcli queue clear

  ytcli history list
  ytcli history clear

Playback control:
  ytcli current
  ytcli next
  ytcli prev
  ytcli pause
  ytcli stop

Most data commands print JSON to stdout.`)
}

func runTUI(query string) {
	p := player.New()
	if err := p.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting mpv player: %v\n", err)
		os.Exit(1)
	}
	defer p.Quit()

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
		fmt.Fprintln(os.Stderr, "Usage: yt search \"query\" [--json]")
		os.Exit(1)
	}

	query := os.Args[2]
	results, err := youtube.Search(query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	outputJSON(results)
}

// --- play ---

func cmdPlay() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: ytcli play <url> [--audio-only|--audio|--video]")
		os.Exit(1)
	}

	url, audioOnly := parsePlayArgs(os.Args[2:])
	if url == "" {
		fmt.Fprintln(os.Stderr, "Usage: ytcli play <url> [--audio-only|--audio|--video]")
		os.Exit(1)
	}

	p := player.NewWithOptions(player.Options{AudioOnly: audioOnly})
	if err := p.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting mpv: %v\n", err)
		os.Exit(1)
	}

	if err := p.Play(url); err != nil {
		fmt.Fprintf(os.Stderr, "Error playing: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Playing: %s\n", url)
}

func parsePlayArgs(args []string) (string, bool) {
	audioOnly := false
	url := ""

	for _, arg := range args {
		switch arg {
		case "--audio-only", "--audio":
			audioOnly = true
		case "--video":
			audioOnly = false
		default:
			if url == "" {
				url = arg
			}
		}
	}

	return url, audioOnly
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

	default:
		fmt.Fprintln(os.Stderr, "Usage: ytcli playlist [names|create|delete|add|list|remove|clear]")
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
			fmt.Fprintf(os.Stderr, "Error clearing history: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "History cleared.")

	default:
		fmt.Fprintln(os.Stderr, "Usage: yt history [list|clear]")
		os.Exit(1)
	}
}

// --- queue ---

func cmdQueue() {
	// Queue is in-memory for TUI, but we can expose basic file-based queue for CLI
	queuePath := getQueuePath()

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
		title := url
		if len(os.Args) > 4 {
			title = strings.Join(os.Args[4:], " ")
		}
		items := loadJSON(queuePath)
		item := youtube.Item{
			Title: title,
			URL:   url,
			ID:    extractID(url),
		}
		items = append(items, item)
		saveJSON(queuePath, items)
		outputJSON(map[string]interface{}{"ok": true, "index": len(items) - 1, "item": item})

	case "list":
		items := loadJSON(queuePath)
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
		items := loadJSON(queuePath)
		if idx < 0 || idx >= len(items) {
			fmt.Fprintf(os.Stderr, "Error: index %d out of range (0-%d)\n", idx, len(items)-1)
			os.Exit(1)
		}
		removed := items[idx]
		items = append(items[:idx], items[idx+1:]...)
		saveJSON(queuePath, items)
		outputJSON(map[string]interface{}{"ok": true, "removed": removed, "queue": items})

	case "pop":
		items := loadJSON(queuePath)
		if len(items) == 0 {
			outputJSON(map[string]interface{}{"ok": true, "item": nil, "queue": items})
			return
		}
		item := items[0]
		items = items[1:]
		saveJSON(queuePath, items)
		outputJSON(map[string]interface{}{"ok": true, "item": item, "queue": items})

	case "clear":
		saveJSON(queuePath, []youtube.Item{})
		outputJSON(map[string]interface{}{"ok": true, "queue": []youtube.Item{}})

	default:
		fmt.Fprintln(os.Stderr, "Usage: ytcli queue [add <url>|list|remove <index>|pop|clear]")
		os.Exit(1)
	}
}

// --- player control ---

func cmdControl(args ...interface{}) {
	p := player.New()
	if err := p.Connect(); err != nil {
		fmt.Fprintln(os.Stderr, "Error: no running mpv instance found. Start the TUI first or use 'yt play <url>'.")
		os.Exit(1)
	}
	if err := p.SendCommand(args...); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// --- current ---
func cmdCurrent() {
	p := player.New()
	if err := p.Connect(); err != nil {
		fmt.Fprintln(os.Stderr, "Error: no running mpv instance found. Start the TUI first or use 'ytcli play <url>'.")
		os.Exit(1)
	}

	// Get media title
	titleCmd := []interface{}{"get_property", "media-title"}
	titleResult, err := p.SendCommandWithResult(titleCmd...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting media title: %v\n", err)
		os.Exit(1)
	}

	// Get pause state
	pauseCmd := []interface{}{"get_property", "pause"}
	pauseResult, err := p.SendCommandWithResult(pauseCmd...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting pause state: %v\n", err)
		os.Exit(1)
	}

	// Get duration
	durationCmd := []interface{}{"get_property", "duration"}
	durationResult, err := p.SendCommandWithResult(durationCmd...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting duration: %v\n", err)
		os.Exit(1)
	}

	// Get position
	positionCmd := []interface{}{"get_property", "time-pos"}
	positionResult, err := p.SendCommandWithResult(positionCmd...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting position: %v\n", err)
		os.Exit(1)
	}

	// Parse results
	title := parseStringResult(titleResult)
	paused := parseBoolResult(pauseResult)
	duration := parseFloatResult(durationResult)
	position := parseFloatResult(positionResult)

	status := "Playing"
	if paused {
		status = "Paused"
	}

	fmt.Printf("Title: %s\n", title)
	fmt.Printf("Status: %s\n", status)
	if position >= 0 && duration > 0 {
		fmt.Printf("Position: %s / %s\n", formatTime(position), formatTime(duration))
		progress := (position / duration) * 100
		fmt.Printf("Progress: %.1f%%\n", progress)
	}
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

func outputJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func exitError(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
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
