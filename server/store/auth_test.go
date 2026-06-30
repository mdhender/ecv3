// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package store

import (
	"context"
	"errors"
	"testing"
)

func TestAuthenticateByPassword(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()

	if _, err := s.CreateAccount(ctx, "Alice@Example.com", "s3cr3t", false); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	adminID, err := s.CreateAccount(ctx, "root@example.com", "rootpw", true)
	if err != nil {
		t.Fatalf("CreateAccount admin: %v", err)
	}
	// Deactivate a second account to test the active check.
	inactiveID, err := s.CreateAccount(ctx, "gone@example.com", "pw", false)
	if err != nil {
		t.Fatalf("CreateAccount inactive: %v", err)
	}
	// Deactivate via a borrowed-then-returned connection. We must NOT hold the
	// connection (as the conn(t,s) helper does until cleanup): the single-conn
	// in-memory pool would then deadlock the pooled AuthenticateByPassword calls.
	deactivateAccount(t, s, inactiveID)

	t.Run("success normalizes email and reports admin", func(t *testing.T) {
		acct, err := s.AuthenticateByPassword(ctx, "  ALICE@example.com ", "s3cr3t")
		if err != nil {
			t.Fatalf("AuthenticateByPassword: %v", err)
		}
		if acct.Email != "alice@example.com" || acct.IsAdmin || !acct.IsActive {
			t.Errorf("got %+v; want alice@example.com, not admin, active", acct)
		}
	})

	t.Run("admin flag", func(t *testing.T) {
		acct, err := s.AuthenticateByPassword(ctx, "root@example.com", "rootpw")
		if err != nil {
			t.Fatalf("AuthenticateByPassword: %v", err)
		}
		if acct.ID != adminID || !acct.IsAdmin {
			t.Errorf("got %+v; want id=%d admin", acct, adminID)
		}
	})

	cases := []struct {
		name, email, password string
	}{
		{"wrong password", "alice@example.com", "wrong"},
		{"unknown email", "nobody@example.com", "whatever"},
		{"inactive account", "gone@example.com", "pw"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.AuthenticateByPassword(ctx, tc.email, tc.password); !errors.Is(err, ErrInvalidCredentials) {
				t.Errorf("err = %v; want ErrInvalidCredentials", err)
			}
		})
	}
}

// deactivateAccount sets is_active=0, borrowing and immediately returning a
// pool connection so it never holds the single in-memory connection across other
// store calls.
func deactivateAccount(t *testing.T, s *Store, id int64) {
	t.Helper()
	c, err := s.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer s.Put(c)
	if err := exec(c, "UPDATE accounts SET is_active=0 WHERE id=?;", id); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
}

func TestGetAccount(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	id, err := s.CreateAccount(ctx, "a@example.com", "pw", true)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	acct, found, err := s.GetAccount(ctx, id)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if !found {
		t.Fatal("found = false; want true")
	}
	if acct.ID != id || acct.Email != "a@example.com" || !acct.IsAdmin || !acct.IsActive {
		t.Errorf("got %+v; want id=%d a@example.com admin active", acct, id)
	}

	if _, found, _ := s.GetAccount(ctx, 99999); found {
		t.Error("found = true for a missing account")
	}
}
