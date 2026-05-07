package cluster

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/prastuvwxyz/memgraph/internal/embed"
)

// Cluster represents one topic group.
type Cluster struct {
	Label string
	Files []string
}

// Content clusters files using TF-IDF on body text.
// Works even when files have no frontmatter tags.
func Content(db *sql.DB, prefix string, namespaces []string, k int) ([]Cluster, error) {
	q := `SELECT path, body FROM notes WHERE 1=1`
	var args []any
	if prefix != "" {
		q += ` AND path LIKE ?`
		args = append(args, strings.TrimRight(prefix, "/")+"/%")
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
	defer func() { _ = rows.Close() }()

	type docTerms struct {
		path string
		tf   map[string]float64
	}
	var docs []docTerms
	df := map[string]int{}

	for rows.Next() {
		var path, body string
		if err := rows.Scan(&path, &body); err != nil {
			return nil, err
		}
		tokens := Tokenize(body)
		if len(tokens) == 0 {
			docs = append(docs, docTerms{path, map[string]float64{}})
			continue
		}
		counts := map[string]int{}
		for _, t := range tokens {
			counts[t]++
		}
		tf := make(map[string]float64, len(counts))
		for t, c := range counts {
			tf[t] = float64(c) / float64(len(tokens))
			df[t]++
		}
		docs = append(docs, docTerms{path, tf})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no files found")
	}

	n := float64(len(docs))

	type termScore struct {
		term  string
		score float64
	}
	termMax := map[string]float64{}
	for _, d := range docs {
		for t, tf := range d.tf {
			idf := math.Log(n/float64(df[t])) + 1
			score := tf * idf
			if score > termMax[t] {
				termMax[t] = score
			}
		}
	}

	maxDF := int(n * 0.4)
	if maxDF < 2 {
		maxDF = 2
	}
	var ranked []termScore
	for t, score := range termMax {
		if df[t] < 2 || df[t] > maxDF {
			continue
		}
		ranked = append(ranked, termScore{t, score})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })

	if len(ranked) == 0 {
		return nil, fmt.Errorf("no discriminating terms found")
	}

	centroids := make([]string, 0, k)
	for _, ts := range ranked {
		if len(centroids) >= k {
			break
		}
		centroids = append(centroids, ts.term)
	}

	clusterFiles := make(map[string][]string, len(centroids)+1)
	assigned := map[string]bool{}
	for _, d := range docs {
		best, bestScore := "", -1.0
		for _, c := range centroids {
			idf := math.Log(n/float64(df[c])) + 1
			score := d.tf[c] * idf
			if score > bestScore {
				bestScore = score
				best = c
			}
		}
		if bestScore > 0 {
			clusterFiles[best] = append(clusterFiles[best], d.path)
			assigned[d.path] = true
		}
	}
	for _, d := range docs {
		if !assigned[d.path] {
			clusterFiles["misc"] = append(clusterFiles["misc"], d.path)
		}
	}

	var clusters []Cluster
	for _, c := range centroids {
		if files := clusterFiles[c]; len(files) > 0 {
			clusters = append(clusters, Cluster{Label: c, Files: files})
		}
	}
	if misc := clusterFiles["misc"]; len(misc) > 0 {
		clusters = append(clusters, Cluster{Label: "misc", Files: misc})
	}
	return clusters, nil
}

// Tag groups files by their most common shared tags.
// Files with no tags or unique tags go into a "misc" group.
func Tag(db *sql.DB, prefix string, namespaces []string, k int) ([]Cluster, error) {
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
	defer func() { _ = rows.Close() }()

	tagFiles := map[string][]string{}
	fileAllTags := map[string][]string{}
	var allFiles []string

	for rows.Next() {
		var path, tagsJSON string
		if err := rows.Scan(&path, &tagsJSON); err != nil {
			return nil, err
		}
		tags := parseTagsJSON(tagsJSON)
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

	clusterTags := make([]string, 0, k)
	for _, r := range ranked {
		if len(clusterTags) >= k {
			break
		}
		clusterTags = append(clusterTags, r.tag)
	}
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

// Vector runs k-means on chunk embeddings and returns file-level clusters.
func Vector(db *sql.DB, prefix string, namespaces []string, k int) ([]Cluster, error) {
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
	defer func() { _ = rows.Close() }()

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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(data) < k {
		return nil, fmt.Errorf("not enough embedded files for %d clusters", k)
	}

	dims := len(data[0].vec)
	centroids := make([][]float32, k)
	for i := range centroids {
		c := make([]float32, dims)
		copy(c, data[i].vec)
		centroids[i] = c
	}

	assign := make([]int, len(data))
	for iter := 0; iter < 10; iter++ {
		for i, d := range data {
			best, bestSim := 0, float32(-1)
			for j, c := range centroids {
				sim := CosineSim(d.vec, c)
				if sim > bestSim {
					bestSim = sim
					best = j
				}
			}
			assign[i] = best
		}
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
				newCentroids[i] = centroids[i]
			}
		}
		centroids = newCentroids
	}

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

// CosineSim returns the cosine similarity between two float32 vectors.
func CosineSim(a, b []float32) float32 {
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
	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
}

// Tokenize splits text into lowercase alpha tokens, filtering stopwords and short words.
func Tokenize(text string) []string {
	var tokens []string
	var buf strings.Builder
	flush := func() {
		if buf.Len() >= 4 {
			w := buf.String()
			if !stopwords[w] {
				tokens = append(tokens, w)
			}
		}
		buf.Reset()
	}
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) {
			buf.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "can": true, "with": true,
	"this": true, "that": true, "from": true, "they": true, "will": true,
	"have": true, "been": true, "more": true, "when": true, "what": true,
	"your": true, "which": true, "their": true, "said": true, "each": true,
	"she": true, "how": true, "its": true, "our": true, "out": true,
	"was": true, "has": true, "had": true, "his": true, "her": true,
	"him": true, "into": true, "also": true, "use": true, "used": true,
	"one": true, "two": true, "new": true, "some": true, "there": true,
	"then": true, "than": true, "very": true, "just": true, "only": true,
	"over": true, "after": true, "before": true, "about": true, "like": true,
	"does": true, "would": true, "could": true, "should": true, "may": true,
	"any": true, "same": true, "both": true, "per": true, "via": true,
}

func parseTagsJSON(s string) []string {
	if s == "" || s == "null" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(s), &tags); err != nil {
		return nil
	}
	return tags
}
