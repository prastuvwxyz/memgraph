package config

import (
	"path/filepath"
	"strings"
)

// NSResolver maps a root-relative file path to its namespace.
type NSResolver func(relPath string) string

// ResolveNS builds an NSResolver from a namespace prefix map and a fallback.
// Priority: longest matching prefix in nsMap > fallback > "" (global).
func ResolveNS(nsMap map[string][]string, fallback string) NSResolver {
	if len(nsMap) == 0 {
		return func(string) string { return fallback }
	}

	type entry struct {
		ns     string
		prefix string
	}
	var entries []entry
	for ns, paths := range nsMap {
		for _, p := range paths {
			prefix := filepath.ToSlash(filepath.Clean(p))
			if prefix == "." {
				continue
			}
			entries = append(entries, entry{ns, prefix + "/"})
		}
	}

	return func(rel string) string {
		relSlash := filepath.ToSlash(rel)
		for _, e := range entries {
			if strings.HasPrefix(relSlash+"/", e.prefix) || strings.HasPrefix(relSlash, e.prefix) {
				return e.ns
			}
		}
		return fallback
	}
}
