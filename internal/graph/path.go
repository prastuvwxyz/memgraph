package graph

import (
	"database/sql"
	"encoding/json"
)

// ShortestPath finds the shortest path between src and dst by following outbound wikilinks.
// Returns nil if no path exists.
func ShortestPath(db *sql.DB, src, dst string) ([]string, error) {
	if src == dst {
		return []string{src}, nil
	}

	visited := map[string]string{src: ""}
	queue := []string{src}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		links, err := outboundLinks(db, curr)
		if err != nil {
			return nil, err
		}

		for _, next := range links {
			if _, seen := visited[next]; seen {
				continue
			}
			visited[next] = curr
			if next == dst {
				return reconstructPath(visited, src, dst), nil
			}
			queue = append(queue, next)
		}
	}

	return nil, nil
}

func outboundLinks(db *sql.DB, path string) ([]string, error) {
	var raw string
	if err := db.QueryRow(`SELECT links_out FROM notes WHERE path = ?`, path).Scan(&raw); err != nil {
		return nil, nil //nolint:nilerr // node not in index — treat as no links
	}
	if raw == "" || raw == "null" || raw == "[]" {
		return nil, nil
	}
	var links []string
	if err := json.Unmarshal([]byte(raw), &links); err != nil {
		return nil, nil //nolint:nilerr // malformed JSON — treat as no links
	}
	return links, nil
}

func reconstructPath(parent map[string]string, src, dst string) []string {
	var path []string
	for node := dst; node != ""; node = parent[node] {
		path = append([]string{node}, path...)
		if node == src {
			break
		}
	}
	return path
}
