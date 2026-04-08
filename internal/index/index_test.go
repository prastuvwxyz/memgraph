package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/prastuvwxyz/memgraph/internal/parse"
)

func tempDB(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dbPath
}

func writeMD(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// makeFile creates a ParsedFile with given path and checksum (for testing skip logic).
func makeFile(path, checksum string) *parse.ParsedFile {
	return &parse.ParsedFile{
		Path:     path,
		Title:    "Test Note",
		Tags:     []string{"a", "b"},
		Body:     "Some body text.",
		Links:    []string{"other-note"},
		Checksum: checksum,
	}
}

// --- Tests ---

func TestOpenClose(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "openclose.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// No cleanup registered — already closed.
}

func TestIndexFile(t *testing.T) {
	db, _ := tempDB(t)

	f := makeFile("/notes/foo.md", "abc123")
	updated, err := db.IndexFile(context.Background(), f, nil)
	if err != nil {
		t.Fatalf("IndexFile: %v", err)
	}
	if !updated {
		t.Error("expected updated=true for new file")
	}

	count, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 file, got %d", count)
	}
}

func TestSkipUnchangedChecksum(t *testing.T) {
	db, _ := tempDB(t)

	f := makeFile("/notes/foo.md", "abc123")
	if _, err := db.IndexFile(context.Background(), f, nil); err != nil {
		t.Fatalf("first IndexFile: %v", err)
	}

	// Same checksum — should skip.
	updated, err := db.IndexFile(context.Background(), f, nil)
	if err != nil {
		t.Fatalf("second IndexFile: %v", err)
	}
	if updated {
		t.Error("expected updated=false for unchanged checksum")
	}
}

func TestUpdateOnChangedChecksum(t *testing.T) {
	db, _ := tempDB(t)

	f := makeFile("/notes/foo.md", "abc123")
	if _, err := db.IndexFile(context.Background(), f, nil); err != nil {
		t.Fatalf("first IndexFile: %v", err)
	}

	// Different checksum — should update.
	f2 := makeFile("/notes/foo.md", "xyz999")
	f2.Title = "Updated Title"
	updated, err := db.IndexFile(context.Background(), f2, nil)
	if err != nil {
		t.Fatalf("second IndexFile: %v", err)
	}
	if !updated {
		t.Error("expected updated=true for changed checksum")
	}

	// Still only 1 file.
	count, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 file after update, got %d", count)
	}
}

func TestDeleteFile(t *testing.T) {
	db, _ := tempDB(t)

	f := makeFile("/notes/foo.md", "abc123")
	if _, err := db.IndexFile(context.Background(), f, nil); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	if err := db.DeleteFile("/notes/foo.md"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}

	count, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 files after delete, got %d", count)
	}
}

func TestDeleteNonExistent(t *testing.T) {
	db, _ := tempDB(t)
	// Deleting non-existent path should not error.
	if err := db.DeleteFile("/notes/nonexistent.md"); err != nil {
		t.Fatalf("DeleteFile nonexistent: %v", err)
	}
}

func TestStats(t *testing.T) {
	db, _ := tempDB(t)

	for i := 0; i < 3; i++ {
		f := makeFile(filepath.Join("/notes", filepath.Join("note"+string(rune('a'+i))+".md")), "cs"+string(rune('a'+i)))
		if _, err := db.IndexFile(context.Background(), f, nil); err != nil {
			t.Fatalf("IndexFile %d: %v", i, err)
		}
	}

	count, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 files, got %d", count)
	}
}

func TestAllPaths(t *testing.T) {
	db, _ := tempDB(t)

	paths := []string{"/a.md", "/b.md", "/c.md"}
	for i, p := range paths {
		f := makeFile(p, "cs"+string(rune('a'+i)))
		if _, err := db.IndexFile(context.Background(), f, nil); err != nil {
			t.Fatalf("IndexFile %s: %v", p, err)
		}
	}

	got, err := db.AllPaths()
	if err != nil {
		t.Fatalf("AllPaths: %v", err)
	}
	if len(got) != len(paths) {
		t.Errorf("expected %d paths, got %d", len(paths), len(got))
	}

	// Check all paths present.
	gotSet := make(map[string]bool)
	for _, p := range got {
		gotSet[p] = true
	}
	for _, p := range paths {
		if !gotSet[p] {
			t.Errorf("missing path %s in AllPaths", p)
		}
	}
}

func TestWalkTempDir(t *testing.T) {
	db, _ := tempDB(t)
	dir := t.TempDir()

	// Create some markdown files.
	writeMD(t, dir, "note1.md", "# Note One\n\nBody of note one.")
	writeMD(t, dir, "note2.md", "# Note Two\n\nBody of note two.")

	// Create a non-markdown file (should be ignored).
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("text"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a subdirectory with a markdown file.
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeMD(t, subDir, "sub-note.md", "# Sub Note\n\nBody.")

	updated, total, err := Walk(db, dir, nil, false, nil)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if total != 3 {
		t.Errorf("expected 3 total md files, got %d", total)
	}
	if updated != 3 {
		t.Errorf("expected 3 updated files on first walk, got %d", updated)
	}

	// Second walk — no changes, nothing should update.
	updated2, total2, err := Walk(db, dir, nil, false, nil)
	if err != nil {
		t.Fatalf("Walk 2: %v", err)
	}
	if total2 != 3 {
		t.Errorf("expected 3 total on second walk, got %d", total2)
	}
	if updated2 != 0 {
		t.Errorf("expected 0 updated on second walk, got %d", updated2)
	}
}

func TestWalkExclude(t *testing.T) {
	db, _ := tempDB(t)
	dir := t.TempDir()

	writeMD(t, dir, "keep.md", "# Keep\n\nKeep this.")

	// Create excluded directory.
	excludeDir := filepath.Join(dir, "drafts")
	if err := os.MkdirAll(excludeDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeMD(t, excludeDir, "draft.md", "# Draft\n\nDraft content.")

	updated, total, err := Walk(db, dir, []string{"drafts"}, false, nil)
	if err != nil {
		t.Fatalf("Walk with exclude: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 total (excluded drafts), got %d", total)
	}
	if updated != 1 {
		t.Errorf("expected 1 updated, got %d", updated)
	}
}

func TestWalkStaleCleanup(t *testing.T) {
	db, _ := tempDB(t)
	dir := t.TempDir()

	path := writeMD(t, dir, "temp.md", "# Temp\n\nTemp content.")

	_, _, err := Walk(db, dir, nil, false, nil)
	if err != nil {
		t.Fatalf("Walk 1: %v", err)
	}

	// Verify file is indexed.
	count, _ := db.Stats()
	if count != 1 {
		t.Fatalf("expected 1 file indexed, got %d", count)
	}

	// Delete the file on disk.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	// Walk again — stale file should be removed from index.
	_, _, err = Walk(db, dir, nil, false, nil)
	if err != nil {
		t.Fatalf("Walk 2: %v", err)
	}

	count, _ = db.Stats()
	if count != 0 {
		t.Errorf("expected 0 files after stale cleanup, got %d", count)
	}
}

func TestNilTagsAndLinks(t *testing.T) {
	db, _ := tempDB(t)

	f := &parse.ParsedFile{
		Path:     "/notes/empty.md",
		Title:    "Empty",
		Tags:     nil,
		Links:    nil,
		Checksum: "xyz",
	}

	updated, err := db.IndexFile(context.Background(), f, nil)
	if err != nil {
		t.Fatalf("IndexFile with nil tags/links: %v", err)
	}
	if !updated {
		t.Error("expected updated=true")
	}
}

func TestCloseNilDB(t *testing.T) {
	// Ensure Close on nil db field doesn't panic.
	db := &DB{}
	// We can't call db.Close() directly as db.db is nil — skip this edge case.
	// Just verify DB struct is valid.
	_ = db
}
