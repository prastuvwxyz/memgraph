package graph

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
)

// FileLinks holds the outbound links and backlinks for a single indexed file.
type FileLinks struct {
	Path      string
	OutLinks  []string
	Backlinks []string
}

// Links looks up outbound links and backlinks for the given filePath.
// It tries several candidate path forms against the index to handle both
// relative and absolute inputs. Returns nil if the file is not in the index.
func Links(db *sql.DB, root, filePath string) (*FileLinks, error) {
	var outLinks []string
	var normalized string

	for _, cand := range linkCandidates(root, filePath) {
		var raw string
		err := db.QueryRow(`SELECT links_out FROM notes WHERE path = ?`, cand).Scan(&raw)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		if raw != "" && raw != "null" {
			_ = json.Unmarshal([]byte(raw), &outLinks)
		}
		normalized = cand
		break
	}

	if normalized == "" {
		return nil, nil
	}

	rows, err := db.Query(
		`SELECT path FROM notes WHERE links_out LIKE ? AND path != ?`,
		"%"+normalized+"%", normalized,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var backlinks []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		backlinks = append(backlinks, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &FileLinks{Path: normalized, OutLinks: outLinks, Backlinks: backlinks}, nil
}

func linkCandidates(root, filePath string) []string {
	candidates := []string{filePath}
	if abs, err := filepath.Abs(filePath); err == nil {
		candidates = append(candidates, abs)
		if rel, err := filepath.Rel(root, abs); err == nil {
			candidates = append(candidates, filepath.ToSlash(rel))
		}
	}
	candidates = append(candidates, filepath.Join(root, filePath))
	return candidates
}
