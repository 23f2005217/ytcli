package app

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"ytcli/internal/history"
	"ytcli/internal/player"
	"ytcli/internal/playlist"
	"ytcli/internal/queue"
	"ytcli/internal/youtube"
)

type Model struct {
	items           []youtube.Item
	playlist        []youtube.Item
	queue           []youtube.Item
	history         []youtube.Item
	mode            string // "results" | "playlist" | "queue" | "history"
	playingList     string // "results" | "playlist" | "queue" | "history"
	cursor          int
	current         int
	query           string
	loading         bool
	errorMsg        string
	status          string
	player          *player.Player
	loopMode        string
	nowPlayingTitle string
}

func NewModel(query string, p *player.Player) Model {
	return Model{
		query:       query,
		loading:     true,
		player:      p,
		current:     -1,
		loopMode:    "none",
		mode:        "results",
		playingList: "results",
		playlist:    playlist.Load(),
		queue:       queue.Load(),
		history:     history.Load(),
	}
}

func getCurrentPlayingItem(m Model) (youtube.Item, bool) {
	if m.current >= 0 {
		if m.playingList == "playlist" && m.current < len(m.playlist) {
			return m.playlist[m.current], true
		} else if m.playingList == "queue" && m.current < len(m.queue) {
			return m.queue[m.current], true
		} else if m.playingList == "history" && m.current < len(m.history) {
			return m.history[m.current], true
		} else if m.playingList == "results" && m.current < len(m.items) {
			return m.items[m.current], true
		}
	}
	return youtube.Item{}, false
}

type searchResultMsg struct {
	items []youtube.Item
	err   error
}

type playResultMsg struct {
	err  error
	item youtube.Item
}

type playerEventMsg struct {
	ev player.Event
}

func waitForPlayerEvent(p *player.Player) tea.Cmd {
	return func() tea.Msg {
		ev := <-p.EventCh
		return playerEventMsg{ev}
	}
}

func searchCmd(query string) tea.Cmd {
	return func() tea.Msg {
		items, err := youtube.Search(query)
		return searchResultMsg{items: items, err: err}
	}
}

func playListCmd(p *player.Player, items []youtube.Item, startIndex int) tea.Cmd {
	return func() tea.Msg {
		if len(items) == 0 || startIndex < 0 || startIndex >= len(items) {
			return playResultMsg{err: fmt.Errorf("invalid play index")}
		}

		urls := make([]string, len(items))
		for i, item := range items {
			urls[i] = item.URL
		}

		err := p.PlayList(urls, startIndex)
		return playResultMsg{err: err, item: items[startIndex]}
	}
}

type statusResultMsg struct {
	title       string
	path        string
	playlistPos int
	paused      bool
	err         error
}

func queryPlayerStatusCmd(p *player.Player) tea.Cmd {
	return func() tea.Msg {
		titleResult, err := p.SendCommandWithResult("get_property", "media-title")
		if err != nil {
			return statusResultMsg{err: err}
		}
		pathResult, _ := p.SendCommandWithResult("get_property", "path")
		posResult, _ := p.SendCommandWithResult("get_property", "playlist-pos")
		pauseResult, _ := p.SendCommandWithResult("get_property", "pause")

		var title string
		var resp map[string]interface{}
		if json.Unmarshal([]byte(titleResult), &resp) == nil {
			if t, ok := resp["data"].(string); ok {
				title = t
			}
		}

		var path string
		if json.Unmarshal([]byte(pathResult), &resp) == nil {
			if pUrl, ok := resp["data"].(string); ok {
				path = pUrl
			}
		}

		playlistPos := -1
		if json.Unmarshal([]byte(posResult), &resp) == nil {
			if val, ok := resp["data"].(float64); ok {
				playlistPos = int(val)
			}
		}

		paused := false
		if json.Unmarshal([]byte(pauseResult), &resp) == nil {
			if pz, ok := resp["data"].(bool); ok {
				paused = pz
			}
		}

		return statusResultMsg{title: title, path: path, playlistPos: playlistPos, paused: paused}
	}
}

func findItemIndexByURL(items []youtube.Item, url string) int {
	for i, item := range items {
		if item.URL == url {
			return i
		}
	}
	return -1
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		searchCmd(m.query),
		waitForPlayerEvent(m.player),
		queryPlayerStatusCmd(m.player),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case searchResultMsg:
		m.loading = false
		if msg.err != nil {
			m.errorMsg = fmt.Sprintf("Search failed: %v", msg.err)
		}
		m.items = msg.items
		m.cursor = 0
		return m, nil

	case playResultMsg:
		if msg.err != nil {
			m.errorMsg = msg.err.Error()
			m.current = -1
			m.status = ""
		} else {
			m.status = "Playing"
			m.errorMsg = ""
			if msg.item.URL != "" {
				m.history = append([]youtube.Item{msg.item}, m.history...)
				history.Save(m.history)
			}
		}
		return m, queryPlayerStatusCmd(m.player)

	case statusResultMsg:
		if msg.err != nil {
			return m, nil
		}
		m.nowPlayingTitle = msg.title
		if msg.paused {
			m.status = "Paused"
		} else {
			m.status = "Playing"
		}

		if msg.path != "" {
			if idx := findItemIndexByURL(m.queue, msg.path); idx >= 0 {
				m.playingList = "queue"
				m.current = idx
			} else if idx := findItemIndexByURL(m.playlist, msg.path); idx >= 0 {
				m.playingList = "playlist"
				m.current = idx
			} else if idx := findItemIndexByURL(m.items, msg.path); idx >= 0 {
				m.playingList = "results"
				m.current = idx
			} else if idx := findItemIndexByURL(m.history, msg.path); idx >= 0 {
				m.playingList = "history"
				m.current = idx
			}
		}

		if m.mode == m.playingList && m.current >= 0 {
			m.cursor = m.current
		}
		return m, nil

	case playerEventMsg:
		cmd := waitForPlayerEvent(m.player)

		switch msg.ev.Event {
		case "end-file":
			if msg.ev.Reason == "error" {
				m.current = -1
				m.status = ""
				m.errorMsg = "Playback stopped unexpectedly (stream error or mpv crash)."
			} else if msg.ev.Reason == "eof" {
				// mpv auto-advances. The file-loaded event will handle updating the index and title.
			}
		case "file-loaded":
			m.status = "Playing"
			m.errorMsg = ""
			return m, tea.Batch(cmd, queryPlayerStatusCmd(m.player))
		}

		return m, cmd

	case tea.KeyMsg:
		var list []youtube.Item
		if m.mode == "playlist" {
			list = m.playlist
		} else if m.mode == "queue" {
			list = m.queue
		} else if m.mode == "history" {
			list = m.history
		} else {
			list = m.items
		}

		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(list)-1 {
				m.cursor++
			}
		case "g":
			if len(list) > 0 {
				m.cursor = 0
			}
		case "G":
			if len(list) > 0 {
				m.cursor = len(list) - 1
			}
		case "q":
			if len(list) > 0 && m.cursor >= 0 && m.cursor < len(list) {
				m.queue = append(m.queue, list[m.cursor])
				queue.Save(m.queue)
				m.player.AppendToPlaylist(list[m.cursor].URL)
			}
		case "Q":
			m.mode = "queue"
			m.queue = queue.Load()
			m.cursor = 0
		case "h":
			if m.mode != "history" {
				m.mode = "history"
				m.history = history.Load()
				m.cursor = 0
			}
		case "P":
			if m.mode != "playlist" {
				m.mode = "playlist"
				m.playlist = playlist.Load()
				m.cursor = 0
			}
		case "esc":
			if m.mode != "results" {
				m.mode = "results"
				m.cursor = 0
			}
		case "a":
			if m.mode == "results" {
				if item, ok := getCurrentPlayingItem(m); ok {
					m.playlist = append(m.playlist, item)
					playlist.Save(m.playlist)
				}
			}
		case "A":
			if m.mode == "results" && len(m.items) > 0 && m.cursor >= 0 && m.cursor < len(m.items) {
				m.playlist = append(m.playlist, m.items[m.cursor])
				playlist.Save(m.playlist)
			}
		case "d":
			if m.mode == "playlist" && len(m.playlist) > 0 && m.cursor >= 0 && m.cursor < len(m.playlist) {
				idx := m.cursor
				m.playlist = append(m.playlist[:idx], m.playlist[idx+1:]...)
				playlist.Save(m.playlist)

				if m.playingList == "playlist" {
					m.player.SendCommand("playlist-remove", idx)
					if m.current == idx {
					} else if m.current > idx {
						m.current--
					}
				}

				if m.cursor >= len(m.playlist) && m.cursor > 0 {
					m.cursor--
				}
			} else if m.mode == "queue" && len(m.queue) > 0 && m.cursor >= 0 && m.cursor < len(m.queue) {
				idx := m.cursor
				m.queue = append(m.queue[:idx], m.queue[idx+1:]...)
				queue.Save(m.queue)

				if m.playingList == "queue" {
					m.player.SendCommand("playlist-remove", idx)
					if m.current == idx {
					} else if m.current > idx {
						m.current--
					}
				}

				if m.cursor >= len(m.queue) && m.cursor > 0 {
					m.cursor--
				}
			} else if m.mode == "history" && len(m.history) > 0 && m.cursor >= 0 && m.cursor < len(m.history) {
				idx := m.cursor
				m.history = append(m.history[:idx], m.history[idx+1:]...)
				history.Save(m.history)

				if m.playingList == "history" {
					m.player.SendCommand("playlist-remove", idx)
					if m.current == idx {
					} else if m.current > idx {
						m.current--
					}
				}

				if m.cursor >= len(m.history) && m.cursor > 0 {
					m.cursor--
				}
			}
		case "enter":
			if len(list) > 0 && m.cursor >= 0 && m.cursor < len(list) {
				m.current = m.cursor
				m.playingList = m.mode
				m.errorMsg = ""
				m.status = "Playing"
				return m, playListCmd(m.player, list, m.cursor)
			}
		case "n":
			m.errorMsg = ""
			return m, func() tea.Msg {
				err := m.player.Next()
				return playResultMsg{err: err}
			}
		case "p":
			m.errorMsg = ""
			return m, func() tea.Msg {
				err := m.player.Previous()
				return playResultMsg{err: err}
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	var b strings.Builder

	if m.mode == "results" {
		b.WriteString(fmt.Sprintf("query: %s\n\n", m.query))
	} else if m.mode == "playlist" {
		b.WriteString("playlist:\n\n")
	} else if m.mode == "queue" {
		b.WriteString("queue:\n\n")
	} else if m.mode == "history" {
		b.WriteString("history:\n\n")
	}

	if m.loading && m.mode == "results" {
		b.WriteString("Loading...\n")
		return b.String()
	}

	if m.errorMsg != "" {
		b.WriteString(fmt.Sprintf("⚠️  Error: %s\n\n", m.errorMsg))
	}

	var list []youtube.Item
	if m.mode == "results" {
		b.WriteString("results:\n\n")
		list = m.items
	} else if m.mode == "playlist" {
		list = m.playlist
	} else if m.mode == "queue" {
		list = m.queue
	} else {
		list = m.history
	}

	if len(list) == 0 {
		if m.mode == "results" {
			b.WriteString("(no results)\n")
		} else if m.mode == "playlist" {
			b.WriteString("(empty playlist)\n")
		} else if m.mode == "queue" {
			b.WriteString("(empty queue)\n")
		} else {
			b.WriteString("(empty history)\n")
		}
	} else {
		for i, item := range list {
			cursor := " "
			if m.cursor == i {
				cursor = ">"
			}
			
			title := item.Title
			if len(title) > 60 {
				title = title[:57] + "..."
			}
			
			b.WriteString(fmt.Sprintf("%s %s [%s]\n", cursor, title, item.DurationStr))
		}
	}

	b.WriteString("\n")

	if m.nowPlayingTitle != "" {
		playingTitle := m.nowPlayingTitle
		if len(playingTitle) > 60 {
			playingTitle = playingTitle[:57] + "..."
		}

		statusPrefix := m.status
		if statusPrefix == "" {
			statusPrefix = "Playing"
		}

		b.WriteString(fmt.Sprintf("▶ %s: %s\n", statusPrefix, playingTitle))
	} else if m.current >= 0 {
		var activeList []youtube.Item
		if m.playingList == "playlist" {
			activeList = m.playlist
		} else if m.playingList == "queue" {
			activeList = m.queue
		} else if m.playingList == "history" {
			activeList = m.history
		} else {
			activeList = m.items
		}

		if m.current < len(activeList) {
			playingTitle := activeList[m.current].Title
			if len(playingTitle) > 60 {
				playingTitle = playingTitle[:57] + "..."
			}
			
			statusPrefix := m.status
			if statusPrefix == "" {
				statusPrefix = "Selected"
			}
			
			b.WriteString(fmt.Sprintf("▶ %s: %s\n", statusPrefix, playingTitle))
		}
	}

	if m.mode == "results" {
		b.WriteString("\n(j/k: move, enter: play, n/p: next/prev, q: queue, Q: view queue, P: playlist, A: add selected, a: add playing, ctrl+c: quit)\n")
	} else {
		b.WriteString("\n(j/k: move, enter: play, n/p: next/prev, d: delete, esc: back to results, ctrl+c: quit)\n")
	}
	return b.String()
}
