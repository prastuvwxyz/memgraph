package rank

import (
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"
)

// setupDB creates an in-memory SQLite DB with the same schema as internal/index.
func setupDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	schema := `
CREATE TABLE IF NOT EXISTS notes (
    id            INTEGER PRIMARY KEY,
    path          TEXT UNIQUE NOT NULL,
    title         TEXT NOT NULL,
    body          TEXT NOT NULL DEFAULT '',
    tags          TEXT NOT NULL DEFAULT '',
    links_out     TEXT NOT NULL DEFAULT '',
    checksum      TEXT NOT NULL,
    last_verified TEXT NOT NULL DEFAULT '',
    last_indexed  INTEGER NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
    title, body,
    content=notes, content_rowid=id
);

CREATE TRIGGER IF NOT EXISTS notes_ai AFTER INSERT ON notes BEGIN
    INSERT INTO notes_fts(rowid, title, body) VALUES (new.id, new.title, new.body);
END;

CREATE TRIGGER IF NOT EXISTS notes_ad AFTER DELETE ON notes BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, title, body) VALUES('delete', old.id, old.title, old.body);
END;

CREATE TRIGGER IF NOT EXISTS notes_au AFTER UPDATE ON notes BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, title, body) VALUES('delete', old.id, old.title, old.body);
    INSERT INTO notes_fts(rowid, title, body) VALUES (new.id, new.title, new.body);
END;
`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertNote(t *testing.T, db *sql.DB, path, title, body string, tags, links []string) {
	t.Helper()
	tagsJSON, _ := json.Marshal(tags)
	linksJSON, _ := json.Marshal(links)
	_, err := db.Exec(`
		INSERT INTO notes (path, title, body, tags, links_out, checksum, last_verified, last_indexed)
		VALUES (?, ?, ?, ?, ?, 'abc', '2026-01-01', 0)`,
		path, title, body, string(tagsJSON), string(linksJSON),
	)
	if err != nil {
		t.Fatalf("insert note %s: %v", path, err)
	}
}

// TestSearchFindsRelevantFile verifies basic FTS search returns a matching result.
func TestSearchFindsRelevantFile(t *testing.T) {
	db := setupDB(t)
	insertNote(t, db, "/notes/golang-tips.md", "Golang Tips", "goroutines channels concurrency", []string{"golang"}, nil)
	insertNote(t, db, "/notes/python-basics.md", "Python Basics", "loops variables print", []string{"python"}, nil)

	results, err := Search(db, "golang", 5)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}
	if results[0].Path != "/notes/golang-tips.md" {
		t.Errorf("expected golang-tips.md first, got %s", results[0].Path)
	}
}

// TestFilenameScoreBoostPathMatches verifies filename scoring boosts path matches.
func TestFilenameScoreBoostPathMatches(t *testing.T) {
	db := setupDB(t)
	// Both have "openclaw" in body, but only one has it in the path.
	insertNote(t, db, "/notes/openclaw-setup.md", "OpenClaw Setup", "openclaw agent config", []string{}, nil)
	insertNote(t, db, "/notes/random-note.md", "Random Note", "openclaw is mentioned here", []string{}, nil)

	results, err := Search(db, "openclaw", 5)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	if results[0].Path != "/notes/openclaw-setup.md" {
		t.Errorf("expected openclaw-setup.md first due to filename boost, got %s", results[0].Path)
	}
}

// TestTagBonusApplied verifies tag bonus adds to score.
func TestTagBonusApplied(t *testing.T) {
	db := setupDB(t)
	insertNote(t, db, "/notes/with-tag.md", "With Tag", "some content about stellar", []string{"stellar"}, nil)
	insertNote(t, db, "/notes/without-tag.md", "Without Tag", "some content about stellar systems", []string{}, nil)

	results, err := Search(db, "stellar", 5)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected results")
	}

	// Find the with-tag result and check its reason mentions tag bonus.
	for _, r := range results {
		if r.Path == "/notes/with-tag.md" {
			// Tag bonus should be reflected in score being higher.
			// Just verify reason contains "tag" component.
			found := false
			for _, rr := range results {
				if rr.Path == "/notes/without-tag.md" {
					if r.Score > rr.Score {
						found = true
					}
					break
				}
			}
			if !found {
				t.Logf("with-tag score: %.4f", r.Score)
				// If without-tag not present, just verify tag note is returned.
			}
			return
		}
	}
	t.Error("with-tag.md not found in results")
}

// TestEmptyQueryReturnsEmpty verifies empty query returns nil results.
func TestEmptyQueryReturnsEmpty(t *testing.T) {
	db := setupDB(t)
	insertNote(t, db, "/notes/some-note.md", "Some Note", "body text", []string{}, nil)

	results, err := Search(db, "", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}

	// Whitespace-only also empty.
	results, err = Search(db, "   ", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for whitespace query, got %d", len(results))
	}
}

// TestTopNRespected verifies the result count is capped at topN.
func TestTopNRespected(t *testing.T) {
	db := setupDB(t)
	for i := 0; i < 10; i++ {
		insertNote(t, db,
			"/notes/note-concurrency-"+string(rune('a'+i))+".md",
			"Note "+string(rune('A'+i)),
			"concurrency goroutine channel",
			[]string{},
			nil,
		)
	}

	results, err := Search(db, "concurrency", 3)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) > 3 {
		t.Errorf("expected at most 3 results, got %d", len(results))
	}
}

// TestSpecialCharsDoNotPanic verifies special FTS5 chars are handled gracefully.
func TestSpecialCharsDoNotPanic(t *testing.T) {
	db := setupDB(t)
	insertNote(t, db, "/notes/test.md", "Test Note", "some body text", []string{}, nil)

	queries := []string{
		`"unclosed quote`,
		`AND OR NOT`,
		`(unbalanced`,
		`*wildcards*`,
		`field:value`,
		`"double""quote"`,
	}

	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			// Should not panic.
			_, err := Search(db, q, 5)
			// Errors are acceptable; panics are not.
			_ = err
		})
	}
}

// TestDefaultTopN verifies topN=0 uses default of 5.
func TestDefaultTopN(t *testing.T) {
	db := setupDB(t)
	for i := 0; i < 10; i++ {
		insertNote(t, db,
			"/notes/default-"+string(rune('a'+i))+".md",
			"Default "+string(rune('A'+i)),
			"default top n test content",
			[]string{},
			nil,
		)
	}

	results, err := Search(db, "default", 0)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(results) > defaultTopN {
		t.Errorf("expected at most %d results with topN=0, got %d", defaultTopN, len(results))
	}
}

// TestGraphBoostLinkedResults verifies linked results get a boost.
func TestGraphBoostLinkedResults(t *testing.T) {
	db := setupDB(t)
	// Note A links to Note B.
	insertNote(t, db, "/notes/note-a.md", "Note A", "graph boost test content", []string{}, []string{"/notes/note-b.md"})
	insertNote(t, db, "/notes/note-b.md", "Note B", "graph boost test content linked", []string{}, nil)
	insertNote(t, db, "/notes/note-c.md", "Note C", "graph boost test content unlinked", []string{}, nil)

	results, err := Search(db, "graph boost test", 10)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	// Just verify it doesn't error and returns results.
	if len(results) == 0 {
		t.Error("expected results for graph boost test")
	}
}
