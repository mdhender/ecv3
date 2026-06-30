// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// metaValue reads a value from schema_meta, proving migrations ran and the
// database is readable.
func metaValue(t *testing.T, s *Store, key string) (string, bool) {
	t.Helper()
	conn, err := s.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer s.Put(conn)

	var (
		value string
		found bool
	)
	err = sqlitex.Execute(conn, "SELECT value FROM schema_meta WHERE key = ?;", &sqlitex.ExecOptions{
		Args: []any{key},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			value = stmt.ColumnText(0)
			found = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("select schema_meta: %v", err)
	}
	return value, found
}

func TestCreateThenOpenRoundTrip(t *testing.T) {
	dir := t.TempDir()

	s, err := Create(dir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if v, ok := metaValue(t, s, "app"); !ok || v != "ecv3" {
		t.Fatalf("after Create: schema_meta[app] = %q, %v; want \"ecv3\", true", v, ok)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The file must actually exist where we said it would.
	if _, err := os.Stat(filepath.Join(dir, Filename)); err != nil {
		t.Fatalf("expected %s to exist: %v", Filename, err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s2.Close()
	if v, ok := metaValue(t, s2, "app"); !ok || v != "ecv3" {
		t.Fatalf("after Open: schema_meta[app] = %q, %v; want \"ecv3\", true", v, ok)
	}
}

func TestOpenMissingDatabaseErrors(t *testing.T) {
	dir := t.TempDir() // exists, but has no ecv3.db

	if _, err := Open(dir); err == nil {
		t.Fatal("Open of a dir with no ecv3.db: want error, got nil")
	}
}

func TestCreateOverExistingDatabaseErrors(t *testing.T) {
	dir := t.TempDir()

	s, err := Create(dir)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	s.Close()

	if _, err := Create(dir); err == nil {
		t.Fatal("Create over an existing ecv3.db: want error, got nil")
	}
}

func TestCreateMissingDirectoryErrorsAndCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")

	if _, err := Create(dir); err == nil {
		t.Fatal("Create against a non-existent dir: want error, got nil")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("Create must not create the directory; stat err = %v", err)
	}
}

func TestMigrationsIdempotentAcrossOpens(t *testing.T) {
	dir := t.TempDir()

	s, err := Create(dir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	s.Close()

	// Re-opening a current database must be a no-op that keeps working.
	for i := range 3 {
		s, err := Open(dir)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		if v, ok := metaValue(t, s, "app"); !ok || v != "ecv3" {
			t.Fatalf("Open #%d: schema_meta[app] = %q, %v; want \"ecv3\", true", i, v, ok)
		}
		s.Close()
	}
}

func TestBasicWriteRead(t *testing.T) {
	s, err := Create(t.TempDir())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer s.Close()

	conn, err := s.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer s.Put(conn)

	if err := sqlitex.Execute(conn, "INSERT INTO schema_meta (key, value) VALUES (?, ?);", &sqlitex.ExecOptions{
		Args: []any{"greeting", "hello"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got string
	err = sqlitex.Execute(conn, "SELECT value FROM schema_meta WHERE key = ?;", &sqlitex.ExecOptions{
		Args: []any{"greeting"},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			got = stmt.ColumnText(0)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != "hello" {
		t.Fatalf("read back %q; want \"hello\"", got)
	}
}

func TestMemoryPath(t *testing.T) {
	for _, name := range []string{"Create", "Open"} {
		t.Run(name, func(t *testing.T) {
			open := Create
			if name == "Open" {
				open = Open
			}
			s, err := open(MemoryPath)
			if err != nil {
				t.Fatalf("%s(:memory:): %v", name, err)
			}
			defer s.Close()

			// Migrated and usable, with no files created anywhere.
			if v, ok := metaValue(t, s, "app"); !ok || v != "ecv3" {
				t.Fatalf("schema_meta[app] = %q, %v; want \"ecv3\", true", v, ok)
			}
			if err := s.Ping(context.Background()); err != nil {
				t.Fatalf("Ping: %v", err)
			}
		})
	}
}
