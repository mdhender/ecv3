// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package store

import (
	"context"
	"strings"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// memStore returns a fresh migrated in-memory store, failing the test on error.
func memStore(t *testing.T) *Store {
	t.Helper()
	s, err := Create(MemoryPath)
	if err != nil {
		t.Fatalf("Create(:memory:): %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// conn takes a connection from the store and returns it on cleanup.
func conn(t *testing.T, s *Store) *sqlite.Conn {
	t.Helper()
	c, err := s.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	t.Cleanup(func() { s.Put(c) })
	return c
}

// exec runs a statement with positional args and returns any error.
func exec(c *sqlite.Conn, query string, args ...any) error {
	return sqlitex.Execute(c, query, &sqlitex.ExecOptions{Args: args})
}

// mustExec runs a statement and fails the test if it errors.
func mustExec(t *testing.T, c *sqlite.Conn, query string, args ...any) {
	t.Helper()
	if err := exec(c, query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// queryString runs a single-row, single-column text query.
func queryString(t *testing.T, c *sqlite.Conn, query string, args ...any) string {
	t.Helper()
	var got string
	err := sqlitex.Execute(c, query, &sqlitex.ExecOptions{
		Args: args,
		ResultFunc: func(stmt *sqlite.Stmt) error {
			got = stmt.ColumnText(0)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return got
}

// queryInt runs a single-row, single-column integer query.
func queryInt(t *testing.T, c *sqlite.Conn, query string, args ...any) int64 {
	t.Helper()
	var got int64
	err := sqlitex.Execute(c, query, &sqlitex.ExecOptions{
		Args: args,
		ResultFunc: func(stmt *sqlite.Stmt) error {
			got = stmt.ColumnInt64(0)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return got
}

// newAccount inserts an account and returns its id.
func newAccount(t *testing.T, c *sqlite.Conn, email string, admin int) int64 {
	t.Helper()
	mustExec(t, c, "INSERT INTO accounts (email, hashed_secret, is_admin) VALUES (?, ?, ?);", email, "hash", admin)
	return c.LastInsertRowID()
}

// newGame inserts a game and returns its id.
func newGame(t *testing.T, c *sqlite.Conn, code string) int64 {
	t.Helper()
	mustExec(t, c, "INSERT INTO games (code) VALUES (?);", code)
	return c.LastInsertRowID()
}

// TestMigration0002AppliesAndIsIdempotent confirms 0002 lands on both Create
// and Open and that re-opening a current DB stays a no-op with the new objects.
func TestMigration0002AppliesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	// withConn runs fn with a borrowed connection and always returns it before
	// returning, so a later s.Close() never blocks waiting on the pool.
	withConn := func(s *Store, fn func(c *sqlite.Conn)) {
		c, err := s.Conn(context.Background())
		if err != nil {
			t.Fatalf("Conn: %v", err)
		}
		defer s.Put(c)
		fn(c)
	}

	s, err := Create(dir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	withConn(s, func(c *sqlite.Conn) {
		for _, tbl := range []string{"accounts", "games", "game_accounts"} {
			n := queryInt(t, c, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?;", tbl)
			if n != 1 {
				t.Fatalf("after Create: table %q present=%d; want 1", tbl, n)
			}
		}
	})
	s.Close()

	// Re-open several times; the new tables + triggers must not break idempotency.
	for i := range 3 {
		s, err := Open(dir)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		withConn(s, func(c *sqlite.Conn) {
			n := queryInt(t, c, "SELECT count(*) FROM sqlite_master WHERE type='trigger';")
			if n != 4 {
				t.Fatalf("Open #%d: trigger count=%d; want 4", i, n)
			}
		})
		s.Close()
	}
}

// TestAccountRoundTrip checks insert/read and that hashed_secret is verbatim.
func TestAccountRoundTrip(t *testing.T) {
	s := memStore(t)
	c := conn(t, s)

	const hash = "$2a$10$abcdefghijklmnopqrstuv"
	mustExec(t, c, "INSERT INTO accounts (email, hashed_secret) VALUES (?, ?);", "alice@example.com", hash)

	got := queryString(t, c, "SELECT hashed_secret FROM accounts WHERE email=?;", "alice@example.com")
	if got != hash {
		t.Fatalf("hashed_secret = %q; want %q", got, hash)
	}
	if active := queryInt(t, c, "SELECT is_active FROM accounts WHERE email=?;", "alice@example.com"); active != 1 {
		t.Fatalf("is_active default = %d; want 1", active)
	}
	if admin := queryInt(t, c, "SELECT is_admin FROM accounts WHERE email=?;", "alice@example.com"); admin != 0 {
		t.Fatalf("is_admin default = %d; want 0", admin)
	}
}

// TestAccountEmailConstraints covers UNIQUE(email) and the lower-case CHECK.
func TestAccountEmailConstraints(t *testing.T) {
	s := memStore(t)
	c := conn(t, s)

	mustExec(t, c, "INSERT INTO accounts (email, hashed_secret) VALUES (?, ?);", "bob@example.com", "h")

	if err := exec(c, "INSERT INTO accounts (email, hashed_secret) VALUES (?, ?);", "bob@example.com", "h"); err == nil {
		t.Fatal("duplicate email: want UNIQUE violation, got nil")
	}
	if err := exec(c, "INSERT INTO accounts (email, hashed_secret) VALUES (?, ?);", "Mixed@Example.com", "h"); err == nil {
		t.Fatal("mixed-case email: want CHECK violation, got nil")
	}
	if err := exec(c, "INSERT INTO accounts (email, hashed_secret) VALUES (?, ?);", "", "h"); err == nil {
		t.Fatal("empty email: want CHECK violation, got nil")
	}
}

// TestHandleDefault covers the player_N defaulting trigger.
func TestHandleDefault(t *testing.T) {
	s := memStore(t)
	c := conn(t, s)

	g := newGame(t, c, "game-default")

	// Empty game: first NULL-handle insert => player_1.
	a1 := newAccount(t, c, "p1@example.com", 0)
	mustExec(t, c, "INSERT INTO game_accounts (game_id, account_id) VALUES (?, ?);", g, a1)
	if h := queryString(t, c, "SELECT handle FROM game_accounts WHERE game_id=? AND account_id=?;", g, a1); h != "player_1" {
		t.Fatalf("first handle = %q; want player_1", h)
	}

	// Successive NULL-handle inserts increment.
	a2 := newAccount(t, c, "p2@example.com", 0)
	mustExec(t, c, "INSERT INTO game_accounts (game_id, account_id) VALUES (?, ?);", g, a2)
	if h := queryString(t, c, "SELECT handle FROM game_accounts WHERE game_id=? AND account_id=?;", g, a2); h != "player_2" {
		t.Fatalf("second handle = %q; want player_2", h)
	}

	// A game with 5 existing members: next NULL insert => player_6.
	g2 := newGame(t, c, "game-five")
	for i := range 5 {
		a := newAccount(t, c, strings.Repeat("x", i+1)+"@example.com", 0)
		mustExec(t, c, "INSERT INTO game_accounts (game_id, account_id, handle) VALUES (?, ?, ?);", g2, a, "seed"+strings.Repeat("z", i+1))
	}
	a6 := newAccount(t, c, "sixth@example.com", 0)
	mustExec(t, c, "INSERT INTO game_accounts (game_id, account_id) VALUES (?, ?);", g2, a6)
	if h := queryString(t, c, "SELECT handle FROM game_accounts WHERE game_id=? AND account_id=?;", g2, a6); h != "player_6" {
		t.Fatalf("sixth handle = %q; want player_6", h)
	}
}

// TestHandleOverride confirms a supplied valid handle is not overwritten.
func TestHandleOverride(t *testing.T) {
	s := memStore(t)
	c := conn(t, s)

	g := newGame(t, c, "game-override")
	a := newAccount(t, c, "named@example.com", 0)
	mustExec(t, c, "INSERT INTO game_accounts (game_id, account_id, handle) VALUES (?, ?, ?);", g, a, "zaphod")
	if h := queryString(t, c, "SELECT handle FROM game_accounts WHERE game_id=? AND account_id=?;", g, a); h != "zaphod" {
		t.Fatalf("handle = %q; want zaphod (override kept)", h)
	}
}

// TestHandleCheck covers the handle pattern CHECK constraint.
func TestHandleCheck(t *testing.T) {
	s := memStore(t)
	c := conn(t, s)
	g := newGame(t, c, "game-check")

	cases := []struct {
		handle string
		ok     bool
	}{
		{"Abc", false},  // leading uppercase
		{"1abc", false}, // leading digit
		{"a", false},    // too short
		{"ab*c", false}, // illegal char
		{"", false},     // empty
		{"a1", true},
		{"a.b_c-d", true},
		{"player_1", true},
	}
	for i, tc := range cases {
		a := newAccount(t, c, "h"+strings.Repeat("y", i+1)+"@example.com", 0)
		err := exec(c, "INSERT INTO game_accounts (game_id, account_id, handle) VALUES (?, ?, ?);", g, a, tc.handle)
		if tc.ok && err != nil {
			t.Errorf("handle %q: want accepted, got error %v", tc.handle, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("handle %q: want CHECK violation, got nil", tc.handle)
		}
	}
}

// TestHandleUniquePerGame covers UNIQUE(game_id, handle).
func TestHandleUniquePerGame(t *testing.T) {
	s := memStore(t)
	c := conn(t, s)

	g1 := newGame(t, c, "g1")
	g2 := newGame(t, c, "g2")
	a1 := newAccount(t, c, "u1@example.com", 0)
	a2 := newAccount(t, c, "u2@example.com", 0)
	a3 := newAccount(t, c, "u3@example.com", 0)

	mustExec(t, c, "INSERT INTO game_accounts (game_id, account_id, handle) VALUES (?, ?, ?);", g1, a1, "hero")

	// Same handle, same game: rejected.
	if err := exec(c, "INSERT INTO game_accounts (game_id, account_id, handle) VALUES (?, ?, ?);", g1, a2, "hero"); err == nil {
		t.Fatal("duplicate handle in same game: want UNIQUE violation, got nil")
	}
	// Same handle text, different game: allowed.
	if err := exec(c, "INSERT INTO game_accounts (game_id, account_id, handle) VALUES (?, ?, ?);", g2, a3, "hero"); err != nil {
		t.Fatalf("same handle in different game: want allowed, got %v", err)
	}
}

// TestNoAdminMember covers both directions of the admin/membership exclusion.
func TestNoAdminMember(t *testing.T) {
	s := memStore(t)
	c := conn(t, s)

	g := newGame(t, c, "admin-game")

	// admin insert into game_accounts is rejected.
	admin := newAccount(t, c, "admin@example.com", 1)
	if err := exec(c, "INSERT INTO game_accounts (game_id, account_id) VALUES (?, ?);", g, admin); err == nil {
		t.Fatal("admin as game member: want trigger ABORT, got nil")
	}

	// promoting an existing member to admin is rejected.
	member := newAccount(t, c, "member@example.com", 0)
	mustExec(t, c, "INSERT INTO game_accounts (game_id, account_id) VALUES (?, ?);", g, member)
	if err := exec(c, "UPDATE accounts SET is_admin = 1 WHERE id = ?;", member); err == nil {
		t.Fatal("promote member to admin: want trigger ABORT, got nil")
	}

	// promoting a non-member to admin is fine.
	loner := newAccount(t, c, "loner@example.com", 0)
	if err := exec(c, "UPDATE accounts SET is_admin = 1 WHERE id = ?;", loner); err != nil {
		t.Fatalf("promote non-member to admin: want allowed, got %v", err)
	}
}

// TestCompositePK rejects a duplicate (game_id, account_id) row.
func TestCompositePK(t *testing.T) {
	s := memStore(t)
	c := conn(t, s)

	g := newGame(t, c, "pk-game")
	a := newAccount(t, c, "dup@example.com", 0)
	mustExec(t, c, "INSERT INTO game_accounts (game_id, account_id, handle) VALUES (?, ?, ?);", g, a, "first")
	if err := exec(c, "INSERT INTO game_accounts (game_id, account_id, handle) VALUES (?, ?, ?);", g, a, "second"); err == nil {
		t.Fatal("duplicate (game_id, account_id): want PK violation, got nil")
	}
}

// TestActiveToggleKeepsHandle drops and re-activates a member without minting a
// new handle.
func TestActiveToggleKeepsHandle(t *testing.T) {
	s := memStore(t)
	c := conn(t, s)

	g := newGame(t, c, "toggle-game")
	a := newAccount(t, c, "toggle@example.com", 0)
	mustExec(t, c, "INSERT INTO game_accounts (game_id, account_id) VALUES (?, ?);", g, a)
	orig := queryString(t, c, "SELECT handle FROM game_accounts WHERE game_id=? AND account_id=?;", g, a)
	if orig != "player_1" {
		t.Fatalf("initial handle = %q; want player_1", orig)
	}

	mustExec(t, c, "UPDATE game_accounts SET is_active = 0 WHERE game_id=? AND account_id=?;", g, a)
	mustExec(t, c, "UPDATE game_accounts SET is_active = 1 WHERE game_id=? AND account_id=?;", g, a)

	if h := queryString(t, c, "SELECT handle FROM game_accounts WHERE game_id=? AND account_id=?;", g, a); h != orig {
		t.Fatalf("handle after toggle = %q; want %q (unchanged)", h, orig)
	}
}

// TestNoGMDowngrade rejects is_gm 1->0 but allows 0->1.
func TestNoGMDowngrade(t *testing.T) {
	s := memStore(t)
	c := conn(t, s)

	g := newGame(t, c, "gm-game")
	a := newAccount(t, c, "gm@example.com", 0)
	mustExec(t, c, "INSERT INTO game_accounts (game_id, account_id) VALUES (?, ?);", g, a)

	// Promote player -> GM: allowed.
	if err := exec(c, "UPDATE game_accounts SET is_gm = 1 WHERE game_id=? AND account_id=?;", g, a); err != nil {
		t.Fatalf("promote to GM: want allowed, got %v", err)
	}
	// Downgrade GM -> player: rejected.
	if err := exec(c, "UPDATE game_accounts SET is_gm = 0 WHERE game_id=? AND account_id=?;", g, a); err == nil {
		t.Fatal("GM downgrade: want trigger ABORT, got nil")
	}
}

// TestCascadeDelete confirms deleting a game or account clears the bridge rows.
func TestCascadeDelete(t *testing.T) {
	s := memStore(t)
	c := conn(t, s)

	// Deleting a game removes its game_accounts.
	g := newGame(t, c, "cascade-game")
	a := newAccount(t, c, "cg@example.com", 0)
	mustExec(t, c, "INSERT INTO game_accounts (game_id, account_id) VALUES (?, ?);", g, a)
	mustExec(t, c, "DELETE FROM games WHERE id = ?;", g)
	if n := queryInt(t, c, "SELECT count(*) FROM game_accounts WHERE game_id = ?;", g); n != 0 {
		t.Fatalf("after game delete: %d bridge rows remain; want 0", n)
	}

	// Deleting an account removes its game_accounts.
	g2 := newGame(t, c, "cascade-game-2")
	a2 := newAccount(t, c, "ca@example.com", 0)
	mustExec(t, c, "INSERT INTO game_accounts (game_id, account_id) VALUES (?, ?);", g2, a2)
	mustExec(t, c, "DELETE FROM accounts WHERE id = ?;", a2)
	if n := queryInt(t, c, "SELECT count(*) FROM game_accounts WHERE account_id = ?;", a2); n != 0 {
		t.Fatalf("after account delete: %d bridge rows remain; want 0", n)
	}
}
