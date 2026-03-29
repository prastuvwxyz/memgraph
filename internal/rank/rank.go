// Package rank searches the memgraph index and returns scored results.
package rank

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const defaultTopN = 5

// SearchOpts configures a Search call.
type SearchOpts struct {
	TopN   int    // max results (0 = default 5)
	Prefix string // if non-empty, restrict to paths with this prefix (e.g. "contexts/work")
}

// Result is a single search result with a blended relevance score.
type Result struct {
	Path         string
	Title        string
	Score        float64
	Tags         []string
	LastVerified string
	Reason       string // human-readable score breakdown
}

// raw holds per-path score components before blending.
type raw struct {
	path         string
	title        string
	tagsJSON     string
	lastVerified string
	bm25         float64
	filename     float64
	tagBonus     float64
	graphBoost   float64
}

// Search queries the index and returns ranked results.
func Search(db *sql.DB, query string, opts SearchOpts) ([]Result, error) {
	topN := opts.TopN
	if topN <= 0 {
		topN = defaultTopN
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	// 1. Tokenize.
	terms := tokenize(query)

	// 2. FTS5 BM25 search.
	rows, err := ftsSearch(db, query, opts.Prefix)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}

	if len(rows) == 0 {
		return nil, nil
	}

	// Index by path for dedup and graph boost lookup.
	byPath := make(map[string]*raw, len(rows))
	ordered := make([]*raw, 0, len(rows))
	for _, r := range rows {
		if _, exists := byPath[r.path]; !exists {
			byPath[r.path] = r
			ordered = append(ordered, r)
		}
	}

	// 3. Filename score.
	for _, r := range ordered {
		r.filename = filenameScore(r.path, terms)
	}

	// 4. Tag bonus.
	for _, r := range ordered {
		r.tagBonus = tagBonusScore(r.tagsJSON, terms)
	}

	// 5. Graph boost: top-3 by (bm25 + filename).
	type scored struct {
		r     *raw
		pre   float64
	}
	pre := make([]scored, len(ordered))
	for i, r := range ordered {
		pre[i] = scored{r, r.bm25 + r.filename}
	}
	sort.Slice(pre, func(i, j int) bool { return pre[i].pre > pre[j].pre })

	top3 := pre
	if len(top3) > 3 {
		top3 = top3[:3]
	}

	for _, s := range top3 {
		links, err := fetchLinksOut(db, s.r.path)
		if err != nil {
			continue
		}
		for _, linked := range links {
			if target, ok := byPath[linked]; ok {
				target.graphBoost += 0.5
			}
		}
	}

	// 6. Blend and build results.
	results := make([]Result, 0, len(ordered))
	for _, r := range ordered {
		final := 0.4*r.filename + 0.5*r.bm25 + 0.1*r.graphBoost + r.tagBonus

		tags := parseTags(r.tagsJSON)
		reason := buildReason(r, final)

		results = append(results, Result{
			Path:         r.path,
			Title:        r.title,
			Score:        final,
			Tags:         tags,
			LastVerified: r.lastVerified,
			Reason:       reason,
		})
	}

	// 7. Sort descending, return topN.
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })

	if len(results) > topN {
		results = results[:topN]
	}
	return results, nil
}

// ftsSearch runs the FTS5 BM25 query with optional path prefix filter.
func ftsSearch(db *sql.DB, query string, prefix string) ([]*raw, error) {
	base := `
		SELECT n.id, n.path, n.title, n.tags, n.last_verified,
		       -bm25(notes_fts) as bm25_score
		FROM notes_fts
		JOIN notes n ON notes_fts.rowid = n.id
		WHERE notes_fts MATCH ?`

	args := []any{query}
	if prefix != "" {
		base += ` AND n.path LIKE ?`
		args = append(args, prefix+"/%")
	}
	base += ` ORDER BY bm25_score DESC LIMIT 50`

	rows, err := db.Query(base, args...)
	if err != nil {
		// Retry with escaped query.
		escaped := `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
		args[0] = escaped
		rows, err = db.Query(base, args...)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()

	var results []*raw
	for rows.Next() {
		var id int64
		var r raw
		if err := rows.Scan(&id, &r.path, &r.title, &r.tagsJSON, &r.lastVerified, &r.bm25); err != nil {
			return nil, err
		}
		results = append(results, &r)
	}
	return results, rows.Err()
}

// fetchLinksOut returns the links_out slice for the given path.
func fetchLinksOut(db *sql.DB, path string) ([]string, error) {
	var linksJSON string
	err := db.QueryRow(`SELECT links_out FROM notes WHERE path = ?`, path).Scan(&linksJSON)
	if err != nil {
		return nil, err
	}
	var links []string
	if err := json.Unmarshal([]byte(linksJSON), &links); err != nil {
		return nil, err
	}
	return links, nil
}

// tokenize splits a query into lowercase terms.
func tokenize(q string) []string {
	parts := strings.Fields(q)
	terms := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.ToLower(p)
		if t != "" {
			terms = append(terms, t)
		}
	}
	return terms
}

// filenameScore returns matches*3.0 for terms found in the path components.
func filenameScore(path string, terms []string) float64 {
	lower := strings.ToLower(path)
	// Check filename and all directory components.
	base := strings.ToLower(filepath.Base(path))
	dir := strings.ToLower(filepath.Dir(path))

	var matches float64
	for _, t := range terms {
		if strings.Contains(base, t) || strings.Contains(dir, t) || strings.Contains(lower, t) {
			matches++
		}
	}
	return matches * 3.0
}

// tagBonusScore returns +2.0 for each query term exactly matching a tag.
func tagBonusScore(tagsJSON string, terms []string) float64 {
	tags := parseTags(tagsJSON)
	if len(tags) == 0 {
		return 0
	}
	tagSet := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tagSet[strings.ToLower(tag)] = struct{}{}
	}
	var bonus float64
	for _, t := range terms {
		if _, ok := tagSet[t]; ok {
			bonus += 2.0
		}
	}
	return bonus
}

// parseTags unmarshals a JSON tags array, returning nil on error.
func parseTags(tagsJSON string) []string {
	if tagsJSON == "" || tagsJSON == "null" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
		return nil
	}
	return tags
}

// buildReason formats the human-readable score breakdown.
func buildReason(r *raw, final float64) string {
	parts := []string{}
	if r.filename > 0 {
		parts = append(parts, fmt.Sprintf("filename:%.0f", r.filename))
	}
	if r.bm25 > 0 {
		parts = append(parts, fmt.Sprintf("bm25:%.2f", r.bm25))
	}
	if r.tagBonus > 0 {
		parts = append(parts, fmt.Sprintf("tag:%.0f", r.tagBonus))
	}
	if r.graphBoost > 0 {
		parts = append(parts, fmt.Sprintf("graph:%.1f", r.graphBoost))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("score:%.4f", final)
	}
	return strings.Join(parts, " ")
}
