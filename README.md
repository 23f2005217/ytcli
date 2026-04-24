# ytcli

A YouTube command-line media player with an interactive Terminal User Interface (TUI) written in Go.

## Features

- **YouTube Search**: Search videos directly from terminal using yt-dlp
- **Interactive TUI**: Browse and select videos with a clean interface powered by Bubble Tea
- **Playlist Management**: Create and manage playlists with JSON storage
- **Watch History**: Automatic tracking of watched videos
- **Queue System**: Add videos to a playback queue
- **Playback Controls**: Play, pause, stop, next, previous with MPV integration
- **Loop Modes**: None, single track, or all tracks

## Installation

### Prerequisites

- [yt-dlp](https://github.com/yt-dlp/yt-dlp) - YouTube video/audio stream extraction
- [MPV](https://mpv.io/) - Media player for playback

Install on Arch Linux:
```bash
sudo pacman -S yt-dlp mpv
```

### Build from Source

```bash
git clone https://github.com/username/ytcli.git
cd ytcli
go build -o ytcli ./cmd/ytcli
```

## Usage

### Launch TUI

```bash
ytcli                    # Launch with default search
ytcli "search query"     # Launch with specific search
```

### CLI Commands

```bash
ytcli search "query"          # Search and output JSON results
ytcli play <url>              # Play a specific URL
ytcli playlist add <url>      # Add to playlist
ytcli playlist list           # List playlist
ytcli playlist remove <url>   # Remove from playlist
ytcli history list            # View watch history
ytcli history clear           # Clear history
ytcli queue add <url>         # Add to queue
ytcli queue list              # List queue
ytcli next                    # Next track
ytcli prev                    # Previous track
ytcli pause                   # Pause playback
ytcli stop                    # Stop playback
```

### TUI Keybindings

| Key | Action |
|-----|--------|
| `j` / `↓` | Navigate down |
| `k` / `↑` | Navigate up |
| `enter` | Play selected item |
| `n` / `p` | Next / Previous track |
| `q` | Add to queue |
| `Q` | View queue |
| `P` | View playlist |
| `h` | View history |
| `a` / `A` | Add to playlist |
| `d` | Delete from playlist/queue/history |
| `esc` | Back to search |
| `ctrl+c` | Quit |

## Configuration

Data is stored in `~/.config/ytcli/`:
- `playlist.json` - Playlist data
- `history.json` - Watch history
- `queue.json` - Queue data

MPV socket: `/tmp/mpv-socket`

## Tech Stack

- **Go 1.26.2**
- **Bubble Tea** - TUI framework
- **Lipgloss** - Terminal styling
- **yt-dlp** - YouTube API
- **MPV** - Media playback

## Project Structure

```
ytcli/
├── cmd/
│   └── ytcli/main.go        # Application entry point
├── internal/
│   ├── app/app.go           # TUI application logic
│   ├── player/player.go     # MPV player control
│   ├── youtube/youtube.go   # YouTube search
│   ├── playlist/playlist.go # Playlist management
│   └── history/history.go   # History tracking
├── go.mod
└── go.sum
```

## License

MIT License (or specify your license)

## Contributing

Contributions are welcome! Feel free to open issues or submit pull requests.
