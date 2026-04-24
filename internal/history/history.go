package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"ytcli/internal/youtube"
)

func getPath() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "ytcli")
	os.MkdirAll(dir, 0755)
	return filepath.Join(dir, "history.json")
}

func Load() []youtube.Item {
	data, err := os.ReadFile(getPath())
	if err != nil {
		return []youtube.Item{}
	}
	var items []youtube.Item
	if err := json.Unmarshal(data, &items); err != nil {
		return []youtube.Item{}
	}
	return items
}

func Save(items []youtube.Item) error {
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(getPath(), data, 0644)
}
