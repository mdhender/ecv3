// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package store

import (
	"context"
	"errors"
	"fmt"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// ErrSessionExists is returned by CreateSession if a row with the same id_hash
// already exists. With a 256-bit random token this is effectively impossible;
// it surfaces a generation bug rather than a real collision.
var ErrSessionExists = errors.New("session already exists")

// Session is a stored login session. Timestamps are unix epoch seconds. The
// store never reads the clock — the auth layer supplies CreatedAt/ExpiresAt/
// LastSeenAt — so behavior stays deterministic and testable.
//
// ImpersonatedAccountID and CurrentGameID are 0 when unset (SQL NULL): account
// and game ids are positive, so 0 unambiguously means "none".
type Session struct {
	IDHash                string
	AccountID             int64
	ImpersonatedAccountID int64 // 0 = not impersonating
	CurrentGameID         int64 // 0 = not in a game
	CreatedAt             int64
	ExpiresAt             int64
	LastSeenAt            int64
	IP                    string
	UserAgent             string
}

// CreateSession inserts a new session. idHash is the SHA-256 (hex) of the random
// cookie token; the raw token is never stored. created/expires/lastSeen are unix
// seconds supplied by the caller. A duplicate id_hash surfaces as
// ErrSessionExists.
func (s *Store) CreateSession(ctx context.Context, idHash string, accountID int64, ip, userAgent string, created, expires, lastSeen int64) error {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return err
	}
	defer s.pool.Put(conn)
	return createSession(conn, idHash, accountID, ip, userAgent, created, expires, lastSeen)
}

// GetSession looks up a session by id_hash. The bool reports whether a row was
// found; a missing session is not an error.
func (s *Store) GetSession(ctx context.Context, idHash string) (Session, bool, error) {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return Session{}, false, err
	}
	defer s.pool.Put(conn)
	return getSession(conn, idHash)
}

// TouchSession slides a session forward: it updates last_seen_at, expires_at,
// and the client fingerprint (ip/user_agent) recorded for the session. It is
// called on each authenticated request.
func (s *Store) TouchSession(ctx context.Context, idHash, ip, userAgent string, lastSeen, expires int64) error {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return err
	}
	defer s.pool.Put(conn)
	return execAffecting(conn,
		"UPDATE sessions SET last_seen_at=?, expires_at=?, ip=?, user_agent=? WHERE id_hash=?;",
		lastSeen, expires, ip, userAgent, idHash)
}

// SetCurrentGame points a session at a game for link resolution. A gameID of 0
// clears it (stores SQL NULL).
func (s *Store) SetCurrentGame(ctx context.Context, idHash string, gameID int64) error {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return err
	}
	defer s.pool.Put(conn)
	return execAffecting(conn,
		"UPDATE sessions SET current_game_id=? WHERE id_hash=?;",
		nullableID(gameID), idHash)
}

// SetImpersonation sets (or clears, with 0) the account a session is acting as.
// Authorization — that the session's real account is an admin and the target is
// not — is the caller's responsibility (Go service layer).
func (s *Store) SetImpersonation(ctx context.Context, idHash string, impersonatedAccountID int64) error {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return err
	}
	defer s.pool.Put(conn)
	return execAffecting(conn,
		"UPDATE sessions SET impersonated_account_id=? WHERE id_hash=?;",
		nullableID(impersonatedAccountID), idHash)
}

// DeleteSession removes a single session (logout). Deleting a session that does
// not exist is not an error.
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return err
	}
	defer s.pool.Put(conn)
	return execConn(conn, "DELETE FROM sessions WHERE id_hash=?;", idHash)
}

// DeleteAccountSessions removes every session for an account ("log out
// everywhere"; also used after a password change or a ban). It returns the
// number of sessions removed.
func (s *Store) DeleteAccountSessions(ctx context.Context, accountID int64) (int, error) {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return 0, err
	}
	defer s.pool.Put(conn)
	if err := execConn(conn, "DELETE FROM sessions WHERE account_id=?;", accountID); err != nil {
		return 0, err
	}
	return conn.Changes(), nil
}

// DeleteExpiredSessions removes sessions whose expires_at is strictly before
// now (unix seconds). It returns the number removed and is meant for a periodic
// or opportunistic cleanup sweep.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now int64) (int, error) {
	conn, err := s.pool.Take(ctx)
	if err != nil {
		return 0, err
	}
	defer s.pool.Put(conn)
	if err := execConn(conn, "DELETE FROM sessions WHERE expires_at < ?;", now); err != nil {
		return 0, err
	}
	return conn.Changes(), nil
}

// createSession is the connection-level insert shared by CreateSession and the
// in-package test fixtures.
func createSession(conn *sqlite.Conn, idHash string, accountID int64, ip, userAgent string, created, expires, lastSeen int64) error {
	err := sqlitex.Execute(conn,
		`INSERT INTO sessions
		   (id_hash, account_id, ip, user_agent, created_at, expires_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?);`,
		&sqlitex.ExecOptions{Args: []any{idHash, accountID, ip, userAgent, created, expires, lastSeen}})
	if err != nil {
		if sqlite.ErrCode(err) == sqlite.ResultConstraintPrimaryKey {
			return fmt.Errorf("session %q: %w", idHash, ErrSessionExists)
		}
		return fmt.Errorf("creating session: %w", err)
	}
	return nil
}

// getSession is the connection-level lookup shared by GetSession and tests.
func getSession(conn *sqlite.Conn, idHash string) (Session, bool, error) {
	var sess Session
	found := false
	err := sqlitex.Execute(conn,
		`SELECT id_hash, account_id, impersonated_account_id, current_game_id,
		        created_at, expires_at, last_seen_at, ip, user_agent
		   FROM sessions WHERE id_hash=?;`,
		&sqlitex.ExecOptions{
			Args: []any{idHash},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				found = true
				sess = Session{
					IDHash:                stmt.ColumnText(0),
					AccountID:             stmt.ColumnInt64(1),
					ImpersonatedAccountID: stmt.ColumnInt64(2), // NULL -> 0
					CurrentGameID:         stmt.ColumnInt64(3), // NULL -> 0
					CreatedAt:             stmt.ColumnInt64(4),
					ExpiresAt:             stmt.ColumnInt64(5),
					LastSeenAt:            stmt.ColumnInt64(6),
					IP:                    stmt.ColumnText(7),
					UserAgent:             stmt.ColumnText(8),
				}
				return nil
			},
		})
	if err != nil {
		return Session{}, false, fmt.Errorf("reading session: %w", err)
	}
	return sess, found, nil
}

// execAffecting runs an UPDATE and reports a missing target (0 rows changed) as
// an error, so callers updating a specific session learn it vanished.
func execAffecting(conn *sqlite.Conn, query string, args ...any) error {
	if err := execConn(conn, query, args...); err != nil {
		return err
	}
	if conn.Changes() == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

// execConn runs a statement with positional args. (The test files define their
// own exec helper; this is the production-build equivalent.)
func execConn(conn *sqlite.Conn, query string, args ...any) error {
	return sqlitex.Execute(conn, query, &sqlitex.ExecOptions{Args: args})
}

// nullableID maps a 0 id to SQL NULL and any positive id to itself, for the
// nullable foreign keys (impersonated_account_id, current_game_id).
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
