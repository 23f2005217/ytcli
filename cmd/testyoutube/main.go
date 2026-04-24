package main

import (
	"encoding/json"
	"fmt"
	"os"
	"ytcli/internal/youtube"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <query>")
		os.Exit(1)
	}
	query := os.Args[1]

	results, err := youtube.Search(query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(results); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
