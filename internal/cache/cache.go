// Package cache is the opt-in SQLite store that mirrors a subset of each
// account's envelopes. Derived state — never authoritative; live verbs
// never read from it (ADR-0003). The storage shape and rationale live in
// ADR-0008.
//
// Every mbx ID written through the Store is canonicalized at the boundary
// so alias-form IDs never end up as distinct rows alongside their canonical
// twins. Reads symmetrically canonicalize lookups.
package cache

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// SchemaVersion is the on-disk schema this binary writes and accepts.
// On a version mismatch Open returns ErrSchemaMismatch — the cache is
// derived, so the caller fix is `mbx cache clear && mbx cache sync`
// rather than a migration (ADR-0008).
const SchemaVersion = "1"

var (
	ErrSchemaMismatch = errors.New("cache schema mismatch")
)

// Store wraps the SQLite *sql.DB and the path it was opened from.
// One DB per mbx invocation; per-account isolation is by row (the
// `account` column on every table) rather than per-file (ADR-0008).
type Store struct {
	db   *sql.DB
	path string
}

// DefaultDir returns the directory mbx looks in for its cache files when
// no explicit cache-dir is given. Resolution mirrors config.DefaultConfigDir
// so the env-var-layering convention stays consistent across mbx's two
// filesystem roots (ADR-0008).
func DefaultDir() (string, error) {
	if v := os.Getenv("MBX_CACHE_DIR"); v != "" {
		return expandHome(v), nil
	}
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(expandHome(v), "mbx"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving user home dir: %w", err)
	}
	return filepath.Join(home, ".cache", "mbx"), nil
}

// ResolveDir returns the effective cache dir: cfgCacheDir wins when
// non-empty (a value the validator already expanded), otherwise the
// env/XDG/home fallback chain via DefaultDir.
func ResolveDir(cfgCacheDir string) (string, error) {
	if cfgCacheDir != "" {
		return cfgCacheDir, nil
	}
	return DefaultDir()
}

// DefaultPath returns <ResolveDir(cfgCacheDir)>/cache.db.
func DefaultPath(cfgCacheDir string) (string, error) {
	dir, err := ResolveDir(cfgCacheDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cache.db"), nil
}

// Open returns a Store backed by the SQLite DB at path. Creates the
// parent directory (0700) if missing, applies the schema on a fresh
// file, and refuses to open on a schema version mismatch.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating cache dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening cache %s: %w", path, err)
	}
	// modernc.org/sqlite doesn't pool connections meaningfully for a
	// single-process CLI; cap at one writer + a few readers to keep the
	// surface predictable.
	db.SetMaxOpenConns(4)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enabling WAL: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	s := &Store{db: db, path: path}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) initSchema() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("creating schema_meta: %w", err)
	}
	var version string
	err := s.db.QueryRow(`SELECT v FROM schema_meta WHERE k='version'`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return s.applyDDL()
	}
	if err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	if version != SchemaVersion {
		return fmt.Errorf("%w: on-disk v%s, binary expects v%s; run `mbx cache clear && mbx cache sync`",
			ErrSchemaMismatch, version, SchemaVersion)
	}
	return nil
}

// applyDDL creates every mbx-owned table and stamps the schema version.
// Idempotent on a clean DB; called by initSchema only when schema_meta
// has no row for k='version'. Each statement is executed individually
// — modernc.org/sqlite's Exec rejects scripts with multiple statements.
func (s *Store) applyDDL() error {
	for _, stmt := range schemaDDL {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("applying schema (%s): %w", firstLine(stmt), err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO schema_meta (k, v) VALUES ('version', ?)`, SchemaVersion); err != nil {
		return fmt.Errorf("stamping schema version: %w", err)
	}
	return nil
}

// schemaDDL is the table set documented in ADR-0008.
var schemaDDL = []string{
	`CREATE TABLE envelopes (
		id            TEXT PRIMARY KEY,
		account       TEXT NOT NULL,
		thread_id     TEXT,
		from_addr     TEXT,
		to_addrs      TEXT,
		cc_addrs      TEXT,
		subject       TEXT,
		date          INTEGER NOT NULL,
		snippet       TEXT,
		has_attach    INTEGER NOT NULL,
		provider      TEXT NOT NULL,
		gmail_labels  TEXT,
		synced_at     INTEGER NOT NULL
	)`,
	`CREATE INDEX envelopes_account_date ON envelopes(account, date DESC)`,
	`CREATE INDEX envelopes_thread       ON envelopes(thread_id)`,
	`CREATE TABLE envelope_flags (
		envelope_id TEXT NOT NULL REFERENCES envelopes(id) ON DELETE CASCADE ON UPDATE CASCADE,
		flag        TEXT NOT NULL,
		PRIMARY KEY (envelope_id, flag)
	)`,
	`CREATE TABLE envelope_folders (
		envelope_id TEXT NOT NULL REFERENCES envelopes(id) ON DELETE CASCADE ON UPDATE CASCADE,
		folder      TEXT NOT NULL,
		PRIMARY KEY (envelope_id, folder)
	)`,
	`CREATE TABLE messages (
		id           TEXT PRIMARY KEY REFERENCES envelopes(id) ON DELETE CASCADE ON UPDATE CASCADE,
		body_plain   TEXT,
		body_html    TEXT,
		body_source  TEXT,
		headers_json TEXT,
		synced_at    INTEGER NOT NULL
	)`,
	`CREATE TABLE sync_state (
		account      TEXT NOT NULL,
		folder       TEXT NOT NULL,
		uidvalidity  INTEGER,
		last_sync_at INTEGER NOT NULL,
		envelope_n   INTEGER NOT NULL,
		PRIMARY KEY (account, folder)
	)`,
}

func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return strings.TrimSpace(before)
	}
	return s
}

func expandHome(p string) string {
	if p == "" || !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}
