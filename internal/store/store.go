// Package store implements SQLite persistence for pastes using the pure-Go
// modernc.org/sqlite driver (no CGO).
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a paste does not exist or has expired.
var ErrNotFound = errors.New("paste not found")

// ErrConflict is returned when a paste ID collides with an existing row.
var ErrConflict = errors.New("paste id already exists")

// Paste is a stored paste row.
type Paste struct {
	ID              string
	Content         []byte
	Format          string // "text", "markdown" or "code"
	Language        string // highlight.js language; "" = auto-detect
	Burn            bool   // destroy after first successful content retrieval
	DeleteTokenHash string // hex SHA-256 of the delete token (plaintext never stored)
	CreatedAt       int64  // unix seconds
	ExpiresAt       *int64 // unix seconds; nil = never expires
}

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and ensures
// the schema exists. The special path ":memory:" opens a private in-memory
// database (useful for tests). WAL mode and a busy timeout are enabled.
func Open(path string) (*Store, error) {
	dsn := path
	inMem := path == ":memory:"
	if inMem {
		// A shared-cache DSN plus a single connection gives a stable
		// in-memory database across queries.
		dsn = "file:paste_mem?mode=memory&cache=shared"
	} else if !strings.HasPrefix(path, "file:") {
		// Regular file path: create the parent directory and enable WAL and
		// busy_timeout via DSN pragmas so every pooled connection gets them.
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
		dsn = "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if inMem {
		db.SetMaxOpenConns(1)
	}

	const schema = `
CREATE TABLE IF NOT EXISTS pastes (
	id                TEXT PRIMARY KEY,
	content           BLOB NOT NULL,
	format            TEXT NOT NULL CHECK (format IN ('text','markdown','code')),
	language          TEXT NOT NULL DEFAULT '',
	burn_after_read   INTEGER NOT NULL DEFAULT 0,
	delete_token_hash TEXT NOT NULL,
	created_at        INTEGER NOT NULL,
	expires_at        INTEGER
);
CREATE INDEX IF NOT EXISTS idx_pastes_expires_at ON pastes (expires_at);
`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies database connectivity.
func (s *Store) Ping() error { return s.db.Ping() }

// Create inserts a new paste. It returns ErrConflict if the ID already
// exists (the caller should retry with a fresh ID).
func (s *Store) Create(p *Paste) error {
	_, err := s.db.Exec(
		`INSERT INTO pastes
			(id, content, format, language, burn_after_read, delete_token_hash, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Content, p.Format, p.Language, boolToInt(p.Burn), p.DeleteTokenHash, p.CreatedAt, p.ExpiresAt,
	)
	if err != nil {
		// modernc.org/sqlite reports unique violations in the message; match
		// on the stable constraint text so we can retry with a new ID.
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrConflict
		}
		return err
	}
	return nil
}

// Get returns the paste with the given id. Expired pastes are treated as
// missing.
func (s *Store) Get(id string) (*Paste, error) {
	p := &Paste{}
	var burn int
	var expires sql.NullInt64
	err := s.db.QueryRow(
		`SELECT id, content, format, language, burn_after_read, delete_token_hash, created_at, expires_at
		 FROM pastes
		 WHERE id = ? AND (expires_at IS NULL OR expires_at > ?)`,
		id, time.Now().Unix(),
	).Scan(&p.ID, &p.Content, &p.Format, &p.Language, &burn, &p.DeleteTokenHash, &p.CreatedAt, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Burn = burn != 0
	if expires.Valid {
		v := expires.Int64
		p.ExpiresAt = &v
	}
	return p, nil
}

// DeleteByID removes a paste unconditionally (used by burn-after-read).
func (s *Store) DeleteByID(id string) error {
	_, err := s.db.Exec(`DELETE FROM pastes WHERE id = ?`, id)
	return err
}

// DeleteWithToken removes the paste only if the (already hashed) token
// matches. A missing paste and a wrong token are indistinguishable
// (ErrNotFound) so the API never leaks a paste's existence.
func (s *Store) DeleteWithToken(id, tokenHash string) error {
	res, err := s.db.Exec(`DELETE FROM pastes WHERE id = ? AND delete_token_hash = ?`, id, tokenHash)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteExpired removes all pastes whose expiry has passed and returns the
// number of rows deleted. Pastes with NULL expires_at are never removed.
func (s *Store) DeleteExpired(now int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM pastes WHERE expires_at IS NOT NULL AND expires_at <= ?`, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
