// Package rank searches the memgraph index and returns scored results.
package rank

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/prastuvwxyz/memgraph/internal/embed"
)

const defaultTopN = 5

const (
	vectorWeight    = 0.6
	bm25Weight      = 0.4
	scoreThreshold  = 0.1 // minimum combined score to include a chunk result
	chunkCandidateK = 20  // chunk candidates per search method
)

// SearchOpts configures a Search call.
type SearchOpts struct {
	TopN       int            // max results (0 = default 5)
	Prefix     string         // if non-empty, restrict to paths with this prefix
	Namespaces []string       // if non-empty, restrict to these namespaces; empty = all
	Hops       int            // BFS graph traversal depth (0 = disabled)
	Embedder   embed.Embedder // optional; enables vector search when non-nil
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
	vectorScore  float64 // max vector score across chunks for this path
}

// Search queries the index and returns ranked results.
// When opts.Embedder is non-nil and chunk vectors exist, uses hybrid BM25+vector search.
// Falls back to BM25-only otherwise.
func Search(db *sql.DB, query string, opts SearchOpts) ([]Result, error) {
	topN := opts.TopN
	if topN <= 0 {
		topN = defaultTopN
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	terms := tokenize(query)

	// Try hybrid chunk-based search first.
	chunkResults, err := searchChunks(db, query, opts)
	if err != nil {
		return nil, err
	}

	// If we got chunk-level results, build file-level scores from them.
	// Otherwise fall back to notes_fts (file-level BM25).
	byPath := make(map[string]*raw)
	var ordered []*raw

	if len(chunkResults) > 0 {
		for path, scores := range chunkResults {
			r := &raw{path: path, bm25: scores.bm25, vectorScore: float64(scores.vector)}
			byPath[path] = r
			ordered = append(ordered, r)
		}
	} else {
		// Fallback: file-level FTS search.
		rows, err := ftsSearch(db, query, opts.Prefix, opts.Namespaces)
		if err != nil {
			return nil, fmt.Errorf("fts search: %w", err)
		}
		for _, r := range rows {
			if _, exists := byPath[r.path]; !exists {
				byPath[r.path] = r
				ordered = append(ordered, r)
			}
		}
	}

	// Tag-only secondary search (for files whose term only appears in tags).
	tagRows, err := tagOnlySearch(db, terms, opts.Prefix, opts.Namespaces, byPath)
	if err == nil {
		for _, r := range tagRows {
			byPath[r.path] = r
			ordered = append(ordered, r)
		}
	}

	if len(ordered) == 0 {
		return nil, nil
	}

	// Hydrate title/tags/lastVerified for chunk-result paths (not fetched during chunk search).
	if err := hydrateMetadata(db, byPath); err != nil {
		return nil, err
	}

	// Filename score.
	for _, r := range ordered {
		r.filename = filenameScore(r.path, terms)
	}

	// Tag bonus.
	for _, r := range ordered {
		r.tagBonus = tagBonusScore(r.tagsJSON, terms)
	}

	// Graph boost: top-3 by pre-blend score.
	type scored struct {
		r   *raw
		pre float64
	}
	pre := make([]scored, len(ordered))
	for i, r := range ordered {
		pre[i] = scored{r, r.bm25 + r.filename + r.vectorScore}
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

	// BFS graph traversal: expand opts.Hops hops beyond initial results.
	if opts.Hops > 0 {
		bfsExpand(db, byPath, &ordered, opts.Hops, opts.Namespaces)
		if err := hydrateMetadata(db, byPath); err != nil {
			return nil, err
		}
		// Re-score filename/tags for newly added nodes.
		for _, r := range ordered {
			if r.filename == 0 {
				r.filename = filenameScore(r.path, terms)
			}
			if r.tagBonus == 0 {
				r.tagBonus = tagBonusScore(r.tagsJSON, terms)
			}
		}
	}

	// Blend and build results.
	results := make([]Result, 0, len(ordered))
	for _, r := range ordered {
		var final float64
		if r.vectorScore > 0 {
			// Hybrid: vector contributes alongside BM25 and filename.
			final = vectorWeight*r.vectorScore + bm25Weight*(0.4*r.filename+0.5*r.bm25+0.1*r.graphBoost+r.tagBonus)
		} else {
			final = 0.4*r.filename + 0.5*r.bm25 + 0.1*r.graphBoost + r.tagBonus
		}

		tags := parseTags(r.tagsJSON)
		results = append(results, Result{
			Path:         r.path,
			Title:        r.title,
			Score:        final,
			Tags:         tags,
			LastVerified: r.lastVerified,
			Reason:       buildReason(r, final),
		})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > topN {
		results = results[:topN]
	}
	return results, nil
}

// pathScores holds the best BM25 and vector score for a path (rolled up from chunks).
type pathScores struct {
	bm25   float64
	vector float32
}

// searchChunks runs chunk-level BM25 + optional vector search, rolling up to path level.
// Returns empty map if no chunks are indexed yet.
func searchChunks(db *sql.DB, query string, opts SearchOpts) (map[string]*pathScores, error) { //nolint:cyclop
	// Check if chunks table has any rows.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks LIMIT 1`).Scan(&count); err != nil || count == 0 {
		return nil, nil
	}

	ctx := context.Background()

	// BM25 on chunks.
	bm25ByChunk, err := chunkBM25Search(db, query, opts.Prefix, opts.Namespaces)
	if err != nil {
		return nil, err
	}

	// Vector search on chunks (optional).
	vectorByChunk := map[string]float32{}
	if opts.Embedder != nil {
		vecs, err := opts.Embedder.Embed(ctx, []string{query})
		if err == nil && len(vecs) > 0 {
			vectorByChunk, _ = chunkVectorSearch(db, vecs[0], opts.Prefix, opts.Namespaces)
		}
	}

	if len(bm25ByChunk) == 0 && len(vectorByChunk) == 0 {
		return nil, nil
	}

	// Normalize BM25 scores to [0,1].
	maxBM25 := float32(0)
	for _, s := range bm25ByChunk {
		if s > maxBM25 {
			maxBM25 = s
		}
	}

	// Collect all candidate chunk IDs.
	seen := map[string]bool{}
	var candidates []string
	for id := range vectorByChunk {
		if !seen[id] {
			seen[id] = true
			candidates = append(candidates, id)
		}
	}
	for id := range bm25ByChunk {
		if !seen[id] {
			seen[id] = true
			candidates = append(candidates, id)
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Fetch paths for all candidates.
	chunkPaths, err := fetchChunkPaths(db, candidates)
	if err != nil {
		return nil, err
	}

	// Roll up to file level: take max score per path.
	result := map[string]*pathScores{}
	for _, id := range candidates {
		path, ok := chunkPaths[id]
		if !ok {
			continue
		}
		vs := vectorByChunk[id]
		bs := bm25ByChunk[id]
		if maxBM25 > 0 {
			bs = bs / maxBM25
		}

		var combined float32
		if opts.Embedder != nil {
			combined = float32(vectorWeight)*vs + float32(bm25Weight)*bs
		} else {
			combined = bs
		}
		if combined < scoreThreshold {
			continue
		}

		if existing, ok := result[path]; ok {
			if combined > float32(existing.bm25) || vs > existing.vector {
				if bs > float32(existing.bm25) {
					existing.bm25 = float64(bs)
				}
				if vs > existing.vector {
					existing.vector = vs
				}
			}
		} else {
			result[path] = &pathScores{bm25: float64(bs), vector: vs}
		}
	}

	return result, nil
}

// chunkBM25Search queries chunks_fts, returning chunk_id → raw BM25 score.
func chunkBM25Search(db *sql.DB, query, prefix string, namespaces []string) (map[string]float32, error) {
	baseQ := `
		SELECT c.id, -rank AS bm25_score
		FROM chunks_fts
		JOIN chunks c ON c.rowid = chunks_fts.rowid
		WHERE chunks_fts MATCH ?`
	args := []any{query}
	if prefix != "" {
		baseQ += ` AND c.path LIKE ?`
		args = append(args, prefix+"/%")
	}
	nsSQL, nsArgs := nsClause("c", namespaces)
	baseQ += nsSQL
	args = append(args, nsArgs...)
	baseQ += ` ORDER BY rank LIMIT ?`
	args = append(args, chunkCandidateK)

	rows, err := db.Query(baseQ, args...)
	if err != nil {
		// Retry with escaped query on FTS syntax error.
		escaped := `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
		args[0] = escaped
		rows, err = db.Query(baseQ, args...)
		if err != nil {
			return nil, nil // non-fatal: fall back to vector-only
		}
	}
	defer rows.Close()

	scores := map[string]float32{}
	for rows.Next() {
		var id string
		var score float32
		if err := rows.Scan(&id, &score); err != nil {
			return nil, err
		}
		scores[id] = score
	}
	return scores, rows.Err()
}

// chunkVectorSearch computes cosine similarity against all stored embeddings.
func chunkVectorSearch(db *sql.DB, queryVec []float32, prefix string, namespaces []string) (map[string]float32, error) {
	needsJoin := prefix != "" || len(namespaces) > 0
	var baseQ string
	var args []any
	if needsJoin {
		baseQ = `SELECT cv.chunk_id, cv.embedding FROM chunk_vectors cv JOIN chunks c ON c.id = cv.chunk_id WHERE 1=1`
		if prefix != "" {
			baseQ += ` AND c.path LIKE ?`
			args = append(args, prefix+"/%")
		}
		nsSQL, nsArgs := nsClause("c", namespaces)
		baseQ += nsSQL
		args = append(args, nsArgs...)
	} else {
		baseQ = `SELECT cv.chunk_id, cv.embedding FROM chunk_vectors cv`
	}

	var rows *sql.Rows
	var err error
	if len(args) > 0 {
		rows, err = db.Query(baseQ, args...)
	} else {
		rows, err = db.Query(baseQ)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type candidate struct {
		id    string
		score float32
	}
	var all []candidate
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		vec, err := embed.DecodeEmbedding(blob)
		if err != nil {
			continue
		}
		sim := cosineSimilarity(queryVec, vec)
		all = append(all, candidate{id, sim})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })
	if len(all) > chunkCandidateK {
		all = all[:chunkCandidateK]
	}

	scores := make(map[string]float32, len(all))
	for _, c := range all {
		scores[c.id] = c.score
	}
	return scores, nil
}

// fetchChunkPaths returns path for each chunk id.
func fetchChunkPaths(db *sql.DB, ids []string) (map[string]string, error) {
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.Query(`SELECT id, path FROM chunks WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]string{}
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			return nil, err
		}
		result[id] = path
	}
	return result, rows.Err()
}

// hydrateMetadata fills title/tags/lastVerified for paths that only have scores so far.
func hydrateMetadata(db *sql.DB, byPath map[string]*raw) error {
	for path, r := range byPath {
		if r.title != "" {
			continue
		}
		var title, tagsJSON, lastVerified string
		err := db.QueryRow(
			`SELECT title, tags, last_verified FROM notes WHERE path = ?`, path,
		).Scan(&title, &tagsJSON, &lastVerified)
		if err != nil {
			continue // file might not be in notes yet; skip gracefully
		}
		r.title = title
		r.tagsJSON = tagsJSON
		r.lastVerified = lastVerified
	}
	return nil
}

// bfsExpand performs breadth-first traversal from the initial result set along
// links_out edges. Newly discovered paths are added to byPath and ordered with
// a decaying score (parent score × 0.5 per hop). Namespaces filter is applied
// if non-empty.
func bfsExpand(db *sql.DB, byPath map[string]*raw, ordered *[]*raw, hops int, namespaces []string) {
	frontier := make([]string, 0, len(byPath))
	for p := range byPath {
		frontier = append(frontier, p)
	}

	for hop := 0; hop < hops && len(frontier) > 0; hop++ {
		decay := math.Pow(0.5, float64(hop+1)) // 0.5, 0.25, 0.125 …
		var nextFrontier []string

		for _, path := range frontier {
			links, err := fetchLinksOut(db, path)
			if err != nil || len(links) == 0 {
				continue
			}
			parentScore := byPath[path].bm25 + byPath[path].vectorScore

			for _, link := range links {
				if byPath[link] != nil {
					continue // already in result set
				}
				// Check namespace constraint.
				if len(namespaces) > 0 && !pathInNamespaces(db, link, namespaces) {
					continue
				}
				r := &raw{
					path:        link,
					bm25:        parentScore * decay,
					graphBoost:  0.5,
				}
				byPath[link] = r
				*ordered = append(*ordered, r)
				nextFrontier = append(nextFrontier, link)
			}
		}
		frontier = nextFrontier
	}
}

// pathInNamespaces returns true if the note at path belongs to one of the given namespaces.
func pathInNamespaces(db *sql.DB, path string, namespaces []string) bool {
	placeholders := strings.Repeat("?,", len(namespaces))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(namespaces)+1)
	args = append(args, path)
	for _, ns := range namespaces {
		args = append(args, ns)
	}
	var ns string
	err := db.QueryRow(
		`SELECT namespace FROM notes WHERE path = ? AND namespace IN (`+placeholders+`)`, args...,
	).Scan(&ns)
	return err == nil
}

// nsClause builds a SQL WHERE fragment and args slice for namespace filtering.
// If namespaces is empty, returns no filter (all namespaces).
// table is the alias/table name that has a .namespace column (e.g. "n", "c").
func nsClause(table string, namespaces []string) (string, []any) {
	if len(namespaces) == 0 {
		return "", nil
	}
	placeholders := strings.Repeat("?,", len(namespaces))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(namespaces))
	for i, ns := range namespaces {
		args[i] = ns
	}
	return " AND " + table + ".namespace IN (" + placeholders + ")", args
}

// cosineSimilarity returns the cosine similarity between two float32 vectors.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}

// ftsSearch runs the file-level FTS5 BM25 query (fallback when no chunks indexed).
func ftsSearch(db *sql.DB, query string, prefix string, namespaces []string) ([]*raw, error) {
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
	nsSQL, nsArgs := nsClause("n", namespaces)
	base += nsSQL
	args = append(args, nsArgs...)
	base += ` ORDER BY bm25_score DESC LIMIT 50`

	rows, err := db.Query(base, args...)
	if err != nil {
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

// parseTags unmarshals a JSON tags array.
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

// tagOnlySearch finds files with an exact tag match not returned by FTS5.
func tagOnlySearch(db *sql.DB, terms []string, prefix string, namespaces []string, already map[string]*raw) ([]*raw, error) {
	if len(terms) == 0 {
		return nil, nil
	}

	clauses := make([]string, 0, len(terms))
	args := make([]any, 0, len(terms)+1)
	for _, t := range terms {
		clauses = append(clauses, `tags LIKE ?`)
		args = append(args, `%"`+t+`"%`)
	}

	q := `SELECT path, title, tags, last_verified FROM notes WHERE (` +
		strings.Join(clauses, " OR ") + `)`
	if prefix != "" {
		q += ` AND path LIKE ?`
		args = append(args, prefix+"/%")
	}
	nsSQL, nsArgs := nsClause("notes", namespaces)
	q += nsSQL
	args = append(args, nsArgs...)

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*raw
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.path, &r.title, &r.tagsJSON, &r.lastVerified); err != nil {
			return nil, err
		}
		if already[r.path] != nil {
			continue
		}
		r.tagBonus = tagBonusScore(r.tagsJSON, terms)
		results = append(results, &r)
	}
	return results, rows.Err()
}

// buildReason formats the human-readable score breakdown.
func buildReason(r *raw, final float64) string {
	var parts []string
	if r.vectorScore > 0 {
		parts = append(parts, fmt.Sprintf("vec:%.2f", r.vectorScore))
	}
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
