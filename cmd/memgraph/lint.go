package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/prastuvwxyz/memgraph/internal/lint"
	"github.com/spf13/cobra"
)

var (
	lintStaleDays int
	lintNs        []string
	lintJSON      bool
	lintExclude   []string
)

var lintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Audit graph health: orphans, weak connections, stale files",
	Long: `Scan the knowledge graph for structural issues:

  Orphaned   — no outbound links AND no backlinks (isolated from graph)
  No backlinks — has outbound links but nothing links back to it
  Stale      — not re-indexed in N days (default: 90)

Inspired by the Knowledge Linting pattern: treat your knowledge base like
code — run lint regularly to surface orphaned concepts and disconnected nodes.`,
	RunE: runLint,
}

func init() {
	lintCmd.Flags().IntVar(&lintStaleDays, "stale-days", 90, "flag files not re-indexed in this many days")
	lintCmd.Flags().StringArrayVar(&lintNs, "ns", nil, "filter by namespace(s)")
	lintCmd.Flags().BoolVar(&lintJSON, "json", false, "output as JSON")
	lintCmd.Flags().StringArrayVar(&lintExclude, "exclude", nil, "exclude path prefixes (e.g. memory/ docs/ agents/)")
}

func runLint(cmd *cobra.Command, args []string) error {
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

	result, err := lint.Analyze(db.SqlDB(), lint.Opts{
		Namespaces: lintNs,
		Exclude:    lintExclude,
		StaleDays:  lintStaleDays,
	})
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}

	if lintJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	printLintResult(result)
	return nil
}

func printLintResult(r *lint.Result) {
	nsLabel := ""
	if len(lintNs) > 0 {
		nsLabel = fmt.Sprintf(" [ns: %s]", strings.Join(lintNs, ", "))
	}
	fmt.Printf("memgraph lint — %d files%s\n\n", r.Total, nsLabel)

	if len(r.Orphaned) == 0 {
		fmt.Println("✓ Orphaned nodes: none")
	} else {
		fmt.Printf("⚠  Orphaned (%d) — no outbound links AND no backlinks:\n", len(r.Orphaned))
		for _, p := range r.Orphaned {
			fmt.Printf("   → %s\n", p)
		}
	}
	fmt.Println()

	if len(r.NoBacklink) == 0 {
		fmt.Println("✓ No-backlink nodes: none")
	} else {
		fmt.Printf("⚠  No backlinks (%d) — has outbound links but nothing links back:\n", len(r.NoBacklink))
		for _, p := range r.NoBacklink {
			fmt.Printf("   → %s\n", p)
		}
	}
	fmt.Println()

	if len(r.SinkNodes) == 0 {
		fmt.Println("✓ Sink nodes: none")
	} else {
		fmt.Printf("⚠  Sink nodes (%d) — has backlinks but no outbound links (dead ends):\n", len(r.SinkNodes))
		for _, p := range r.SinkNodes {
			fmt.Printf("   → %s\n", p)
		}
	}
	fmt.Println()

	if len(r.Stale) == 0 {
		fmt.Printf("✓ Stale files: none (threshold: %d days)\n", lintStaleDays)
	} else {
		fmt.Printf("⚠  Stale (%d) — not re-indexed in %d+ days:\n", len(r.Stale), lintStaleDays)
		for _, s := range r.Stale {
			fmt.Printf("   → %s (%d days ago)\n", s.Path, s.DaysAgo)
		}
	}
	fmt.Println()

	healthPct := 0
	if r.Total > 0 {
		healthPct = r.Healthy * 100 / r.Total
	}
	fmt.Printf("Summary: %d/%d files healthy (%d%%)\n", r.Healthy, r.Total, healthPct)
}
