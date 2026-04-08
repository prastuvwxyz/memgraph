package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/embed"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/prastuvwxyz/memgraph/internal/rank"
	"github.com/spf13/cobra"
)

var (
	queryCtx        string
	queryTop        int
	queryFormat     string
	queryNamespaces []string
	queryHops       int
)

var queryCmd = &cobra.Command{
	Use:   "query <text>",
	Short: "Search the index",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runQuery,
}

func init() {
	queryCmd.Flags().StringVar(&queryCtx, "ctx", "", "named context to search within")
	queryCmd.Flags().IntVar(&queryTop, "top", 5, "number of results to return")
	queryCmd.Flags().StringVar(&queryFormat, "format", "table", "output format: table, json, paths")
	queryCmd.Flags().StringArrayVar(&queryNamespaces, "ns", nil, "filter by namespace(s); repeatable: --ns stella --ns shared")
	queryCmd.Flags().IntVar(&queryHops, "hops", 0, "BFS graph traversal depth beyond initial results (0 = disabled)")
}

func runQuery(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")

	dir := "."
	if dirFlag != "" {
		dir = dirFlag
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	workspace := config.LoadOrDefault(abs)

	// Resolve context root if --ctx given
	var ctxPrefix string
	if queryCtx != "" {
		ctxDef, ok := workspace.Config.Contexts[queryCtx]
		if !ok {
			return fmt.Errorf("context %q not found in config", queryCtx)
		}
		ctxRoot := ctxDef.Root
		if !filepath.IsAbs(ctxRoot) {
			ctxRoot = filepath.Join(workspace.Root, ctxRoot)
		}
		if rel, relErr := filepath.Rel(workspace.Root, ctxRoot); relErr == nil && rel != "." {
			ctxPrefix = filepath.ToSlash(rel)
		}
	}

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

	emb := resolveEmbedder(workspace.Config.Embed)
	results, err := rank.Search(db.SqlDB(), query, rank.SearchOpts{
		TopN:       queryTop,
		Prefix:     ctxPrefix,
		Namespaces: queryNamespaces,
		Hops:       queryHops,
		Embedder:   emb,
	})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}

	if len(results) == 0 {
		fmt.Printf("No results for %q\n", query)
		return nil
	}

	switch queryFormat {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)

	case "paths":
		for _, r := range results {
			fmt.Println(r.Path)
		}

	default: // table
		printTable(results)
	}

	return nil
}

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

func printTable(results []rank.Result) {
	// Calculate column widths
	pathW := len("Path")
	tagsW := len("Tags")
	for _, r := range results {
		if len(r.Path) > pathW {
			pathW = len(r.Path)
		}
		tags := strings.Join(r.Tags, ", ")
		if len(tags) > tagsW {
			tagsW = len(tags)
		}
	}

	// Cap widths for readability
	if pathW > 50 {
		pathW = 50
	}
	if tagsW > 30 {
		tagsW = 30
	}

	scoreW := 7
	reasonW := 30

	// Header
	header := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s",
		pathW, "Path",
		scoreW, "Score",
		tagsW, "Tags",
		reasonW, "Reason",
	)
	fmt.Println(header)
	fmt.Println(strings.Repeat("-", len(header)))

	// Rows
	for _, r := range results {
		path := r.Path
		if len(path) > pathW {
			path = "..." + path[len(path)-(pathW-3):]
		}
		tags := strings.Join(r.Tags, ", ")
		if len(tags) > tagsW {
			tags = tags[:tagsW-3] + "..."
		}
		reason := r.Reason
		if len(reason) > reasonW {
			reason = reason[:reasonW-3] + "..."
		}
		fmt.Printf("%-*s  %-*.4f  %-*s  %-*s\n",
			pathW, path,
			scoreW, r.Score,
			tagsW, tags,
			reasonW, reason,
		)
	}
}
