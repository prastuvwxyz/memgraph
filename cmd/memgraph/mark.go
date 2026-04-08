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

var markCmd = &cobra.Command{
	Use:   "mark <path> [path...]",
	Short: "Mark files as consolidated (excluded from --skip-consolidated queries)",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runMark,
}

var unmarkCmd = &cobra.Command{
	Use:   "unmark <path> [path...]",
	Short: "Remove consolidated marker from files",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runUnmark,
}

func openWorkspaceDB(dir string) (*index.DB, *config.Workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve path: %w", err)
	}
	workspace := config.LoadOrDefault(abs)
	dbPath := filepath.Join(workspace.Root, ".memgraph", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("no index found — run: memgraph index .")
	}
	db, err := index.Open(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open index: %w", err)
	}
	return db, workspace, nil
}

func runMark(cmd *cobra.Command, args []string) error {
	dir := "."
	if dirFlag != "" {
		dir = dirFlag
	}
	db, workspace, err := openWorkspaceDB(dir)
	if err != nil {
		return err
	}
	defer db.Close()

	// Resolve paths relative to workspace root.
	paths := resolveRelPaths(args, workspace.Root)

	n, err := db.MarkConsolidated(paths)
	if err != nil {
		return err
	}
	fmt.Printf("Marked %d file(s) as consolidated at %s\n", n, time.Now().Format("2006-01-02 15:04:05"))
	return nil
}

func runUnmark(cmd *cobra.Command, args []string) error {
	dir := "."
	if dirFlag != "" {
		dir = dirFlag
	}
	db, workspace, err := openWorkspaceDB(dir)
	if err != nil {
		return err
	}
	defer db.Close()

	paths := resolveRelPaths(args, workspace.Root)

	n, err := db.UnmarkConsolidated(paths)
	if err != nil {
		return err
	}
	fmt.Printf("Unmarked %d file(s)\n", n)
	return nil
}

// resolveRelPaths converts absolute or relative input paths to vault-root-relative paths.
func resolveRelPaths(args []string, root string) []string {
	paths := make([]string, 0, len(args))
	for _, a := range args {
		abs, err := filepath.Abs(a)
		if err != nil {
			paths = append(paths, a)
			continue
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			paths = append(paths, a)
			continue
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	return paths
}
