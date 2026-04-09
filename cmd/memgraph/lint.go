package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/index"
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

type lintNote struct {
	Path        string
	Namespace   string
	Title       string
	LinksOut    []string
	LastIndexed int64
}

type lintResult struct {
	Total      int        `json:"total"`
	Orphaned   []string   `json:"orphaned"`    // 0 out, 0 in
	NoBacklink []string   `json:"no_backlink"` // has outbound, nothing links back
	Stale      []staleEntry `json:"stale"`
	Healthy    int        `json:"healthy"`
}

type staleEntry struct {
	Path     string `json:"path"`
	DaysAgo  int    `json:"days_ago"`
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

	sqlDB := db.SqlDB()

	// Build query — optionally filter by namespace
	query := `SELECT path, namespace, title, links_out, last_indexed FROM notes`
	var queryArgs []any
	if len(lintNs) > 0 {
		placeholders := make([]string, len(lintNs))
		for i, ns := range lintNs {
			placeholders[i] = "?"
			queryArgs = append(queryArgs, ns)
		}
		query += ` WHERE namespace IN (` + strings.Join(placeholders, ",") + `)`
	}

	rows, err := sqlDB.Query(query, queryArgs...)
	if err != nil {
		return fmt.Errorf("query notes: %w", err)
	}
	defer rows.Close()

	var notes []lintNote
	for rows.Next() {
		var n lintNote
		var linksJSON string
		if err := rows.Scan(&n.Path, &n.Namespace, &n.Title, &linksJSON, &n.LastIndexed); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		// Skip excluded prefixes
		excluded := false
		for _, ex := range lintExclude {
			if strings.HasPrefix(n.Path, ex) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		if linksJSON != "" && linksJSON != "null" {
			_ = json.Unmarshal([]byte(linksJSON), &n.LinksOut)
		}
		notes = append(notes, n)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	// Build backlink index: path → set of files that link to it
	backlinks := make(map[string][]string)
	for _, n := range notes {
		for _, target := range n.LinksOut {
			backlinks[target] = append(backlinks[target], n.Path)
		}
	}

	now := time.Now().Unix()
	staleThreshold := int64(lintStaleDays) * 86400

	result := lintResult{}
	result.Total = len(notes)

	unhealthy := make(map[string]bool)

	for _, n := range notes {
		outDeg := len(n.LinksOut)
		inDeg := len(backlinks[n.Path])

		if outDeg == 0 && inDeg == 0 {
			result.Orphaned = append(result.Orphaned, n.Path)
			unhealthy[n.Path] = true
		} else if outDeg > 0 && inDeg == 0 {
			result.NoBacklink = append(result.NoBacklink, n.Path)
			unhealthy[n.Path] = true
		}

		age := now - n.LastIndexed
		if age > staleThreshold {
			daysAgo := int(age / 86400)
			result.Stale = append(result.Stale, staleEntry{Path: n.Path, DaysAgo: daysAgo})
			unhealthy[n.Path] = true
		}
	}

	result.Healthy = result.Total - len(unhealthy)

	sort.Strings(result.Orphaned)
	sort.Strings(result.NoBacklink)
	sort.Slice(result.Stale, func(i, j int) bool {
		return result.Stale[i].DaysAgo > result.Stale[j].DaysAgo
	})

	if lintJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	printLintResult(result, lintStaleDays)
	return nil
}

func printLintResult(r lintResult, staleDays int) {
	nsLabel := ""
	if len(lintNs) > 0 {
		nsLabel = fmt.Sprintf(" [ns: %s]", strings.Join(lintNs, ", "))
	}
	fmt.Printf("memgraph lint — %d files%s\n\n", r.Total, nsLabel)

	// Orphaned
	if len(r.Orphaned) == 0 {
		fmt.Println("✓ Orphaned nodes: none")
	} else {
		fmt.Printf("⚠  Orphaned (%d) — no outbound links AND no backlinks:\n", len(r.Orphaned))
		for _, p := range r.Orphaned {
			fmt.Printf("   → %s\n", p)
		}
	}
	fmt.Println()

	// No backlinks
	if len(r.NoBacklink) == 0 {
		fmt.Println("✓ No-backlink nodes: none")
	} else {
		fmt.Printf("⚠  No backlinks (%d) — has outbound links but nothing links back:\n", len(r.NoBacklink))
		for _, p := range r.NoBacklink {
			fmt.Printf("   → %s\n", p)
		}
	}
	fmt.Println()

	// Stale
	if len(r.Stale) == 0 {
		fmt.Printf("✓ Stale files: none (threshold: %d days)\n", staleDays)
	} else {
		fmt.Printf("⚠  Stale (%d) — not re-indexed in %d+ days:\n", len(r.Stale), staleDays)
		for _, s := range r.Stale {
			fmt.Printf("   → %s (%d days ago)\n", s.Path, s.DaysAgo)
		}
	}
	fmt.Println()

	// Summary
	healthPct := 0
	if r.Total > 0 {
		healthPct = r.Healthy * 100 / r.Total
	}
	fmt.Printf("Summary: %d/%d files healthy (%d%%)\n", r.Healthy, r.Total, healthPct)
}
