package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/graph"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/spf13/cobra"
)

var pathJSON bool

var pathCmd = &cobra.Command{
	Use:   "path <from> <to>",
	Short: "Find the shortest link path between two notes",
	Long: `Find the shortest path between two notes by following wikilinks.
Uses BFS over the outbound link graph.

Example:
  memgraph path knowledge/kubernetes.md runbooks/deploy.md
  memgraph path knowledge/kubernetes.md runbooks/deploy.md --json`,
	Args: cobra.ExactArgs(2),
	RunE: runPath,
}

func init() {
	pathCmd.Flags().BoolVar(&pathJSON, "json", false, "output as JSON")
}

func runPath(cmd *cobra.Command, args []string) error {
	from := filepath.ToSlash(args[0])
	to := filepath.ToSlash(args[1])

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

	found, err := graph.ShortestPath(db.SqlDB(), from, to)
	if err != nil {
		return fmt.Errorf("path search: %w", err)
	}

	if pathJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if found == nil {
			return enc.Encode(map[string]any{"from": from, "to": to, "found": false, "path": nil, "hops": -1})
		}
		return enc.Encode(map[string]any{"from": from, "to": to, "found": true, "path": found, "hops": len(found) - 1})
	}

	if found == nil {
		fmt.Printf("No path found from %q to %q\n", from, to)
		return nil
	}

	fmt.Printf("Path: %d hop(s)\n\n", len(found)-1)
	for i, node := range found {
		switch {
		case i == 0:
			fmt.Printf("  %s  (start)\n", node)
		case i == len(found)-1:
			fmt.Printf("  %s  (end)\n", node)
		default:
			fmt.Printf("  %s\n", node)
		}
		if i < len(found)-1 {
			fmt.Println("    ↓")
		}
	}
	return nil
}
