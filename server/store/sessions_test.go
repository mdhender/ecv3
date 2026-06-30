// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package store

import (
	"context"
	"errors"
	"testing"
)

// fixed, deterministic timestamps (unix seconds) so tests never read the clock.
const (
	tCreated = 1_700_000_000
	tExpires = 1_700_086_400 // +24h
)

// newSessionAccount inserts an account and returns its id, for session tests.
func newSessionAccount(t *testing.T, s *Store, email string) int64 {
	t.Helper()
	id, err := s.CreateAccount(context.Background(), email, "pw", false)
	if err != nil {
		t.Fatalf("CreateAccount(%q): %v", email, err)
	}
	return id
}

func TestCreateAndGetSession(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	acct := newSessionAccount(t, s, "a@example.com")

	if err := s.CreateSession(ctx, "hash1", acct, "10.0.0.1", "agent/1", tCreated, tExpires, tCreated); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, found, err := s.GetSession(ctx, "hash1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if !found {
		t.Fatal("GetSession: found = false; want true")
	}
	want := Session{
		IDHash: "hash1", AccountID: acct,
		ImpersonatedAccountID: 0, CurrentGameID: 0,
		CreatedAt: tCreated, ExpiresAt: tExpires, LastSeenAt: tCreated,
		IP: "10.0.0.1", UserAgent: "agent/1",
	}
	if got != want {
		t.Errorf("session = %+v; want %+v", got, want)
	}
}

func TestGetSessionMissing(t *testing.T) {
	s := memStore(t)
	_, found, err := s.GetSession(context.Background(), "nope")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if found {
		t.Error("found = true for a missing session; want false")
	}
}

func TestCreateSessionDuplicate(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	acct := newSessionAccount(t, s, "a@example.com")

	if err := s.CreateSession(ctx, "dup", acct, "", "", tCreated, tExpires, tCreated); err != nil {
		t.Fatalf("first CreateSession: %v", err)
	}
	err := s.CreateSession(ctx, "dup", acct, "", "", tCreated, tExpires, tCreated)
	if !errors.Is(err, ErrSessionExists) {
		t.Fatalf("duplicate CreateSession err = %v; want ErrSessionExists", err)
	}
}

func TestTouchSession(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	acct := newSessionAccount(t, s, "a@example.com")
	if err := s.CreateSession(ctx, "h", acct, "10.0.0.1", "agent/1", tCreated, tExpires, tCreated); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	const newSeen, newExp = tCreated + 100, tExpires + 100
	if err := s.TouchSession(ctx, "h", "10.0.0.2", "agent/2", newSeen, newExp); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	got, _, err := s.GetSession(ctx, "h")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.LastSeenAt != newSeen || got.ExpiresAt != newExp || got.IP != "10.0.0.2" || got.UserAgent != "agent/2" {
		t.Errorf("after touch: %+v; want last_seen=%d exp=%d ip=10.0.0.2 ua=agent/2", got, newSeen, newExp)
	}
}

func TestTouchSessionMissing(t *testing.T) {
	s := memStore(t)
	err := s.TouchSession(context.Background(), "ghost", "", "", tCreated, tExpires)
	if err == nil {
		t.Fatal("TouchSession on missing session = nil; want error")
	}
}

func TestSetCurrentGameAndImpersonation(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	admin := newSessionAccount(t, s, "admin@example.com")
	target := newSessionAccount(t, s, "player@example.com")
	gameID, err := s.CreateGame(ctx, "alpha")
	if err != nil {
		t.Fatalf("CreateGame: %v", err)
	}
	if err := s.CreateSession(ctx, "h", admin, "", "", tCreated, tExpires, tCreated); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.SetCurrentGame(ctx, "h", gameID); err != nil {
		t.Fatalf("SetCurrentGame: %v", err)
	}
	if err := s.SetImpersonation(ctx, "h", target); err != nil {
		t.Fatalf("SetImpersonation: %v", err)
	}
	got, _, err := s.GetSession(ctx, "h")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.CurrentGameID != gameID || got.ImpersonatedAccountID != target {
		t.Errorf("got game=%d impersonated=%d; want game=%d impersonated=%d",
			got.CurrentGameID, got.ImpersonatedAccountID, gameID, target)
	}

	// Clearing with 0 stores NULL (read back as 0).
	if err := s.SetCurrentGame(ctx, "h", 0); err != nil {
		t.Fatalf("SetCurrentGame(clear): %v", err)
	}
	if err := s.SetImpersonation(ctx, "h", 0); err != nil {
		t.Fatalf("SetImpersonation(clear): %v", err)
	}
	got, _, _ = s.GetSession(ctx, "h")
	if got.CurrentGameID != 0 || got.ImpersonatedAccountID != 0 {
		t.Errorf("after clear: game=%d impersonated=%d; want both 0", got.CurrentGameID, got.ImpersonatedAccountID)
	}
}

// TestImpersonateSelfRejected confirms the schema CHECK forbids a session
// impersonating its own account.
func TestImpersonateSelfRejected(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	acct := newSessionAccount(t, s, "a@example.com")
	if err := s.CreateSession(ctx, "h", acct, "", "", tCreated, tExpires, tCreated); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.SetImpersonation(ctx, "h", acct); err == nil {
		t.Fatal("SetImpersonation to own account = nil; want CHECK violation")
	}
}

// TestAccountCascadeDeletesSessions confirms the ON DELETE CASCADE FK.
func TestAccountCascadeDeletesSessions(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	acct := newSessionAccount(t, s, "a@example.com")
	if err := s.CreateSession(ctx, "h", acct, "", "", tCreated, tExpires, tCreated); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	c := conn(t, s)
	mustExec(t, c, "DELETE FROM accounts WHERE id=?;", acct)
	if n := queryInt(t, c, "SELECT count(*) FROM sessions WHERE id_hash=?;", "h"); n != 0 {
		t.Errorf("sessions after account delete = %d; want 0 (cascade)", n)
	}
}

func TestDeleteSession(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	acct := newSessionAccount(t, s, "a@example.com")
	if err := s.CreateSession(ctx, "h", acct, "", "", tCreated, tExpires, tCreated); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.DeleteSession(ctx, "h"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, found, _ := s.GetSession(ctx, "h"); found {
		t.Error("session still present after DeleteSession")
	}
	// Deleting a missing session is not an error.
	if err := s.DeleteSession(ctx, "h"); err != nil {
		t.Errorf("DeleteSession(missing) = %v; want nil", err)
	}
}

func TestDeleteAccountSessions(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	a1 := newSessionAccount(t, s, "a@example.com")
	a2 := newSessionAccount(t, s, "b@example.com")
	for _, h := range []string{"a-1", "a-2", "a-3"} {
		if err := s.CreateSession(ctx, h, a1, "", "", tCreated, tExpires, tCreated); err != nil {
			t.Fatalf("CreateSession %q: %v", h, err)
		}
	}
	if err := s.CreateSession(ctx, "b-1", a2, "", "", tCreated, tExpires, tCreated); err != nil {
		t.Fatalf("CreateSession b-1: %v", err)
	}

	n, err := s.DeleteAccountSessions(ctx, a1)
	if err != nil {
		t.Fatalf("DeleteAccountSessions: %v", err)
	}
	if n != 3 {
		t.Errorf("deleted = %d; want 3", n)
	}
	if _, found, _ := s.GetSession(ctx, "b-1"); !found {
		t.Error("other account's session was deleted")
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	acct := newSessionAccount(t, s, "a@example.com")
	// expired: expires_at = tCreated+10; live: expires_at = tCreated+1000.
	if err := s.CreateSession(ctx, "old", acct, "", "", tCreated, tCreated+10, tCreated); err != nil {
		t.Fatalf("CreateSession old: %v", err)
	}
	if err := s.CreateSession(ctx, "new", acct, "", "", tCreated, tCreated+1000, tCreated); err != nil {
		t.Fatalf("CreateSession new: %v", err)
	}

	n, err := s.DeleteExpiredSessions(ctx, tCreated+100)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted = %d; want 1", n)
	}
	if _, found, _ := s.GetSession(ctx, "old"); found {
		t.Error("expired session survived")
	}
	if _, found, _ := s.GetSession(ctx, "new"); !found {
		t.Error("live session was deleted")
	}
}
