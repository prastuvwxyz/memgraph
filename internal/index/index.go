package index

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/prastuvwxyz/memgraph/internal/parse"
	_ "modernc.org/sqlite"
)

// DB wraps a SQLite database connection for the memgraph index.
type DB struct {
	db *sql.DB
}

const schema = `
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

// Open opens (or creates) the index at dbPath.
func Open(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL mode for better concurrent access.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &DB{db: db}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.db.Close()
}

// SqlDB returns the underlying *sql.DB for use by other packages (e.g. rank).
func (db *DB) SqlDB() *sql.DB {
	return db.db
}

// IndexFile upserts one ParsedFile. Skips if checksum unchanged.
// Returns true if the file was actually re-indexed.
func (db *DB) IndexFile(f *parse.ParsedFile) (updated bool, err error) {
	// Check existing checksum.
	var existingChecksum string
	err = db.db.QueryRow(`SELECT checksum FROM notes WHERE path = ?`, f.Path).Scan(&existingChecksum)
	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("query checksum: %w", err)
	}

	if err == nil && existingChecksum == f.Checksum {
		// Checksum unchanged, skip.
		return false, nil
	}

	tagsJSON, err := json.Marshal(f.Tags)
	if err != nil {
		return false, fmt.Errorf("marshal tags: %w", err)
	}

	linksJSON, err := json.Marshal(f.Links)
	if err != nil {
		return false, fmt.Errorf("marshal links: %w", err)
	}

	now := time.Now().Unix()

	_, err = db.db.Exec(`
		INSERT INTO notes (path, title, body, tags, links_out, checksum, last_verified, last_indexed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			title         = excluded.title,
			body          = excluded.body,
			tags          = excluded.tags,
			links_out     = excluded.links_out,
			checksum      = excluded.checksum,
			last_verified = excluded.last_verified,
			last_indexed  = excluded.last_indexed
	`,
		f.Path,
		f.Title,
		f.Body,
		string(tagsJSON),
		string(linksJSON),
		f.Checksum,
		f.LastVerified,
		now,
	)
	if err != nil {
		return false, fmt.Errorf("upsert note: %w", err)
	}

	return true, nil
}

// DeleteFile removes a file from the index by path.
func (db *DB) DeleteFile(path string) error {
	_, err := db.db.Exec(`DELETE FROM notes WHERE path = ?`, path)
	if err != nil {
		return fmt.Errorf("delete note: %w", err)
	}
	return nil
}

// Stats returns count of indexed files and db file size.
func (db *DB) Stats() (fileCount int, err error) {
	err = db.db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&fileCount)
	if err != nil {
		return 0, fmt.Errorf("count notes: %w", err)
	}
	return fileCount, nil
}

// DBPath returns the file path of the database. Used internally.
func (db *DB) dbPath() string {
	// We need to get the db file path for size reporting.
	// This is retrieved via PRAGMA database_list.
	var seq int
	var name, file string
	row := db.db.QueryRow(`PRAGMA database_list`)
	if err := row.Scan(&seq, &name, &file); err != nil {
		return ""
	}
	return file
}

// FileSize returns the size of the database file in bytes.
func (db *DB) FileSize() (int64, error) {
	path := db.dbPath()
	if path == "" {
		return 0, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat db file: %w", err)
	}
	return info.Size(), nil
}

// AllPaths returns all indexed paths (for stale detection).
func (db *DB) AllPaths() ([]string, error) {
	rows, err := db.db.Query(`SELECT path FROM notes`)
	if err != nil {
		return nil, fmt.Errorf("query paths: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan path: %w", err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}
