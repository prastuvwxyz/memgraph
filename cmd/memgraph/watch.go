package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/prastuvwxyz/memgraph/internal/parse"
	"github.com/spf13/cobra"
)

var watchCmd = &cobra.Command{
	Use:   "watch [dir]",
	Short: "Watch a directory and re-index changed markdown files automatically",
	Long: `Watch a directory for file changes and incrementally re-index
modified markdown files without a full re-index.

Only .md files that are created, modified, or deleted trigger a re-index.
Changes are debounced by 500ms to avoid thrashing on rapid edits.

Press Ctrl+C to stop.

Example:
  memgraph watch ~/notes
  memgraph watch .`,
	Args: cobra.MaximumNArgs(1),
	RunE: runWatch,
}

func runWatch(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	} else if dirFlag != "" {
		dir = dirFlag
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	workspace := config.LoadOrDefault(abs)
	dbPath := filepath.Join(workspace.Root, ".memgraph", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "No index found. Run: memgraph index . first.")
		os.Exit(1)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer watcher.Close()

	if err := watchDirs(watcher, abs, workspace.Config.Exclude); err != nil {
		return fmt.Errorf("watch dirs: %w", err)
	}

	fmt.Printf("Watching %s — press Ctrl+C to stop\n", abs)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	emb := resolveEmbedder(workspace.Config.Embed)

	// Debounce: collect changed paths, flush every 500ms.
	pending := map[string]bool{}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nStopped.")
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if !strings.HasSuffix(event.Name, ".md") {
				// Watch newly created subdirectories.
				if event.Has(fsnotify.Create) {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						_ = watcher.Add(event.Name)
					}
				}
				continue
			}
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Remove) {
				pending[event.Name] = true
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "watch error: %v\n", err)

		case <-ticker.C:
			if len(pending) == 0 {
				continue
			}
			paths := make([]string, 0, len(pending))
			for p := range pending {
				paths = append(paths, p)
			}
			pending = map[string]bool{}

			db, err := index.Open(dbPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "open index: %v\n", err)
				continue
			}

			reindexed := 0
			for _, absPath := range paths {
				rel, err := filepath.Rel(workspace.Root, absPath)
				if err != nil {
					continue
				}
				rel = filepath.ToSlash(rel)

				if _, statErr := os.Stat(absPath); os.IsNotExist(statErr) {
					if err := db.DeleteFile(rel); err == nil {
						fmt.Printf("  removed  %s\n", rel)
					}
					continue
				}

				parsed, err := parse.ParseFile(absPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  parse error %s: %v\n", rel, err)
					continue
				}
				parsed.Path = rel

				nsResolver := buildNSResolver(workspace.Root, workspace.Config.Namespaces, "")
				updated, err := db.IndexFile(context.Background(), parsed, nsResolver(rel), emb)
				if err != nil {
					fmt.Fprintf(os.Stderr, "  index error %s: %v\n", rel, err)
					continue
				}
				if updated {
					reindexed++
					fmt.Printf("  indexed  %s\n", rel)
				}
			}
			db.Close()
			if reindexed > 0 {
				fmt.Printf("Re-indexed %d file(s)\n", reindexed)
			}
		}
	}
}

// watchDirs recursively adds all subdirectories to the watcher, skipping excluded patterns.
func watchDirs(w *fsnotify.Watcher, root string, excludes []string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == ".git" || name == ".memgraph" || name == "node_modules" || name == "vendor" {
			return filepath.SkipDir
		}
		for _, ex := range excludes {
			if strings.Contains(path, ex) {
				return filepath.SkipDir
			}
		}
		return w.Add(path)
	})
}
