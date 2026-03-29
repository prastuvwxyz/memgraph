package config

import (
	"os"
	"path/filepath"
	"testing"
)

// makeDir creates a directory and returns a cleanup func.
func makeDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", path, err)
	}
}

// writeFile writes content to path, creating parent dirs as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// ---------------------------------------------------------------------------
// FindRoot tests
// ---------------------------------------------------------------------------

func TestFindRoot_DirectMatch(t *testing.T) {
	tmp := t.TempDir()
	makeDir(t, filepath.Join(tmp, ".memgraph"))

	got, err := FindRoot(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != tmp {
		t.Errorf("want %s, got %s", tmp, got)
	}
}

func TestFindRoot_WalksUp(t *testing.T) {
	tmp := t.TempDir()
	// .memgraph lives at root, startDir is a deeply nested child
	makeDir(t, filepath.Join(tmp, ".memgraph"))
	deep := filepath.Join(tmp, "a", "b", "c")
	makeDir(t, deep)

	got, err := FindRoot(deep)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != tmp {
		t.Errorf("want %s, got %s", tmp, got)
	}
}

func TestFindRoot_ErrorAtFSRoot(t *testing.T) {
	// Use a temp dir with no .memgraph anywhere in the hierarchy.
	// We can't literally start from "/" in a portable way without risking a
	// real .memgraph being found, so we create a fresh temp tree.
	tmp := t.TempDir()
	// No .memgraph created — walk will hit tmp's parent chain and eventually
	// the filesystem root.
	_, err := FindRoot(tmp)
	if err == nil {
		t.Fatal("expected error when .memgraph not found, got nil")
	}
}

// ---------------------------------------------------------------------------
// Load / defaults tests
// ---------------------------------------------------------------------------

func TestLoad_DefaultsWhenNoConfigFile(t *testing.T) {
	tmp := t.TempDir()
	makeDir(t, filepath.Join(tmp, ".memgraph"))

	ws, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if ws.Config.TopN != 5 {
		t.Errorf("TopN: want 5, got %d", ws.Config.TopN)
	}
	if ws.Config.Format != "table" {
		t.Errorf("Format: want table, got %s", ws.Config.Format)
	}
	if len(ws.Config.Exclude) == 0 {
		t.Error("Exclude: want non-empty defaults")
	}
}

func TestLoad_ProjectConfigOverridesDefaults(t *testing.T) {
	tmp := t.TempDir()
	memgraphDir := filepath.Join(tmp, ".memgraph")
	makeDir(t, memgraphDir)

	writeFile(t, filepath.Join(memgraphDir, "config.toml"), `
top_n = 10
format = "json"
exclude = ["*.log"]
`)

	ws, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if ws.Config.TopN != 10 {
		t.Errorf("TopN: want 10, got %d", ws.Config.TopN)
	}
	if ws.Config.Format != "json" {
		t.Errorf("Format: want json, got %s", ws.Config.Format)
	}
	// New behavior: project exclude list is appended to defaults, not replacing them.
	foundLog := false
	for _, e := range ws.Config.Exclude {
		if e == "*.log" {
			foundLog = true
			break
		}
	}
	if !foundLog {
		t.Errorf("Exclude: expected *.log to be present, got %v", ws.Config.Exclude)
	}
	if len(ws.Config.Exclude) <= 1 {
		t.Errorf("Exclude: expected defaults + *.log, got only %v", ws.Config.Exclude)
	}
}

func TestLoad_ProjectConfigMergesContexts(t *testing.T) {
	tmp := t.TempDir()
	makeDir(t, filepath.Join(tmp, ".memgraph"))

	writeFile(t, filepath.Join(tmp, ".memgraph", "config.toml"), `
[contexts.work]
root = "contexts/electrum"
`)

	ws, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx, ok := ws.Config.Contexts["work"]
	if !ok {
		t.Fatal("contexts.work not found")
	}
	if ctx.Root != "contexts/electrum" {
		t.Errorf("contexts.work.root: want contexts/electrum, got %s", ctx.Root)
	}
}

// ---------------------------------------------------------------------------
// Config merge: global overridden by project
// ---------------------------------------------------------------------------

func TestLoad_ProjectOverridesGlobal(t *testing.T) {
	tmp := t.TempDir()
	makeDir(t, filepath.Join(tmp, ".memgraph"))

	// Fake XDG home by setting XDG_CONFIG_HOME
	fakeXDG := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", fakeXDG)

	// Global config sets top_n = 7 and format = "paths"
	writeFile(t, filepath.Join(fakeXDG, "memgraph", "config.toml"), `
top_n = 7
format = "paths"
`)

	// Project config overrides only top_n
	writeFile(t, filepath.Join(tmp, ".memgraph", "config.toml"), `
top_n = 3
`)

	ws, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Project wins on top_n
	if ws.Config.TopN != 3 {
		t.Errorf("TopN: want 3, got %d", ws.Config.TopN)
	}
	// Global value survives for format (not overridden by project)
	if ws.Config.Format != "paths" {
		t.Errorf("Format: want paths, got %s", ws.Config.Format)
	}
}

// ---------------------------------------------------------------------------
// LoadOrDefault
// ---------------------------------------------------------------------------

func TestLoadOrDefault_NoMemgraph(t *testing.T) {
	tmp := t.TempDir()
	// No .memgraph directory — should not panic or error.
	ws := LoadOrDefault(tmp)
	if ws == nil {
		t.Fatal("got nil workspace")
	}
	if ws.Config.TopN != 5 {
		t.Errorf("TopN: want 5, got %d", ws.Config.TopN)
	}
}

func TestLoadOrDefault_WithMemgraph(t *testing.T) {
	tmp := t.TempDir()
	makeDir(t, filepath.Join(tmp, ".memgraph"))

	ws := LoadOrDefault(tmp)
	if ws.Root != tmp {
		t.Errorf("Root: want %s, got %s", tmp, ws.Root)
	}
}
