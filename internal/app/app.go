package app

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"ytcli/internal/history"
	"ytcli/internal/player"
	"ytcli/internal/playlist"
	"ytcli/internal/youtube"
)

type Model struct {
	items       []youtube.Item
	playlist    []youtube.Item
	queue       []youtube.Item
	history     []youtube.Item
	mode        string // "results" | "playlist" | "queue" | "history"
	playingList string // "results" | "playlist" | "queue" | "history"
	cursor      int
	current     int
	query       string
	loading     bool
	errorMsg    string
	status      string
	player      *player.Player
	loopMode    string
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
		queue:       []youtube.Item{},
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

func playCmd(p *player.Player, item youtube.Item) tea.Cmd {
	return func() tea.Msg {
		// Extract stream URL using yt-dlp as requested
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "yt-dlp", "-f", "bestaudio", "-g", item.URL)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			return playResultMsg{err: fmt.Errorf("stream extraction failed: %v\nstderr: %s", err, stderr.String()), item: item}
		}

		streamURL := strings.TrimSpace(stdout.String())
		if streamURL == "" {
			return playResultMsg{err: fmt.Errorf("yt-dlp returned empty stream URL"), item: item}
		}

		// Pass extracted stream URL to player
		err := p.Play(streamURL)
		return playResultMsg{err: err, item: item}
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		searchCmd(m.query),
		waitForPlayerEvent(m.player),
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
			m.history = append([]youtube.Item{msg.item}, m.history...)
			history.Save(m.history)
		}
		return m, nil

	case playerEventMsg:
		// Listen for next event
		cmd := waitForPlayerEvent(m.player)

		switch msg.ev.Event {
		case "end-file":
			if msg.ev.Reason == "error" {
				m.current = -1
				m.status = ""
				m.errorMsg = "Playback stopped unexpectedly (stream error or mpv crash)."
			} else if msg.ev.Reason == "eof" && m.current >= 0 {
				var list []youtube.Item
				if m.playingList == "playlist" {
					list = m.playlist
				} else if m.playingList == "queue" {
					list = m.queue
				} else if m.playingList == "history" {
					list = m.history
				} else {
					list = m.items
				}

				if m.loopMode == "one" {
					m.errorMsg = ""
					m.status = "Extracting stream (yt-dlp)..."
					return m, tea.Batch(cmd, playCmd(m.player, list[m.current]))
				} else if m.current < len(list)-1 {
					m.current++
					if m.mode == m.playingList {
						m.cursor = m.current
					}
					m.errorMsg = ""
					m.status = "Extracting stream (yt-dlp)..."
					return m, tea.Batch(cmd, playCmd(m.player, list[m.current]))
				} else {
					m.current = -1
					m.status = ""
				}
			} else if msg.ev.Reason == "stop" {
				// We don't clear m.current here because "stop" is usually triggered by loading a new file via n/p or enter.
			}
		case "file-loaded":
			m.status = "Playing"
			m.errorMsg = ""
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
			}
		case "Q":
			m.mode = "queue"
			m.cursor = 0
		case "h":
			if m.mode != "history" {
				m.mode = "history"
				m.cursor = 0
			}
		case "P":
			if m.mode != "playlist" {
				m.mode = "playlist"
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
					if m.current == idx {
						// Optionally stop playing, or let mpv finish.
					} else if m.current > idx {
						m.current-- // shift tracking
					}
				}

				if m.cursor >= len(m.playlist) && m.cursor > 0 {
					m.cursor--
				}
			} else if m.mode == "queue" && len(m.queue) > 0 && m.cursor >= 0 && m.cursor < len(m.queue) {
				idx := m.cursor
				m.queue = append(m.queue[:idx], m.queue[idx+1:]...)

				if m.playingList == "queue" {
					if m.current == idx {
						// Optionally stop playing, or let mpv finish.
					} else if m.current > idx {
						m.current-- // shift tracking
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
				m.status = "Extracting stream (yt-dlp)..."
				return m, playCmd(m.player, list[m.cursor])
			}
		case "n":
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
			if m.current >= -1 && m.current < len(activeList)-1 {
				m.current++
				if m.mode == m.playingList {
					m.cursor = m.current
				}
				m.errorMsg = ""
				m.status = "Extracting stream (yt-dlp)..."
				return m, playCmd(m.player, activeList[m.current])
			}
		case "p":
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
			if m.current > 0 {
				m.current--
				if m.mode == m.playingList {
					m.cursor = m.current
				}
				m.errorMsg = ""
				m.status = "Extracting stream (yt-dlp)..."
				return m, playCmd(m.player, activeList[m.current])
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
	} else {
		list = m.queue
	}

	if len(list) == 0 {
		if m.mode == "results" {
			b.WriteString("(no results)\n")
		} else if m.mode == "playlist" {
			b.WriteString("(empty playlist)\n")
		} else {
			b.WriteString("(empty queue)\n")
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

	if m.current >= 0 {
		var activeList []youtube.Item
		if m.playingList == "playlist" {
			activeList = m.playlist
		} else if m.playingList == "queue" {
			activeList = m.queue
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
