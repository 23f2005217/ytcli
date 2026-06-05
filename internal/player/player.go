package player

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Player struct {
	socketPath string
	conn       net.Conn
	cmd        *exec.Cmd
	mu         sync.Mutex
	EventCh    chan Event
	audioOnly  bool
}

type Event struct {
	Event  string `json:"event"`
	Reason string `json:"reason,omitempty"`
}

type Options struct {
	AudioOnly bool
}

func New() *Player {
	return NewWithOptions(Options{AudioOnly: true})
}

func NewWithOptions(opts Options) *Player {
	return &Player{
		socketPath: "/tmp/mpv-socket",
		EventCh:    make(chan Event, 100),
		audioOnly:  opts.AudioOnly,
	}
}

func (p *Player) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if already running via our process tracker
	if p.cmd != nil && p.cmd.Process != nil {
		if err := p.cmd.Process.Signal(syscall.Signal(0)); err == nil {
			return nil
		}
	}

	// Try to connect to an existing socket (mpv might be running from a previous run)
	if err := p.connect(); err == nil {
		return nil
	}

	// Kill any stale mpv process started by ytcli
	exec.Command("pkill", "-f", "mpv.*--input-ipc-server="+p.socketPath).Run()

	// Remove old socket if it exists and is stale
	os.Remove(p.socketPath)

	args := []string{
		"--idle=yes",
		"--input-ipc-server=" + p.socketPath,
		"--no-terminal",
	}
	if p.audioOnly {
		args = append(args, "--no-video")
	}

	p.cmd = exec.Command("mpv", args...)

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start mpv: %w", err)
	}

	// Wait for socket to be created
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(p.socketPath); err == nil {
			break
		}
		if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
			return fmt.Errorf("mpv exited before creating IPC socket")
		}
		time.Sleep(100 * time.Millisecond)
	}

	return p.connect()
}

// Connect attaches to an already-running mpv instance via its IPC socket.
// Unlike Start(), it will never spawn a new mpv process.
func (p *Player) Connect() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connect()
}

func (p *Player) connect() error {
	conn, err := net.Dial("unix", p.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to mpv socket: %w", err)
	}
	p.conn = conn

	go p.listenEvents(conn)

	return nil
}

func (p *Player) listenEvents(conn net.Conn) {
	decoder := json.NewDecoder(conn)
	for {
		var event Event
		if err := decoder.Decode(&event); err != nil {
			break
		}
		if event.Event != "" {
			p.EventCh <- event
		}
	}
}

func (p *Player) SendCommand(args ...interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn == nil {
		return errors.New("not connected to mpv")
	}

	cmd := map[string]interface{}{
		"command": args,
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	_, err = p.conn.Write(data)
	if err != nil {
		// Try to reconnect once if disconnected
		if err := p.connect(); err == nil {
			_, err = p.conn.Write(data)
		}
		if err != nil {
			return fmt.Errorf("failed to send command: %w", err)
		}
	}

	return nil
}

func (p *Player) Play(url string) error {
	if p.audioOnly {
		if err := p.SendCommand("set", "vid", "no"); err != nil {
			return err
		}
	} else if err := p.SendCommand("set", "vid", "auto"); err != nil {
		return err
	}
	return p.SendCommand("loadfile", url, "replace")
}

func (p *Player) PlayList(urls []string, startIndex int) error {
	if p.audioOnly {
		if err := p.SendCommand("set", "vid", "no"); err != nil {
			return err
		}
	} else if err := p.SendCommand("set", "vid", "auto"); err != nil {
		return err
	}

	if err := p.SendCommand("playlist-clear"); err != nil {
		return err
	}
	for _, url := range urls {
		if err := p.SendCommand("loadfile", url, "append"); err != nil {
			return err
		}
	}
	return p.SendCommand("set", "playlist-pos", startIndex)
}

func (p *Player) AppendToPlaylist(url string) error {
	return p.SendCommand("loadfile", url, "append-play")
}

func (p *Player) TogglePause() error {
	return p.SendCommand("cycle", "pause")
}

func (p *Player) Seek(seconds int) error {
	return p.SendCommand("seek", seconds)
}

func (p *Player) SeekAbsolute(seconds int) error {
	return p.SendCommand("seek", seconds, "absolute")
}

func (p *Player) Next() error {
	return p.SendCommand("playlist-next")
}

func (p *Player) Previous() error {
	return p.SendCommand("playlist-prev")
}

func (p *Player) Disconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		p.conn.Close()
		p.conn = nil
	}
}

func (p *Player) Stop() error {
	return p.SendCommand("stop")
}

func (p *Player) Quit() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn != nil {
		cmd := map[string]interface{}{
			"command": []string{"quit"},
		}
		data, _ := json.Marshal(cmd)
		data = append(data, '\n')
		p.conn.Write(data)
		p.conn.Close()
		p.conn = nil
	}

	if p.cmd != nil && p.cmd.Process != nil {
		p.cmd.Process.Kill()
		p.cmd.Wait()
		p.cmd = nil
	}

	os.Remove(p.socketPath)
	return nil
}

func (p *Player) SetLoop(mode string) error {
	// loopMode: none, one, all
	switch mode {
	case "none":
		if err := p.SendCommand("set", "loop-file", "no"); err != nil {
			return err
		}
		return p.SendCommand("set", "loop-playlist", "no")
	case "one":
		return p.SendCommand("set", "loop-file", "inf")
	case "all":
		if err := p.SendCommand("set", "loop-file", "no"); err != nil {
			return err
		}
		return p.SendCommand("set", "loop-playlist", "inf")
	default:
		return fmt.Errorf("unknown loop mode: %s", mode)
	}
}

func (p *Player) SendCommandWithResult(args ...interface{}) (string, error) {
	conn, err := net.Dial("unix", p.socketPath)
	if err != nil {
		return "", fmt.Errorf("failed to connect to mpv socket: %w", err)
	}
	defer conn.Close()

	cmd := map[string]interface{}{
		"command": args,
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return "", err
	}
	data = append(data, '\n')

	_, err = conn.Write(data)
	if err != nil {
		return "", fmt.Errorf("failed to send command: %w", err)
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(buf[:n]), nil
}
