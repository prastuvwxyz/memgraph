package index

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/prastuvwxyz/memgraph/internal/parse"
)

// loadIgnoreFile reads a .memgraphignore-style file and returns its patterns.
// Lines that are blank or start with # are skipped.
func loadIgnoreFile(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// Walk indexes all markdown files under rootDir, skipping exclude patterns.
// Returns count of files indexed (updated) and total files visited.
// If verbose=true, prints each updated file path to stderr.
func Walk(db *DB, rootDir string, exclude []string, verbose bool) (updated, total int, err error) {
	// Merge .memgraphignore patterns (if present) into exclude list.
	if extra := loadIgnoreFile(filepath.Join(rootDir, ".memgraphignore")); len(extra) > 0 {
		seen := make(map[string]bool, len(exclude))
		for _, e := range exclude {
			seen[e] = true
		}
		for _, e := range extra {
			if !seen[e] {
				exclude = append(exclude, e)
			}
		}
	}

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
		visited[rel] = true

		f, parseErr := parse.ParseFile(path)
		if parseErr != nil {
			// Log and skip unparseable files.
			fmt.Fprintf(os.Stderr, "parse error %s: %v\n", rel, parseErr)
			return nil
		}
		f.Path = rel // store relative path for portable indexing

		// Normalize outbound links from file-relative to vault-root-relative paths.
		// e.g. from agents/intel/AGENTS.md, "../../knowledge/x.md" → "knowledge/x.md"
		fileDir := filepath.Dir(rel)
		for i, link := range f.Links {
			normalized := filepath.ToSlash(filepath.Clean(filepath.Join(fileDir, link)))
			if normalized != "." {
				f.Links[i] = normalized
			}
		}

		didUpdate, indexErr := db.IndexFile(f)
		if indexErr != nil {
			return fmt.Errorf("index %s: %w", rel, indexErr)
		}

		if didUpdate {
			updated++
			if verbose {
				fmt.Fprintf(os.Stderr, "indexed: %s\n", rel)
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
		// Strip trailing slash from directory patterns (gitignore convention).
		p := strings.TrimSuffix(pattern, "/")
		// Match against full relative path.
		if matched, _ := filepath.Match(p, rel); matched {
			return true
		}
		// Match against each component.
		for _, component := range components {
			if matched, _ := filepath.Match(p, component); matched {
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
