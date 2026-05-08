package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/embed"
)

// resolveEmbedder builds an Embedder from config + MEMGRAPH_EMBED_KEY env var.
// Returns nil if no API key is available (BM25-only mode).
func resolveEmbedder(cfg config.EmbedConfig) embed.Embedder {
	key := cfg.APIKey
	if envKey := os.Getenv("MEMGRAPH_EMBED_KEY"); envKey != "" {
		key = envKey
	}
	if key == "" {
		return nil
	}
	provider := cfg.Provider
	if provider == "" {
		provider = "openai"
	}
	switch provider {
	case "google":
		return embed.NewGoogle(key, cfg.BaseURL)
	default:
		return embed.NewOpenAI(key, cfg.BaseURL)
	}
}

// formatSize returns a human-readable file size string.
func formatSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/gb)
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/mb)
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/kb)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// parseTags parses a JSON-encoded []string from the notes.tags database column.
func parseTags(tagsJSON string) []string {
	if tagsJSON == "" || tagsJSON == "null" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
		return nil
	}
	return tags
}
