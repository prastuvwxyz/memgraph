package index

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/prastuvwxyz/memgraph/internal/parse"
)

// Walk indexes all markdown files under rootDir, skipping exclude patterns.
// Returns count of files indexed (updated) and total files visited.
// If verbose=true, prints each updated file path to stderr.
func Walk(db *DB, rootDir string, exclude []string, verbose bool) (updated, total int, err error) {
	visited := make(map[string]bool)

	walkErr := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Only process .md files.
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}

		// Compute relative path for exclusion matching.
		rel, relErr := filepath.Rel(rootDir, path)
		if relErr != nil {
			rel = path
		}

		// Check each exclude pattern against each path component and full relative path.
		if matchesExclude(rel, exclude) {
			return nil
		}

		total++
		visited[path] = true

		f, parseErr := parse.ParseFile(path)
		if parseErr != nil {
			// Log and skip unparseable files.
			fmt.Fprintf(os.Stderr, "parse error %s: %v\n", path, parseErr)
			return nil
		}

		didUpdate, indexErr := db.IndexFile(f)
		if indexErr != nil {
			return fmt.Errorf("index %s: %w", path, indexErr)
		}

		if didUpdate {
			updated++
			if verbose {
				fmt.Fprintf(os.Stderr, "indexed: %s\n", path)
			}
		}

		return nil
	})

	if walkErr != nil {
		return updated, total, walkErr
	}

	// Stale cleanup: remove indexed paths that no longer exist on disk.
	allPaths, err := db.AllPaths()
	if err != nil {
		return updated, total, fmt.Errorf("get all paths: %w", err)
	}

	for _, p := range allPaths {
		if !visited[p] {
			if delErr := db.DeleteFile(p); delErr != nil {
				fmt.Fprintf(os.Stderr, "delete stale %s: %v\n", p, delErr)
			} else if verbose {
				fmt.Fprintf(os.Stderr, "removed stale: %s\n", p)
			}
		}
	}

	return updated, total, nil
}

// matchesExclude returns true if the relative path matches any exclude pattern.
// Checks each path component individually and the full relative path.
func matchesExclude(rel string, exclude []string) bool {
	if len(exclude) == 0 {
		return false
	}

	// Split path into components.
	components := splitPathComponents(rel)

	for _, pattern := range exclude {
		// Match against full relative path.
		if matched, _ := filepath.Match(pattern, rel); matched {
			return true
		}
		// Match against each component.
		for _, component := range components {
			if matched, _ := filepath.Match(pattern, component); matched {
				return true
			}
		}
	}

	return false
}

// splitPathComponents splits a path into its individual components.
func splitPathComponents(path string) []string {
	var parts []string
	for path != "" {
		dir, file := filepath.Split(path)
		if file != "" {
			parts = append(parts, file)
		}
		path = filepath.Clean(dir)
		if path == "." || path == "/" {
			break
		}
	}
	return parts
}
