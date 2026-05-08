package lint

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Result holds the outcome of a graph health analysis.
type Result struct {
	Total      int          `json:"total"`
	Orphaned   []string     `json:"orphaned"`    // 0 out, 0 in
	NoBacklink []string     `json:"no_backlink"` // has outbound, nothing links back
	SinkNodes  []string     `json:"sink_nodes"`  // has backlinks, 0 outbound
	Stale      []StaleEntry `json:"stale"`
	Healthy    int          `json:"healthy"`
}

// StaleEntry is a file that has not been re-indexed within the stale threshold.
type StaleEntry struct {
	Path    string `json:"path"`
	DaysAgo int    `json:"days_ago"`
}

// Opts controls what Analyze scans.
type Opts struct {
	Namespaces []string
	Exclude    []string
	StaleDays  int
}

type note struct {
	path        string
	linksOut    []string
	lastIndexed int64
}

// Analyze queries the index and returns a graph health report.
func Analyze(db *sql.DB, opts Opts) (*Result, error) {
	q := `SELECT path, links_out, last_indexed FROM notes`
	var args []any
	if len(opts.Namespaces) > 0 {
		placeholders := strings.Repeat("?,", len(opts.Namespaces))
		placeholders = placeholders[:len(placeholders)-1]
		q += ` WHERE namespace IN (` + placeholders + `)`
		for _, ns := range opts.Namespaces {
			args = append(args, ns)
		}
	}

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var notes []note
	for rows.Next() {
		var n note
		var linksJSON string
		if err := rows.Scan(&n.path, &linksJSON, &n.lastIndexed); err != nil {
			return nil, err
		}
		if isExcluded(n.path, opts.Exclude) {
			continue
		}
		if linksJSON != "" && linksJSON != "null" {
			_ = json.Unmarshal([]byte(linksJSON), &n.linksOut)
		}
		notes = append(notes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	backlinks := make(map[string][]string)
	for _, n := range notes {
		for _, target := range n.linksOut {
			backlinks[target] = append(backlinks[target], n.path)
		}
	}

	staleDays := opts.StaleDays
	if staleDays <= 0 {
		staleDays = 90
	}
	staleThreshold := int64(staleDays) * 86400
	now := time.Now().Unix()

	res := &Result{Total: len(notes)}
	unhealthy := make(map[string]bool)

	for _, n := range notes {
		outDeg := len(n.linksOut)
		inDeg := len(backlinks[n.path])

		switch {
		case outDeg == 0 && inDeg == 0:
			res.Orphaned = append(res.Orphaned, n.path)
			unhealthy[n.path] = true
		case outDeg > 0 && inDeg == 0:
			res.NoBacklink = append(res.NoBacklink, n.path)
			unhealthy[n.path] = true
		case outDeg == 0 && inDeg > 0:
			res.SinkNodes = append(res.SinkNodes, n.path)
			unhealthy[n.path] = true
		}

		if age := now - n.lastIndexed; age > staleThreshold {
			res.Stale = append(res.Stale, StaleEntry{Path: n.path, DaysAgo: int(age / 86400)})
			unhealthy[n.path] = true
		}
	}

	res.Healthy = res.Total - len(unhealthy)

	sort.Strings(res.Orphaned)
	sort.Strings(res.NoBacklink)
	sort.Strings(res.SinkNodes)
	sort.Slice(res.Stale, func(i, j int) bool {
		return res.Stale[i].DaysAgo > res.Stale[j].DaysAgo
	})

	return res, nil
}

func isExcluded(path string, exclude []string) bool {
	for _, ex := range exclude {
		if strings.HasPrefix(path, ex) {
			return true
		}
	}
	return false
}
