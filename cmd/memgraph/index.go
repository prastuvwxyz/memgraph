package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/spf13/cobra"
)


var indexVerbose bool
var indexNamespace string

var indexCmd = &cobra.Command{
	Use:   "index [dir]",
	Short: "Index markdown files in a directory",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runIndex,
}

func init() {
	indexCmd.Flags().BoolVarP(&indexVerbose, "verbose", "v", false, "print each updated file")
	indexCmd.Flags().StringVar(&indexNamespace, "ns", "", "namespace tag for all indexed files (empty = global)")
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

	emb := resolveEmbedder(workspace.Config.Embed)
	if emb != nil {
		fmt.Fprintf(os.Stderr, "Embedding enabled: %s\n", emb.ModelName())
	}

	// Build namespace resolver: config-based prefix map takes precedence over --ns flag.
	nsResolver := buildNSResolver(workspace.Root, workspace.Config.Namespaces, indexNamespace)

	start := time.Now()
	totalUpdated, totalFiles, err := index.Walk(db, abs, workspace.Config.Exclude, indexVerbose, nsResolver, emb)
	if err != nil {
		return fmt.Errorf("walk: %w", err)
	}

	elapsed := time.Since(start)
	fmt.Printf("Indexed %d files (%d updated) in %.2fs\n", totalFiles, totalUpdated, elapsed.Seconds())

	return nil
}

// buildNSResolver returns a function that maps a root-relative file path to its namespace.
// Priority: config namespace map > --ns flag > "" (global).
func buildNSResolver(root string, nsMap map[string][]string, flagNS string) func(string) string {
	if len(nsMap) == 0 {
		return func(string) string { return flagNS }
	}
	// Pre-normalise prefixes: strip trailing slash, convert to slash-separated.
	type entry struct {
		ns     string
		prefix string
	}
	var entries []entry
	for ns, paths := range nsMap {
		for _, p := range paths {
			prefix := filepath.ToSlash(filepath.Clean(p))
			if prefix == "." {
				continue
			}
			entries = append(entries, entry{ns, prefix + "/"})
		}
	}
	_ = root // root available if needed for abs-path comparisons
	return func(rel string) string {
		relSlash := filepath.ToSlash(rel)
		for _, e := range entries {
			if strings.HasPrefix(relSlash+"/", e.prefix) || strings.HasPrefix(relSlash, e.prefix) {
				return e.ns
			}
		}
		return flagNS
	}
}
