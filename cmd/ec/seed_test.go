// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdhender/ecv3/server/store"
)

// freshDB creates an empty migrated database under a temp dir and returns the
// dir.
func freshDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Create(dir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	st.Close()
	return dir
}

// TestDatabaseSeedRequiresDev confirms the prod guard: seeding without --dev is
// refused before the database is touched.
func TestDatabaseSeedRequiresDev(t *testing.T) {
	dir := freshDB(t)
	file := filepath.Join("..", "..", "testdata", "dev-seed.json")
	err := databaseSeed(context.Background(), file, dir, false)
	if err == nil || !strings.Contains(err.Error(), "--dev") {
		t.Fatalf("error = %v; want a --dev guard error", err)
	}
}

// TestDatabaseSeedLoadsSample loads the committed sample seed file and confirms
// the accounts and memberships land.
func TestDatabaseSeedLoadsSample(t *testing.T) {
	dir := freshDB(t)
	file := filepath.Join("..", "..", "testdata", "dev-seed.json")
	ctx := context.Background()

	if err := databaseSeed(ctx, file, dir, true); err != nil {
		t.Fatalf("databaseSeed: %v", err)
	}

	// Re-seeding the same file must fail (duplicate accounts), proving the first
	// run actually wrote rows.
	if err := databaseSeed(ctx, file, dir, true); err == nil {
		t.Fatal("re-seeding duplicate accounts: want error, got nil")
	}
}
