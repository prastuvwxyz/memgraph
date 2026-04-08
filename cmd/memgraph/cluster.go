package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/embed"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/spf13/cobra"
)

var (
	clusterTopics int
	clusterNs     []string
	clusterPrefix string
)

var clusterCmd = &cobra.Command{
	Use:   "cluster [dir]",
	Short: "Group files by topic automatically",
	Long: `Cluster indexed files into N topic groups without needing to know keywords.
Uses vector k-means when embeddings are available; falls back to tag-graph clustering.

Example:
  memgraph cluster memory/2026-03/ --topics 5
  memgraph cluster --topics 8 --ns stella`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCluster,
}

func init() {
	clusterCmd.Flags().IntVar(&clusterTopics, "topics", 5, "number of topic clusters to produce")
	clusterCmd.Flags().StringArrayVar(&clusterNs, "ns", nil, "filter by namespace(s)")
	clusterCmd.Flags().StringVar(&clusterPrefix, "prefix", "", "restrict to files under this path prefix")
}

func runCluster(cmd *cobra.Command, args []string) error {
	dir := "."
	if dirFlag != "" {
		dir = dirFlag
	}
	if len(args) > 0 {
		// If a subdir is given, use it as prefix filter.
		clusterPrefix = filepath.ToSlash(args[0])
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

	// Try vector clustering first; fall back to tag clustering.
	if emb != nil {
		clusters, err := vectorCluster(db.SqlDB(), clusterPrefix, clusterNs, clusterTopics)
		if err == nil && len(clusters) > 0 {
			printClusters(clusters, "vector")
			return nil
		}
	}

	clusters, err := tagCluster(db.SqlDB(), clusterPrefix, clusterNs, clusterTopics)
	if err != nil {
		return fmt.Errorf("cluster: %w", err)
	}
	printClusters(clusters, "tag")
	return nil
}

// Cluster represents one topic group.
type Cluster struct {
	Label string
	Files []string
}

// tagCluster groups files by their most common shared tags.
// Files with no tags or unique tags go into an "untagged" group.
func tagCluster(db *sql.DB, prefix string, namespaces []string, k int) ([]Cluster, error) {
	q := `SELECT path, tags FROM notes WHERE 1=1`
	var args []any
	if prefix != "" {
		q += ` AND path LIKE ?`
		args = append(args, prefix+"%")
	}
	if len(namespaces) > 0 {
		ph := strings.Repeat("?,", len(namespaces))
		ph = ph[:len(ph)-1]
		q += ` AND namespace IN (` + ph + `)`
		for _, ns := range namespaces {
			args = append(args, ns)
		}
	}
	q += ` ORDER BY path`

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// tag → files mapping.
	tagFiles := map[string][]string{}
	fileAllTags := map[string][]string{}
	var allFiles []string

	for rows.Next() {
		var path, tagsJSON string
		if err := rows.Scan(&path, &tagsJSON); err != nil {
			return nil, err
		}
		tags := parseTags(tagsJSON)
		allFiles = append(allFiles, path)
		fileAllTags[path] = tags
		for _, t := range tags {
			tagFiles[t] = append(tagFiles[t], path)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(allFiles) == 0 {
		return nil, fmt.Errorf("no files found")
	}

	// Sort tags by frequency descending; pick top k representative tags.
	// Skip tags that are too ubiquitous (> 40% of files) — they make poor cluster labels.
	// Skip tags that appear in only 1 file — too specific to be a cluster.
	type tf struct {
		tag   string
		count int
	}
	var ranked []tf
	maxFilesFrac := int(float64(len(allFiles)) * 0.4)
	if maxFilesFrac < 2 {
		maxFilesFrac = 2
	}
	for t, files := range tagFiles {
		n := len(files)
		if n <= 1 || n > maxFilesFrac {
			continue
		}
		ranked = append(ranked, tf{t, n})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].count > ranked[j].count })

	// Pick up to k representative tags.
	clusterTags := make([]string, 0, k)
	for _, r := range ranked {
		if len(clusterTags) >= k {
			break
		}
		clusterTags = append(clusterTags, r.tag)
	}
	// Fallback: if filtering left us with nothing, use the k most frequent tags.
	if len(clusterTags) == 0 {
		for t, files := range tagFiles {
			ranked = append(ranked, tf{t, len(files)})
		}
		sort.Slice(ranked, func(i, j int) bool { return ranked[i].count > ranked[j].count })
		for _, r := range ranked {
			if len(clusterTags) >= k {
				break
			}
			clusterTags = append(clusterTags, r.tag)
		}
	}

	// Assign each file to its best matching cluster (first tag match wins).
	clusterFiles := make(map[string][]string, len(clusterTags)+1)
	assigned := map[string]bool{}
	for _, file := range allFiles {
		for _, ct := range clusterTags {
			for _, ft := range fileAllTags[file] {
				if ft == ct {
					clusterFiles[ct] = append(clusterFiles[ct], file)
					assigned[file] = true
					goto next
				}
			}
		}
	next:
	}

	// Unassigned files go to "misc".
	for _, file := range allFiles {
		if !assigned[file] {
			clusterFiles["misc"] = append(clusterFiles["misc"], file)
		}
	}

	var clusters []Cluster
	for _, ct := range clusterTags {
		if files := clusterFiles[ct]; len(files) > 0 {
			clusters = append(clusters, Cluster{Label: ct, Files: files})
		}
	}
	if misc := clusterFiles["misc"]; len(misc) > 0 {
		clusters = append(clusters, Cluster{Label: "misc", Files: misc})
	}
	return clusters, nil
}

// vectorCluster runs k-means on chunk embeddings and returns file-level clusters.
func vectorCluster(db *sql.DB, prefix string, namespaces []string, k int) ([]Cluster, error) {
	// Fetch one embedding per file (first chunk).
	q := `SELECT c.path, cv.embedding
		  FROM chunk_vectors cv
		  JOIN chunks c ON c.id = cv.chunk_id
		  WHERE c.chunk_index = 0`
	var args []any
	if prefix != "" {
		q += ` AND c.path LIKE ?`
		args = append(args, prefix+"%")
	}
	if len(namespaces) > 0 {
		ph := strings.Repeat("?,", len(namespaces))
		ph = ph[:len(ph)-1]
		q += ` AND c.namespace IN (` + ph + `)`
		for _, ns := range namespaces {
			args = append(args, ns)
		}
	}

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type fileVec struct {
		path string
		vec  []float32
	}
	var data []fileVec
	for rows.Next() {
		var path string
		var blob []byte
		if err := rows.Scan(&path, &blob); err != nil {
			return nil, err
		}
		vec, err := embed.DecodeEmbedding(blob)
		if err != nil {
			continue
		}
		data = append(data, fileVec{path, vec})
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	if len(data) < k {
		return nil, fmt.Errorf("not enough embedded files for %d clusters", k)
	}

	// Simple k-means: initialise centroids from first k files, iterate 10 rounds.
	dims := len(data[0].vec)
	centroids := make([][]float32, k)
	for i := range centroids {
		c := make([]float32, dims)
		copy(c, data[i].vec)
		centroids[i] = c
	}

	assign := make([]int, len(data))
	for iter := 0; iter < 10; iter++ {
		// Assign step.
		for i, d := range data {
			best, bestSim := 0, float32(-1)
			for j, c := range centroids {
				sim := cosineSim(d.vec, c)
				if sim > bestSim {
					bestSim = sim
					best = j
				}
			}
			assign[i] = best
		}
		// Update step.
		newCentroids := make([][]float32, k)
		counts := make([]int, k)
		for i := range newCentroids {
			newCentroids[i] = make([]float32, dims)
		}
		for i, d := range data {
			c := assign[i]
			counts[c]++
			for j, v := range d.vec {
				newCentroids[c][j] += v
			}
		}
		for i := range newCentroids {
			if counts[i] > 0 {
				for j := range newCentroids[i] {
					newCentroids[i][j] /= float32(counts[i])
				}
			} else {
				// Empty cluster: keep old centroid.
				newCentroids[i] = centroids[i]
			}
		}
		centroids = newCentroids
	}

	// Build clusters.
	clusterMap := make(map[int][]string, k)
	for i, d := range data {
		clusterMap[assign[i]] = append(clusterMap[assign[i]], d.path)
	}

	clusters := make([]Cluster, 0, k)
	for i := 0; i < k; i++ {
		if files, ok := clusterMap[i]; ok {
			clusters = append(clusters, Cluster{
				Label: fmt.Sprintf("cluster-%d", i+1),
				Files: files,
			})
		}
	}
	return clusters, nil
}

func cosineSim(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	denom := sqrtF64(na) * sqrtF64(nb)
	return float32(dot / denom)
}

func sqrtF64(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x / 2
	for i := 0; i < 50; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}

func parseTags(tagsJSON string) []string {
	if tagsJSON == "" || tagsJSON == "null" {
		return nil
	}
	// Simple JSON array parse — avoid importing encoding/json for this.
	s := strings.TrimSpace(tagsJSON)
	if !strings.HasPrefix(s, "[") {
		return nil
	}
	s = s[1 : len(s)-1]
	var tags []string
	for _, part := range strings.Split(s, ",") {
		t := strings.TrimSpace(part)
		t = strings.Trim(t, `"`)
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

func printClusters(clusters []Cluster, mode string) {
	fmt.Printf("Clusters (%s-based, %d groups)\n\n", mode, len(clusters))
	for i, c := range clusters {
		fmt.Printf("── %d. [%s] (%d files)\n", i+1, c.Label, len(c.Files))
		for j, f := range c.Files {
			if j >= 8 {
				fmt.Printf("   … and %d more\n", len(c.Files)-j)
				break
			}
			fmt.Printf("   %s\n", f)
		}
		fmt.Println()
	}
}
