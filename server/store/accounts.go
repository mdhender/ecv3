// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// ErrEmailExists is returned by CreateAccount when an account with the same
// (lower-cased) email already exists. Callers can match it with errors.Is to
// give the operator a clear "that email is taken" message.
var ErrEmailExists = errors.New("account email already exists")

// ErrGameExists is returned by CreateGame when a game with the same code
// already exists.
var ErrGameExists = errors.New("game code already exists")

// CreateAccount inserts a new account and returns its id. The email is
// lower-cased (the schema also CHECKs this) and the password is bcrypt-hashed
// in Go before it is stored; the plaintext never reaches SQLite. A duplicate
// email surfaces as ErrEmailExists.
func (s *Store) CreateAccount(ctx context.Context, email, password string, isAdmin bool) (int64, error) {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return 0, err
	}
	defer s.pool.Put(conn)
	return createAccount(conn, email, password, isAdmin)
}

// CreateGame inserts a new game with the given code and returns its id. A
// duplicate code surfaces as ErrGameExists.
func (s *Store) CreateGame(ctx context.Context, code string) (int64, error) {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return 0, err
	}
	defer s.pool.Put(conn)
	return createGame(conn, code)
}

// AddGameAccount adds accountID to gameID. An empty handle is stored as NULL so
// the schema's default-handle trigger assigns "player_N"; a non-empty handle is
// used verbatim (and validated by the schema's handle CHECK). The schema's
// no-admin-member trigger rejects adding an admin account.
func (s *Store) AddGameAccount(ctx context.Context, gameID, accountID int64, handle string, isGM bool) error {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return err
	}
	defer s.pool.Put(conn)
	return addGameAccount(conn, gameID, accountID, handle, isGM)
}

// createAccount is the connection-level account insert shared by the pooled
// CreateAccount method and the in-package test fixtures. It hashes the password
// and normalizes the email exactly once, here, so both paths behave identically.
func createAccount(conn *sqlite.Conn, email, password string, isAdmin bool) (int64, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return 0, errors.New("email is required")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		if errors.Is(err, bcrypt.ErrPasswordTooLong) {
			return 0, fmt.Errorf("password is too long (bcrypt accepts at most 72 bytes)")
		}
		return 0, fmt.Errorf("hashing password: %w", err)
	}

	err = sqlitex.Execute(conn,
		"INSERT INTO accounts (email, hashed_secret, is_admin) VALUES (?, ?, ?);",
		&sqlitex.ExecOptions{Args: []any{email, string(hash), boolToInt(isAdmin)}})
	if err != nil {
		if sqlite.ErrCode(err) == sqlite.ResultConstraintUnique {
			return 0, fmt.Errorf("%q: %w", email, ErrEmailExists)
		}
		return 0, fmt.Errorf("creating account %q: %w", email, err)
	}
	return conn.LastInsertRowID(), nil
}

// createGame is the connection-level game insert shared by CreateGame and the
// test fixtures.
func createGame(conn *sqlite.Conn, code string) (int64, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return 0, errors.New("game code is required")
	}

	err := sqlitex.Execute(conn,
		"INSERT INTO games (code) VALUES (?);",
		&sqlitex.ExecOptions{Args: []any{code}})
	if err != nil {
		if sqlite.ErrCode(err) == sqlite.ResultConstraintUnique {
			return 0, fmt.Errorf("%q: %w", code, ErrGameExists)
		}
		return 0, fmt.Errorf("creating game %q: %w", code, err)
	}
	return conn.LastInsertRowID(), nil
}

// addGameAccount is the connection-level membership insert shared by
// AddGameAccount and the test fixtures.
func addGameAccount(conn *sqlite.Conn, gameID, accountID int64, handle string, isGM bool) error {
	// nil => SQL NULL, which fires the default-handle trigger.
	var handleArg any
	if handle != "" {
		handleArg = handle
	}

	err := sqlitex.Execute(conn,
		"INSERT INTO game_accounts (game_id, account_id, handle, is_gm) VALUES (?, ?, ?, ?);",
		&sqlitex.ExecOptions{Args: []any{gameID, accountID, handleArg, boolToInt(isGM)}})
	if err != nil {
		return fmt.Errorf("adding account %d to game %d: %w", accountID, gameID, err)
	}
	return nil
}

// boolToInt maps a Go bool to the 0/1 integers SQLite stores for the schema's
// boolean columns.
func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
