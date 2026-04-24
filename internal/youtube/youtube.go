package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"
)

type Item struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	ID          string `json:"id"`
	Duration    int    `json:"duration"`
	DurationStr string `json:"duration_str"`
	Channel     string `json:"channel"`
}

func FormatDuration(sec int) string {
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func Search(query string) ([]Item, error) {
	// Check if yt-dlp is installed
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return nil, fmt.Errorf("yt-dlp is not installed or not in PATH")
	}

	// 10 seconds timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Execute yt-dlp with --flat-playlist to prevent fetching each video's formats,
	// making the search fast enough to complete within the timeout.
	cmd := exec.CommandContext(ctx, "yt-dlp", fmt.Sprintf("ytsearch20:%s", query), "--dump-json", "--flat-playlist")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("search timed out after 10 seconds")
		}
		return nil, fmt.Errorf("yt-dlp failed: %v\nstderr: %s", err, stderr.String())
	}

	var results []Item
	decoder := json.NewDecoder(&stdout)

	for {
		var raw struct {
			ID       string  `json:"id"`
			Title    string  `json:"title"`
			Duration float64 `json:"duration"`
			Uploader string  `json:"uploader"`
			Channel  string  `json:"channel"`
			IsLive   bool    `json:"is_live"`
		}

		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			continue
		}

		if raw.ID == "" || raw.Title == "" || raw.IsLive {
			continue
		}

		// Channel fallback
		channelName := raw.Channel
		if channelName == "" {
			channelName = raw.Uploader
		}

		item := Item{
			Title:       raw.Title,
			URL:         fmt.Sprintf("https://youtu.be/%s", raw.ID),
			ID:          raw.ID,
			Duration:    int(raw.Duration),
			DurationStr: FormatDuration(int(raw.Duration)),
			Channel:     channelName,
		}

		results = append(results, item)

		// Max 20 results as required
		if len(results) >= 20 {
			break
		}
	}

	return results, nil
}
