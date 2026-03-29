package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prastuvwxyz/memgraph/internal/config"
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
	filePath := args[0]

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

	sqlDB := db.SqlDB()

	// Build candidate paths to try in the index:
	// 1. As given
	// 2. Absolute version
	// 3. Relative to workspace root (absolute input)
	// 4. Absolute version of path relative to workspace root
	candidates := []string{filePath}
	if absFile, absErr := filepath.Abs(filePath); absErr == nil {
		candidates = append(candidates, absFile)
		if rel, relErr := filepath.Rel(workspace.Root, absFile); relErr == nil {
			candidates = append(candidates, rel)
		}
	}
	// Also try filePath relative to workspace.Root
	candidates = append(candidates, filepath.Join(workspace.Root, filePath))

	var linksJSON string
	var normalizedPath string
	for _, cand := range candidates {
		var tmp string
		queryErr := sqlDB.QueryRow(`SELECT links_out FROM notes WHERE path = ?`, cand).Scan(&tmp)
		if queryErr == nil {
			linksJSON = tmp
			normalizedPath = cand
			break
		} else if queryErr != sql.ErrNoRows {
			return fmt.Errorf("query links: %w", queryErr)
		}
	}
	if normalizedPath == "" {
		return fmt.Errorf("file not found in index: %s", filePath)
	}

	var outLinks []string
	if linksJSON != "" && linksJSON != "null" {
		if jsonErr := json.Unmarshal([]byte(linksJSON), &outLinks); jsonErr != nil {
			return fmt.Errorf("parse links: %w", jsonErr)
		}
	}

	// Query backlinks: files that link to this file
	// Use JSON contains check via LIKE
	likePattern := "%" + normalizedPath + "%"
	rows, err := sqlDB.Query(`SELECT path FROM notes WHERE links_out LIKE ? AND path != ?`, likePattern, normalizedPath)
	if err != nil {
		return fmt.Errorf("query backlinks: %w", err)
	}
	defer rows.Close()

	var backlinks []string
	for rows.Next() {
		var p string
		if scanErr := rows.Scan(&p); scanErr != nil {
			return fmt.Errorf("scan backlink: %w", scanErr)
		}
		backlinks = append(backlinks, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backlinks rows: %w", err)
	}

	// Print output
	fmt.Printf("graph: %s\n", normalizedPath)
	fmt.Println()

	fmt.Printf("Outbound links (%d):\n", len(outLinks))
	if len(outLinks) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, l := range outLinks {
			fmt.Printf("  → %s\n", l)
		}
	}

	fmt.Println()
	fmt.Printf("Backlinks (%d):\n", len(backlinks))
	if len(backlinks) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, b := range backlinks {
			fmt.Printf("  ← %s\n", b)
		}
	}

	return nil
}
