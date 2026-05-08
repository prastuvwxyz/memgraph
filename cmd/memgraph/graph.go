package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/graph"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/spf13/cobra"
)

var graphCmd = &cobra.Command{
	Use:   "graph <file>",
	Short: "Show outbound links and backlinks for a file",
	Args:  cobra.ExactArgs(1),
	RunE:  runGraph,
}

func runGraph(cmd *cobra.Command, args []string) error {
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

	links, err := graph.Links(db.SqlDB(), workspace.Root, args[0])
	if err != nil {
		return fmt.Errorf("query links: %w", err)
	}
	if links == nil {
		return fmt.Errorf("file not found in index: %s", args[0])
	}

	fmt.Printf("graph: %s\n\n", links.Path)

	fmt.Printf("Outbound links (%d):\n", len(links.OutLinks))
	if len(links.OutLinks) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, l := range links.OutLinks {
			fmt.Printf("  → %s\n", l)
		}
	}

	fmt.Println()
	fmt.Printf("Backlinks (%d):\n", len(links.Backlinks))
	if len(links.Backlinks) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, b := range links.Backlinks {
			fmt.Printf("  ← %s\n", b)
		}
	}

	return nil
}
