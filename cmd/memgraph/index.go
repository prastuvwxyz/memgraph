package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/spf13/cobra"
)

var indexVerbose bool

var indexCmd = &cobra.Command{
	Use:   "index [dir]",
	Short: "Index markdown files in a directory",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runIndex,
}

func init() {
	indexCmd.Flags().BoolVarP(&indexVerbose, "verbose", "v", false, "print each updated file")
}

func runIndex(cmd *cobra.Command, args []string) error {
	dir := "."
	if dirFlag != "" {
		dir = dirFlag
	}
	if len(args) > 0 {
		dir = args[0]
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	workspace := config.LoadOrDefault(abs)

	dbPath := filepath.Join(workspace.Root, ".memgraph", "index.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return fmt.Errorf("create .memgraph/: %w", err)
	}

	db, err := index.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer db.Close()

	start := time.Now()
	updated, total, err := index.Walk(db, abs, workspace.Config.Exclude, indexVerbose)
	if err != nil {
		return fmt.Errorf("walk: %w", err)
	}

	elapsed := time.Since(start)
	fmt.Printf("Indexed %d files (%d updated) in %.2fs\n", total, updated, elapsed.Seconds())

	return nil
}
