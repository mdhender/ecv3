// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package store

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestCreateAccount covers the Go account-creation primitive: lower-casing,
// the admin flag, duplicate-email detection, and a bcrypt verify round-trip.
func TestCreateAccount(t *testing.T) {
	cases := []struct {
		name      string
		email     string
		password  string
		isAdmin   bool
		wantEmail string // email as it should be stored (lower-cased)
	}{
		{name: "plain", email: "alice@example.com", password: "s3cr3t", wantEmail: "alice@example.com"},
		{name: "lowercased", email: "Mixed@Example.COM", password: "hunter2", wantEmail: "mixed@example.com"},
		{name: "admin", email: "root@example.com", password: "p4ss", isAdmin: true, wantEmail: "root@example.com"},
		{name: "trimmed", email: "  bob@example.com  ", password: "pw", wantEmail: "bob@example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := memStore(t)
			ctx := context.Background()

			id, err := s.CreateAccount(ctx, tc.email, tc.password, tc.isAdmin)
			if err != nil {
				t.Fatalf("CreateAccount: %v", err)
			}
			if id <= 0 {
				t.Fatalf("CreateAccount returned id %d; want > 0", id)
			}

			// Read back through a borrowed connection (taken AFTER CreateAccount
			// returned, so the single-conn pool is free).
			c := conn(t, s)
			gotEmail := queryString(t, c, "SELECT email FROM accounts WHERE id=?;", id)
			if gotEmail != tc.wantEmail {
				t.Errorf("stored email = %q; want %q", gotEmail, tc.wantEmail)
			}
			wantAdmin := int64(0)
			if tc.isAdmin {
				wantAdmin = 1
			}
			if got := queryInt(t, c, "SELECT is_admin FROM accounts WHERE id=?;", id); got != wantAdmin {
				t.Errorf("is_admin = %d; want %d", got, wantAdmin)
			}

			// bcrypt round-trip: the stored hash must verify the password and
			// reject a wrong one, and must not be the plaintext.
			hash := queryString(t, c, "SELECT hashed_secret FROM accounts WHERE id=?;", id)
			if hash == tc.password {
				t.Fatal("hashed_secret stored as plaintext")
			}
			if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(tc.password)); err != nil {
				t.Errorf("bcrypt verify of correct password failed: %v", err)
			}
			if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(tc.password+"x")); err == nil {
				t.Error("bcrypt verify of wrong password succeeded; want failure")
			}
		})
	}
}

// TestCreateAccountDuplicate confirms a duplicate email (including a differing
// case that normalizes to the same address) surfaces as ErrEmailExists.
func TestCreateAccountDuplicate(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()

	if _, err := s.CreateAccount(ctx, "dup@example.com", "pw", false); err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}

	if _, err := s.CreateAccount(ctx, "dup@example.com", "pw", false); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("duplicate email error = %v; want ErrEmailExists", err)
	}
	// Different case normalizes to the same email -> still a duplicate.
	if _, err := s.CreateAccount(ctx, "DUP@example.com", "pw", false); !errors.Is(err, ErrEmailExists) {
		t.Fatalf("case-variant duplicate error = %v; want ErrEmailExists", err)
	}
}

// TestCreateAccountEmptyEmail rejects an empty (or whitespace-only) email
// before touching the database.
func TestCreateAccountEmptyEmail(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	if _, err := s.CreateAccount(ctx, "   ", "pw", false); err == nil {
		t.Fatal("empty email: want error, got nil")
	}
}

// TestCreateGameDuplicate confirms a duplicate game code surfaces as
// ErrGameExists.
func TestCreateGameDuplicate(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	if _, err := s.CreateGame(ctx, "alpha"); err != nil {
		t.Fatalf("first CreateGame: %v", err)
	}
	if _, err := s.CreateGame(ctx, "alpha"); !errors.Is(err, ErrGameExists) {
		t.Fatalf("duplicate game error = %v; want ErrGameExists", err)
	}
}

// TestApplySeed loads a small in-memory seed and verifies the rows, including
// the default-handle trigger and the no-admin-member rule (an admin is never
// added as a game member).
func TestApplySeed(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()

	seed := Seed{
		Accounts: []SeedAccount{
			{Email: "admin@example.com", Password: "pw", IsAdmin: true},
			{Email: "gm@example.com", Password: "pw"},
			{Email: "player@example.com", Password: "pw"},
		},
		Games: []SeedGame{{Code: "demo"}},
		Members: []SeedMember{
			{Game: "demo", Email: "gm@example.com", Handle: "captain", IsGM: true},
			{Game: "demo", Email: "player@example.com"}, // NULL handle -> player_N
		},
	}
	if err := s.ApplySeed(ctx, seed); err != nil {
		t.Fatalf("ApplySeed: %v", err)
	}

	c := conn(t, s)
	if n := queryInt(t, c, "SELECT count(*) FROM accounts;"); n != 3 {
		t.Errorf("accounts = %d; want 3", n)
	}
	if n := queryInt(t, c, "SELECT count(*) FROM game_accounts;"); n != 2 {
		t.Errorf("game_accounts = %d; want 2", n)
	}
	if h := queryString(t, c, "SELECT handle FROM game_accounts ga JOIN accounts a ON a.id=ga.account_id WHERE a.email=?;", "gm@example.com"); h != "captain" {
		t.Errorf("gm handle = %q; want captain", h)
	}
	// The player with a NULL seed handle gets the default trigger's player_N.
	if h := queryString(t, c, "SELECT handle FROM game_accounts ga JOIN accounts a ON a.id=ga.account_id WHERE a.email=?;", "player@example.com"); h == "" {
		t.Error("player handle is empty; want a default player_N handle")
	}
}

// TestApplySeedAdminMemberRejected confirms the schema's no-admin-member trigger
// still fires through the seed path: seeding an admin as a member fails.
func TestApplySeedAdminMemberRejected(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()

	seed := Seed{
		Accounts: []SeedAccount{{Email: "admin@example.com", Password: "pw", IsAdmin: true}},
		Games:    []SeedGame{{Code: "demo"}},
		Members:  []SeedMember{{Game: "demo", Email: "admin@example.com"}},
	}
	if err := s.ApplySeed(ctx, seed); err == nil {
		t.Fatal("seeding an admin as a game member: want error, got nil")
	}

	// The whole seed runs in one transaction, so the failure must have rolled
	// the already-inserted account/game back: nothing should remain.
	c := conn(t, s)
	if n := queryInt(t, c, "SELECT count(*) FROM accounts;"); n != 0 {
		t.Errorf("after failed seed: accounts = %d; want 0 (rolled back)", n)
	}
	if n := queryInt(t, c, "SELECT count(*) FROM games;"); n != 0 {
		t.Errorf("after failed seed: games = %d; want 0 (rolled back)", n)
	}
}

// TestSeedAccountsFixture exercises the in-package fixture helper.
func TestSeedAccountsFixture(t *testing.T) {
	s := memStore(t)
	c := conn(t, s)

	ids := seedAccounts(t, c,
		seedAccount{email: "One@Example.com", password: "pw"},
		seedAccount{email: "two@example.com", password: "pw", admin: true},
	)
	if len(ids) != 2 {
		t.Fatalf("seedAccounts returned %d ids; want 2", len(ids))
	}
	if _, ok := ids["one@example.com"]; !ok {
		t.Error("fixture map missing normalized key one@example.com")
	}
	if got := queryInt(t, c, "SELECT is_admin FROM accounts WHERE id=?;", ids["two@example.com"]); got != 1 {
		t.Errorf("two@example.com is_admin = %d; want 1", got)
	}
}
