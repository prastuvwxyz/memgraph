package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/prastuvwxyz/memgraph/internal/chunk"
	"github.com/prastuvwxyz/memgraph/internal/embed"
	"github.com/prastuvwxyz/memgraph/internal/parse"
	"github.com/google/uuid"
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
    namespace     TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS chunks (
    id          TEXT UNIQUE NOT NULL,
    path        TEXT NOT NULL,
    namespace   TEXT NOT NULL DEFAULT '',
    chunk_index INTEGER NOT NULL,
    content     TEXT NOT NULL,
    token_count INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chunks_path ON chunks(path);

CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    content,
    content=chunks, content_rowid=rowid
);

CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, content) VALUES (new.rowid, new.content);
END;

CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, content) VALUES('delete', old.rowid, old.content);
END;

CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, content) VALUES('delete', old.rowid, old.content);
    INSERT INTO chunks_fts(rowid, content) VALUES (new.rowid, new.content);
END;

CREATE TABLE IF NOT EXISTS chunk_vectors (
    chunk_id  TEXT PRIMARY KEY,
    embedding BLOB NOT NULL
);
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

	// Migrations for existing databases (errors ignored — column already exists).
	for _, m := range []string{
		`ALTER TABLE notes ADD COLUMN namespace TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE chunks ADD COLUMN namespace TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE notes ADD COLUMN consolidated_at INTEGER DEFAULT NULL`,
	} {
		db.Exec(m)
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
// namespace tags the file for per-agent isolation (empty = global).
// emb is optional — when non-nil, chunks are also embedded for vector search.
// Returns true if the file was actually re-indexed.
func (db *DB) IndexFile(ctx context.Context, f *parse.ParsedFile, namespace string, emb embed.Embedder) (updated bool, err error) {
	// Check existing checksum.
	var existingChecksum string
	err = db.db.QueryRow(`SELECT checksum FROM notes WHERE path = ?`, f.Path).Scan(&existingChecksum)
	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("query checksum: %w", err)
	}

	if err == nil && existingChecksum == f.Checksum {
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
		INSERT INTO notes (path, namespace, title, body, tags, links_out, checksum, last_verified, last_indexed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			namespace     = excluded.namespace,
			title         = excluded.title,
			body          = excluded.body,
			tags          = excluded.tags,
			links_out     = excluded.links_out,
			checksum      = excluded.checksum,
			last_verified = excluded.last_verified,
			last_indexed  = excluded.last_indexed
	`,
		f.Path, namespace, f.Title, f.Body, string(tagsJSON), string(linksJSON),
		f.Checksum, f.LastVerified, now,
	)
	if err != nil {
		return false, fmt.Errorf("upsert note: %w", err)
	}

	if err := db.indexChunks(ctx, f.Path, namespace, f.FullBody, emb); err != nil {
		fmt.Fprintf(os.Stderr, "chunk %s: %v\n", f.Path, err)
	}

	return true, nil
}

// indexChunks deletes old chunks for path and inserts fresh ones.
// If emb is non-nil, also stores vector embeddings.
func (db *DB) indexChunks(ctx context.Context, path, namespace, fullBody string, emb embed.Embedder) error {
	// Delete stale chunks (triggers handle FTS cleanup).
	if _, err := db.db.ExecContext(ctx, `DELETE FROM chunks WHERE path = ?`, path); err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}

	chunks := chunk.Split(fullBody, path)
	if len(chunks) == 0 {
		return nil
	}

	// Insert chunks in a transaction.
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	ids := make([]string, len(chunks))
	for i, c := range chunks {
		id := uuid.New().String()
		ids[i] = id
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO chunks (id, path, namespace, chunk_index, content, token_count) VALUES (?, ?, ?, ?, ?, ?)`,
			id, path, namespace, c.ChunkIndex, c.Content, c.TokenCount); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert chunk: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if emb == nil {
		return nil
	}

	// Embed in batches of 100.
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	const batchSize = 100
	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := emb.Embed(ctx, texts[start:end])
		if err != nil {
			return fmt.Errorf("embed batch: %w", err)
		}
		for j, vec := range vecs {
			blob, err := embed.EncodeEmbedding(vec)
			if err != nil {
				continue
			}
			if _, err := db.db.ExecContext(ctx,
				`INSERT OR REPLACE INTO chunk_vectors (chunk_id, embedding) VALUES (?, ?)`,
				ids[start+j], blob); err != nil {
				fmt.Fprintf(os.Stderr, "store vector %s: %v\n", ids[start+j], err)
			}
		}
	}
	return nil
}

// DeleteFile removes a file and its chunks from the index.
func (db *DB) DeleteFile(path string) error {
	if _, err := db.db.Exec(`DELETE FROM notes WHERE path = ?`, path); err != nil {
		return fmt.Errorf("delete note: %w", err)
	}
	// Delete chunks; triggers handle FTS cleanup; chunk_vectors cascade via app logic.
	rows, err := db.db.Query(`SELECT id FROM chunks WHERE path = ?`, path)
	if err == nil {
		var ids []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			db.db.Exec(`DELETE FROM chunk_vectors WHERE chunk_id = ?`, id)
		}
	}
	if _, err := db.db.Exec(`DELETE FROM chunks WHERE path = ?`, path); err != nil {
		return fmt.Errorf("delete chunks: %w", err)
	}
	return nil
}

// MarkConsolidated sets consolidated_at = now for the given paths.
// Returns count of paths actually updated.
func (db *DB) MarkConsolidated(paths []string) (int, error) {
	now := time.Now().Unix()
	var n int
	for _, p := range paths {
		res, err := db.db.Exec(`UPDATE notes SET consolidated_at = ? WHERE path = ?`, now, p)
		if err != nil {
			return n, fmt.Errorf("mark %s: %w", p, err)
		}
		rows, _ := res.RowsAffected()
		n += int(rows)
	}
	return n, nil
}

// UnmarkConsolidated clears consolidated_at for the given paths.
func (db *DB) UnmarkConsolidated(paths []string) (int, error) {
	var n int
	for _, p := range paths {
		res, err := db.db.Exec(`UPDATE notes SET consolidated_at = NULL WHERE path = ?`, p)
		if err != nil {
			return n, fmt.Errorf("unmark %s: %w", p, err)
		}
		rows, _ := res.RowsAffected()
		n += int(rows)
	}
	return n, nil
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
