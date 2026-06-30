// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/mdhender/ecv3/server/store"
)

// databaseSeed loads a JSON seed file into an existing database. It is a
// dev/test-only tool: the --dev flag is a required guard so the command can
// never quietly run against a production database. The file path is always
// explicit (never a default) for the same reason.
func databaseSeed(ctx context.Context, file, dataDir string, dev bool) error {
	if !dev {
		return errors.New("refusing to seed without --dev (seeding is for dev/test databases only)")
	}
	if file == "" {
		return errors.New("a seed file path is required")
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("reading seed file: %w", err)
	}

	var seed store.Seed
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&seed); err != nil {
		return fmt.Errorf("parsing seed file %s: %w", file, err)
	}

	st, err := store.Open(dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.ApplySeed(ctx, seed); err != nil {
		return err
	}

	fmt.Printf("seeded %s from %s: %d account(s), %d game(s), %d membership(s)\n",
		dbLocation(dataDir), file, len(seed.Accounts), len(seed.Games), len(seed.Members))
	return nil
}
