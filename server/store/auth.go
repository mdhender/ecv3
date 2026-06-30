// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package store

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// ErrInvalidCredentials is returned by AuthenticateByPassword for any login
// failure — unknown email, wrong password, or an inactive account. It is
// deliberately a single error so callers cannot leak which accounts exist.
var ErrInvalidCredentials = errors.New("invalid credentials")

// Account is the non-secret view of an account row. It never carries the
// password hash.
type Account struct {
	ID       int64
	Email    string
	IsAdmin  bool
	IsActive bool
}

// dummyHash is a valid bcrypt hash compared against when the email is unknown,
// so a login for a non-existent account does the same bcrypt work as a real one
// and the response timing does not reveal which emails exist. It is computed
// once at startup over a throwaway value.
var dummyHash = mustDummyHash()

func mustDummyHash() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcrypt.DefaultCost)
	if err != nil {
		panic("store: generating dummy bcrypt hash: " + err.Error())
	}
	return h
}

// AuthenticateByPassword verifies an email/password pair. On success it returns
// the account; on any failure it returns ErrInvalidCredentials. The email is
// lower-cased to match storage. bcrypt comparison runs even when the email is
// unknown (against dummyHash) to keep timing uniform, and the active check comes
// after the comparison for the same reason.
func (s *Store) AuthenticateByPassword(ctx context.Context, email, password string) (Account, error) {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return Account{}, err
	}
	defer s.pool.Put(conn)

	email = strings.ToLower(strings.TrimSpace(email))

	var (
		acct  Account
		hash  string
		found bool
	)
	err = sqlitex.Execute(conn,
		"SELECT id, email, hashed_secret, is_admin, is_active FROM accounts WHERE email=?;",
		&sqlitex.ExecOptions{
			Args: []any{email},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				found = true
				acct.ID = stmt.ColumnInt64(0)
				acct.Email = stmt.ColumnText(1)
				hash = stmt.ColumnText(2)
				acct.IsAdmin = stmt.ColumnInt64(3) == 1
				acct.IsActive = stmt.ColumnInt64(4) == 1
				return nil
			},
		})
	if err != nil {
		return Account{}, err
	}

	if !found {
		// Equalize timing with the found path; result is ignored.
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return Account{}, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return Account{}, ErrInvalidCredentials
	}
	if !acct.IsActive {
		return Account{}, ErrInvalidCredentials
	}
	return acct, nil
}

// GetAccount loads an account by id. The bool reports whether it was found; a
// missing account is not an error. Used to resolve a session's account into the
// non-secret fields the API returns.
func (s *Store) GetAccount(ctx context.Context, id int64) (Account, bool, error) {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return Account{}, false, err
	}
	defer s.pool.Put(conn)

	var (
		acct  Account
		found bool
	)
	err = sqlitex.Execute(conn,
		"SELECT id, email, is_admin, is_active FROM accounts WHERE id=?;",
		&sqlitex.ExecOptions{
			Args: []any{id},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				found = true
				acct.ID = stmt.ColumnInt64(0)
				acct.Email = stmt.ColumnText(1)
				acct.IsAdmin = stmt.ColumnInt64(2) == 1
				acct.IsActive = stmt.ColumnInt64(3) == 1
				return nil
			},
		})
	if err != nil {
		return Account{}, false, err
	}
	return acct, found, nil
}
