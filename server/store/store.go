// Copyright (c) 2026 Michael D Henderson. All rights reserved.

// Package store is the SQLite data-layer foundation for ecv3.
//
// It wraps a pure-Go (no CGO) zombiezen.com/go/sqlite connection pool and keeps
// all SQLite details — pragmas, driver, migrations — inside this package.
//
// Path handling is uniform: Create and Open both take a DIRECTORY and always
// append the fixed filename ecv3.db (Filename). Neither ever creates the
// directory; it must already exist.
//
//   - Create(dir) is the explicit "make a new database" path: the directory
//     must exist, dir/ecv3.db must NOT already exist, and migrations are applied
//     before returning.
//   - Open(dir) only opens an existing dir/ecv3.db, applying any pending
//     migrations (idempotent); it never creates the database.
//
// As a special case for tests, both Create and Open accept the path
// ":memory:" (MemoryPath): each call builds a fresh, migrated, in-memory
// database with no files touched.
package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Filename is the single, fixed database filename. Create and Open append it to
// the directory they are given, so they can never disagree about where the
// database lives.
const Filename = "ecv3.db"

// MemoryPath is the special directory value that selects a private in-memory
// database instead of a file. It is intended for tests; pass it to Create or
// Open to get a fresh, migrated database that lives only in RAM.
const MemoryPath = ":memory:"

// Store is the typed handle to the database. It owns a connection pool; callers
// must Close it when done.
type Store struct {
	pool *sqlitex.Pool
}

// Create makes a new database under dir and applies all migrations.
//
// dir must be an existing directory (Create never creates directories) and must
// not already contain ecv3.db (Create never clobbers). Passing MemoryPath
// creates a fresh in-memory database instead.
func Create(dir string) (*Store, error) {
	if dir == MemoryPath {
		return openPool(MemoryPath, true)
	}

	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("data directory does not exist: %s", dir)
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("data path is not a directory: %s", dir)
	}

	dbPath := filepath.Join(dir, Filename)
	switch _, err := os.Stat(dbPath); {
	case err == nil:
		return nil, fmt.Errorf("database already exists: %s", dbPath)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}

	return openPool(dbPath, true)
}

// Open opens the existing database under dir and applies any pending migrations.
//
// dir/ecv3.db must already exist; if it does not, Open fails fast with a message
// telling the operator to run `ec database create <dir>` (it never creates the
// database). Passing MemoryPath opens a fresh in-memory database instead.
func Open(dir string) (*Store, error) {
	if dir == MemoryPath {
		return openPool(MemoryPath, true)
	}

	dbPath := filepath.Join(dir, Filename)
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("no database at %s; run `ec database create %s`", dbPath, dir)
		}
		return nil, err
	}

	return openPool(dbPath, false)
}

// memCounter names each in-memory database uniquely so that separate Stores
// (e.g. parallel tests) never share one. A monotonic counter keeps this
// deterministic — no clock or randomness involved.
var memCounter atomic.Uint64

// openPool opens a pool at uri, applies migrations, and returns a ready Store.
// create reports whether the database may be created if missing; Open passes
// false so a vanished file surfaces as an error rather than a blank new DB.
//
// A bare ":memory:" is private per connection (and the driver rejects it for a
// pool), so MemoryPath is rewritten to a uniquely-named shared-cache in-memory
// URI and given a single-connection pool. The shared cache keeps the database
// alive for the life of the pool and isolates it from other in-memory Stores.
func openPool(uri string, create bool) (*Store, error) {
	flags := sqlite.OpenReadWrite | sqlite.OpenWAL | sqlite.OpenURI
	if create {
		flags |= sqlite.OpenCreate
	}

	opts := sqlitex.PoolOptions{
		Flags:       flags,
		PrepareConn: prepareConn,
	}
	if uri == MemoryPath {
		n := memCounter.Add(1)
		uri = fmt.Sprintf("file:ecv3mem%d?mode=memory&cache=shared", n)
		opts.PoolSize = 1
	}

	pool, err := sqlitex.NewPool(uri, opts)
	if err != nil {
		return nil, err
	}

	if err := migrate(pool); err != nil {
		_ = pool.Close()
		return nil, err
	}

	return &Store{pool: pool}, nil
}

// migrate brings the database up to the current schema. It is shared by Create
// and Open and is safe to run on an already-current database (a no-op).
func migrate(pool *sqlitex.Pool) error {
	ctx := context.Background()
	conn, err := pool.Take(ctx)
	if err != nil {
		return err
	}
	defer pool.Put(conn)

	if err := sqlitemigration.Migrate(ctx, conn, schema); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return nil
}

// prepareConn sets the per-connection pragmas every ecv3 connection needs.
// journal_mode = WAL is set per the OpenWAL flag too, but is repeated here so
// the intent is explicit and lives alongside the other pragmas.
func prepareConn(conn *sqlite.Conn) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
	}
	for _, p := range pragmas {
		// journal_mode cannot run inside a transaction, so each pragma is run
		// transiently and individually rather than via a wrapped script.
		if err := sqlitex.ExecuteTransient(conn, p, nil); err != nil {
			return fmt.Errorf("setting pragma %q: %w", p, err)
		}
	}
	return nil
}

// Ping verifies a live database connection. It is used by the /api/healthz
// endpoint to confirm the data layer is reachable.
func (s *Store) Ping(ctx context.Context) error {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return err
	}
	defer s.pool.Put(conn)
	return sqlitex.ExecuteTransient(conn, "SELECT 1;", nil)
}

// Conn obtains a connection from the pool. The caller must return it with Put.
// It exists so other server packages can run queries without importing the
// SQLite driver's pool type directly.
func (s *Store) Conn(ctx context.Context) (*sqlite.Conn, error) {
	return s.pool.Take(ctx)
}

// Put returns a connection obtained from Conn back to the pool.
func (s *Store) Put(conn *sqlite.Conn) {
	s.pool.Put(conn)
}

// Close releases the connection pool. It is safe to call once.
func (s *Store) Close() error {
	return s.pool.Close()
}
