package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// DefaultExclude contains the default glob patterns skipped during indexing.
var DefaultExclude = []string{
	".git",
	".memgraph",
	"node_modules",
	"vendor",
	"*.pdf",
	"*.csv",
	"*.png",
	"*.jpg",
	"*.jpeg",
	"*.gif",
	"*.svg",
}

// ContextDef describes a named context.
type ContextDef struct {
	Root string `toml:"root"`
}

// Config holds memgraph configuration values.
type Config struct {
	TopN     int                    `toml:"top_n"`
	Format   string                 `toml:"format"`
	Exclude  []string               `toml:"exclude"`
	Contexts map[string]ContextDef  `toml:"contexts"`
}

// Workspace is the loaded configuration together with the detected repo root.
type Workspace struct {
	Root   string // absolute path to directory containing .memgraph/
	Config Config
}

// xdgConfigHome returns the XDG config home directory, honouring the
// XDG_CONFIG_HOME environment variable so tests can override it.
func xdgConfigHome() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config")
}

// defaults returns a Config pre-filled with built-in defaults.
func defaults() Config {
	excl := make([]string, len(DefaultExclude))
	copy(excl, DefaultExclude)
	return Config{
		TopN:    5,
		Format:  "table",
		Exclude: excl,
	}
}

// FindRoot walks up from startDir looking for a directory that contains a
// ".memgraph/" subdirectory. It returns the path of that directory.
// An error is returned when the filesystem root is reached without finding one.
func FindRoot(startDir string) (string, error) {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}

	dir := abs
	for {
		candidate := filepath.Join(dir, ".memgraph")
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// reached filesystem root
			return "", errors.New("config: no .memgraph/ directory found (walked to filesystem root)")
		}
		dir = parent
	}
}

// mergeFile decodes a TOML file at path into dst, overriding only fields that
// are explicitly set. Non-zero scalar values and non-nil slices win.
func mergeFile(dst *Config, path string) error {
	var src Config
	if _, err := toml.DecodeFile(path, &src); err != nil {
		return err
	}

	if src.TopN != 0 {
		dst.TopN = src.TopN
	}
	if src.Format != "" {
		dst.Format = src.Format
	}
	if src.Exclude != nil {
		dst.Exclude = src.Exclude
	}
	if src.Contexts != nil {
		if dst.Contexts == nil {
			dst.Contexts = make(map[string]ContextDef)
		}
		for k, v := range src.Contexts {
			dst.Contexts[k] = v
		}
	}
	return nil
}

// Load finds the workspace root and loads configuration with the following
// merge order (later entries win):
//
//  1. Built-in defaults
//  2. Global config: ~/.config/memgraph/config.toml  (XDG)
//  3. Project config: {root}/.memgraph/config.toml
func Load(startDir string) (*Workspace, error) {
	root, err := FindRoot(startDir)
	if err != nil {
		return nil, err
	}

	cfg := defaults()

	// 1. Global config via XDG (resolve dynamically so tests can override XDG_CONFIG_HOME)
	globalPath := filepath.Join(xdgConfigHome(), "memgraph", "config.toml")
	if _, err := os.Stat(globalPath); err == nil {
		if mergeErr := mergeFile(&cfg, globalPath); mergeErr != nil {
			return nil, mergeErr
		}
	}

	// 2. Project config
	projectPath := filepath.Join(root, ".memgraph", "config.toml")
	if _, err := os.Stat(projectPath); err == nil {
		if mergeErr := mergeFile(&cfg, projectPath); mergeErr != nil {
			return nil, mergeErr
		}
	}

	return &Workspace{Root: root, Config: cfg}, nil
}

// LoadOrDefault behaves like Load but never returns an error. If no
// .memgraph/ root is found, startDir is used as the workspace root with
// built-in defaults. Useful for commands that work without init.
func LoadOrDefault(startDir string) *Workspace {
	ws, err := Load(startDir)
	if err != nil {
		abs, absErr := filepath.Abs(startDir)
		if absErr != nil {
			abs = startDir
		}
		return &Workspace{Root: abs, Config: defaults()}
	}
	return ws
}
