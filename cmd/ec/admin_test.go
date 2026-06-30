// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mdhender/ecv3/server/store"
)

// TestGeneratePassphraseFormat checks the generated passphrase shape: 5 words
// joined by ".".
func TestGeneratePassphraseFormat(t *testing.T) {
	pw := generatePassphrase(true, 42)
	parts := strings.Split(pw, ".")
	if len(parts) != 5 {
		t.Fatalf("passphrase %q has %d words; want 5", pw, len(parts))
	}
	for i, p := range parts {
		if p == "" {
			t.Errorf("word %d is empty in %q", i, pw)
		}
	}
}

// TestGeneratePassphraseSeeded confirms --seed is reproducible and different
// seeds give different passphrases (so the SplitMix64 expansion isn't degenerate).
func TestGeneratePassphraseSeeded(t *testing.T) {
	a := generatePassphrase(true, 12345)
	b := generatePassphrase(true, 12345)
	if a != b {
		t.Fatalf("same seed gave different passphrases: %q vs %q", a, b)
	}
	c := generatePassphrase(true, 67890)
	if a == c {
		t.Fatalf("different seeds gave the same passphrase: %q", a)
	}
}

// TestAdminCreateGenerate creates an admin on a fresh DB with --generate, then
// confirms re-running the same email fails clearly.
func TestAdminCreateGenerate(t *testing.T) {
	dir := t.TempDir()
	if st, err := store.Create(dir); err != nil {
		t.Fatalf("Create: %v", err)
	} else {
		st.Close()
	}

	ctx := context.Background()
	if err := adminCreate(ctx, "ME@example.com", dir, true, true, 99); err != nil {
		t.Fatalf("first adminCreate: %v", err)
	}

	// Re-running the same email (case-insensitively) must fail clearly.
	err := adminCreate(ctx, "me@example.com", dir, true, true, 99)
	if err == nil {
		t.Fatal("re-creating same admin email: want error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v; want an 'already exists' message", err)
	}
}

// TestAdminCreateSeedWithoutGenerate rejects --seed unless --generate is set.
func TestAdminCreateSeedWithoutGenerate(t *testing.T) {
	dir := t.TempDir()
	if st, err := store.Create(dir); err != nil {
		t.Fatalf("Create: %v", err)
	} else {
		st.Close()
	}
	err := adminCreate(context.Background(), "x@example.com", dir, false, true, 1)
	if err == nil || !strings.Contains(err.Error(), "--seed") {
		t.Fatalf("error = %v; want a --seed/--generate message", err)
	}
}
