package graph

import (
	"database/sql"
	"encoding/json"
	"strings"
)

// Node is a single file in the graph visualisation.
type Node struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Tags      []string `json:"tags"`
	Group     string   `json:"group"`
	Namespace string   `json:"namespace"`
	Links     int      `json:"links"`
}

// Link is a directed edge between two nodes.
type Link struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// Data is the full graph payload for the web UI.
type Data struct {
	Nodes []Node `json:"nodes"`
	Links []Link `json:"links"`
}

// BuildData queries the index and returns a graph payload, optionally filtered by namespace.
func BuildData(db *sql.DB, nsFilter string) (*Data, error) {
	q := `SELECT path, title, tags, links_out, namespace FROM notes`
	var args []any
	if nsFilter != "" {
		q += ` WHERE namespace = ?`
		args = append(args, nsFilter)
	}

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	pathSet := make(map[string]bool)
	var nodes []Node
	var allLinks []Link

	for rows.Next() {
		var path, title, tagsJSON, linksJSON, namespace string
		if err := rows.Scan(&path, &title, &tagsJSON, &linksJSON, &namespace); err != nil {
			return nil, err
		}

		pathSet[path] = true

		var tags []string
		if tagsJSON != "" && tagsJSON != "null" {
			_ = json.Unmarshal([]byte(tagsJSON), &tags)
		}

		var rawLinks []string
		if linksJSON != "" && linksJSON != "null" {
			_ = json.Unmarshal([]byte(linksJSON), &rawLinks)
		}

		nodes = append(nodes, Node{
			ID:        path,
			Title:     title,
			Tags:      tags,
			Group:     pathGroup(path),
			Namespace: namespace,
			Links:     len(rawLinks),
		})

		for _, target := range rawLinks {
			allLinks = append(allLinks, Link{Source: path, Target: target})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Only include links where both endpoints are indexed.
	validLinks := make([]Link, 0, len(allLinks))
	for _, l := range allLinks {
		if pathSet[l.Target] {
			validLinks = append(validLinks, l)
		}
	}

	return &Data{Nodes: nodes, Links: validLinks}, nil
}

func pathGroup(path string) string {
	idx := strings.IndexByte(path, '/')
	if idx < 0 {
		return "root"
	}
	return path[:idx]
}
