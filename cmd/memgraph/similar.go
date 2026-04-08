package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/prastuvwxyz/memgraph/internal/rank"
	"github.com/spf13/cobra"
)

var (
	similarAgainst   string
	similarTop       int
	similarThreshold float64
	similarNs        []string
)

var similarCmd = &cobra.Command{
	Use:   "similar <text>",
	Short: "Find files similar to a text snippet (dedup/insight check)",
	Long: `Find files that are semantically or lexically similar to the given text.
Useful for checking: "does this insight already exist in my knowledge base?"

Output includes a confidence label: MATCH (>80%), SIMILAR (>50%), RELATED (<50%).`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSimilar,
}

func init() {
	similarCmd.Flags().StringVar(&similarAgainst, "against", "", "restrict search to this path prefix (e.g. knowledge/)")
	similarCmd.Flags().IntVar(&similarTop, "top", 5, "number of results to return")
	similarCmd.Flags().Float64Var(&similarThreshold, "threshold", 0.0, "minimum score threshold (0.0–1.0, 0 = show all)")
	similarCmd.Flags().StringArrayVar(&similarNs, "ns", nil, "filter by namespace(s)")
}

func runSimilar(cmd *cobra.Command, args []string) error {
	text := strings.Join(args, " ")

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

	emb := resolveEmbedder(workspace.Config.Embed)
	opts := rank.SearchOpts{
		TopN:       similarTop * 3,
		Prefix:     similarAgainst,
		Namespaces: similarNs,
		Embedder:   emb,
	}
	results, err := rank.Search(db.SqlDB(), text, opts)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}

	// If no results with full text, retry with the longest word (most distinctive token).
	if len(results) == 0 {
		best := longestToken(text)
		if best != "" && best != text {
			results, err = rank.Search(db.SqlDB(), best, opts)
			if err != nil {
				return fmt.Errorf("search: %w", err)
			}
		}
	}

	if len(results) == 0 {
		fmt.Println("No similar content found. Try a shorter or more specific query.")
		return nil
	}

	// Normalize scores to [0,1] relative to top result.
	maxScore := results[0].Score
	if maxScore == 0 {
		maxScore = 1
	}

	// Print header.
	pathW := 50
	fmt.Printf("%-*s  %-10s  %-10s  %s\n", pathW, "Path", "Score", "Similarity", "Confidence")
	fmt.Println(strings.Repeat("-", pathW+36))

	shown := 0
	for _, r := range results {
		norm := r.Score / maxScore
		if norm < similarThreshold {
			continue
		}

		label := confidenceLabel(norm)
		path := r.Path
		if len(path) > pathW {
			path = "..." + path[len(path)-(pathW-3):]
		}
		fmt.Printf("%-*s  %-10.4f  %-10.1f%%  %s\n", pathW, path, r.Score, norm*100, label)
		shown++
		if shown >= similarTop {
			break
		}
	}

	if shown == 0 {
		fmt.Printf("No results above threshold %.2f\n", similarThreshold)
	}
	return nil
}

// longestToken returns the longest whitespace-separated word in text.
func longestToken(text string) string {
	best := ""
	for _, w := range strings.Fields(text) {
		if len(w) > len(best) {
			best = w
		}
	}
	return best
}

func confidenceLabel(norm float64) string {
	switch {
	case norm >= 0.8:
		return "MATCH"
	case norm >= 0.5:
		return "SIMILAR"
	default:
		return "RELATED"
	}
}
