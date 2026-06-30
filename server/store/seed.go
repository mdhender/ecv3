// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package store

import (
	"context"
	"fmt"
	"strings"

	"zombiezen.com/go/sqlite/sqlitex"
)

// Seed is a declarative description of accounts, games, and memberships to load
// into a database. It is the on-disk shape of an `ec database seed` file (see
// testdata/dev-seed.json) and is applied with (*Store).ApplySeed.
//
// Memberships reference accounts and games by their human-friendly keys (email
// and game code) rather than database ids, so a hand-written seed file never has
// to know about generated ids.
type Seed struct {
	Accounts []SeedAccount `json:"accounts"`
	Games    []SeedGame    `json:"games"`
	Members  []SeedMember  `json:"game_accounts"`
}

// SeedAccount is one account to create. Password is plaintext in the seed file
// (dev/test only) and is bcrypt-hashed by CreateAccount when applied.
type SeedAccount struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

// SeedGame is one game to create, identified by its unique code.
type SeedGame struct {
	Code string `json:"code"`
}

// SeedMember adds an account (by email) to a game (by code), optionally with a
// fixed handle and the GM role.
type SeedMember struct {
	Game   string `json:"game"`
	Email  string `json:"email"`
	Handle string `json:"handle"`
	IsGM   bool   `json:"is_gm"`
}

// ApplySeed creates the accounts, games, and memberships described by seed,
// reusing the same account/game/membership primitives the rest of the app uses.
// The whole seed is applied in one transaction: any error rolls the batch back,
// so a bad seed file (e.g. an unknown game reference) leaves the database
// untouched and can simply be fixed and re-run.
func (s *Store) ApplySeed(ctx context.Context, seed Seed) (err error) {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return err
	}
	defer s.pool.Put(conn)

	defer sqlitex.Transaction(conn)(&err)

	emailToID := make(map[string]int64, len(seed.Accounts))
	for _, a := range seed.Accounts {
		id, err := createAccount(conn, a.Email, a.Password, a.IsAdmin)
		if err != nil {
			return fmt.Errorf("seed account %q: %w", a.Email, err)
		}
		emailToID[strings.ToLower(strings.TrimSpace(a.Email))] = id
	}

	codeToID := make(map[string]int64, len(seed.Games))
	for _, g := range seed.Games {
		id, err := createGame(conn, g.Code)
		if err != nil {
			return fmt.Errorf("seed game %q: %w", g.Code, err)
		}
		codeToID[strings.TrimSpace(g.Code)] = id
	}

	for _, m := range seed.Members {
		gameID, ok := codeToID[strings.TrimSpace(m.Game)]
		if !ok {
			return fmt.Errorf("seed membership: unknown game %q", m.Game)
		}
		accountID, ok := emailToID[strings.ToLower(strings.TrimSpace(m.Email))]
		if !ok {
			return fmt.Errorf("seed membership: unknown account %q", m.Email)
		}
		if err := addGameAccount(conn, gameID, accountID, m.Handle, m.IsGM); err != nil {
			return fmt.Errorf("seed membership %q in %q: %w", m.Email, m.Game, err)
		}
	}
	return nil
}
