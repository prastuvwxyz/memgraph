package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/spf13/cobra"
)

var statsJSON bool

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show index statistics",
	Args:  cobra.NoArgs,
	RunE:  runStats,
}

func init() {
	statsCmd.Flags().BoolVar(&statsJSON, "json", false, "output as JSON")
}

func runStats(cmd *cobra.Command, args []string) error {
	dir := "."
	if dirFlag != "" {
		dir = dirFlag
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	workspace := config.LoadOrDefault(abs)

	dbPath := filepath.Join(workspace.Root, ".memgraph", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "No index found. Run: memgraph index .")
		os.Exit(1)
	}

	db, err := index.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer db.Close()

	fileCount, err := db.Stats()
	if err != nil {
		return fmt.Errorf("get stats: %w", err)
	}

	dbSize, err := db.FileSize()
	if err != nil {
		return fmt.Errorf("get file size: %w", err)
	}

	info, err := os.Stat(dbPath)
	if err != nil {
		return fmt.Errorf("stat db: %w", err)
	}

	if statsJSON {
		out := map[string]any{
			"files_indexed":    fileCount,
			"index_size_bytes": dbSize,
			"index_path":       dbPath,
			"last_modified":    info.ModTime().Format(time.RFC3339),
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	fmt.Println("memgraph index stats")
	fmt.Printf("  Files indexed:  %d\n", fileCount)
	fmt.Printf("  Index size:     %s\n", formatSize(dbSize))
	fmt.Printf("  Index path:     %s\n", dbPath)
	fmt.Printf("  Last modified:  %s\n", info.ModTime().Format(time.DateTime))

	return nil
}
