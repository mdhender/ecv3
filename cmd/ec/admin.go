// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"os"
	"strings"

	psg "github.com/mdhender/ecv3/internal/phrases"
	"github.com/mdhender/ecv3/server/store"
	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"
	"golang.org/x/term"
)

// adminPasswordEnv is the environment variable an operator can set to supply the
// new admin's password non-interactively (provisioning / CI), avoiding both a
// TTY prompt and putting the secret in shell history via a flag.
const adminPasswordEnv = "ECV3_ADMIN_PASSWORD"

// newAdminCmd builds the `ec admin` command tree. Admin bootstrap is a
// deliberate, separate step that operates on an existing database (never a side
// effect of `database create` or `serve`), so it can be re-run to recover a
// locked-out admin.
func newAdminCmd(rootFlags *ff.FlagSet) *ff.Command {
	adminFlags := ff.NewFlagSet("admin").SetParent(rootFlags)
	adminCmd := &ff.Command{
		Name:      "admin",
		Usage:     "ec admin <SUBCOMMAND>",
		ShortHelp: "manage administrator accounts",
		Flags:     adminFlags,
	}

	createFlags := ff.NewFlagSet("create").SetParent(adminFlags)
	email := createFlags.StringLong("email", "", "email address of the admin account (required)")
	dataDir := createFlags.StringLong("data", defaultData(), "data directory holding "+store.Filename)
	generate := createFlags.BoolLong("generate", "generate a random passphrase and print it once")
	seed := createFlags.Uint64Long("seed", 0, "pin the passphrase generator (reproducible; --generate only)")
	createCmd := &ff.Command{
		Name:      "create",
		Usage:     "ec admin create --email <addr> [--generate] [--seed N] [--data DIR]",
		ShortHelp: "create an administrator account in an existing database",
		Flags:     createFlags,
		Exec: func(ctx context.Context, _ []string) error {
			seedSet := false
			if f, ok := createFlags.GetFlag("seed"); ok {
				seedSet = f.IsSet()
			}
			return adminCreate(ctx, *email, *dataDir, *generate, seedSet, *seed)
		},
	}

	adminCmd.Subcommands = []*ff.Command{createCmd}
	adminCmd.Exec = func(context.Context, []string) error {
		fmt.Fprint(os.Stderr, ffhelp.Command(adminCmd))
		return nil
	}
	return adminCmd
}

// adminCreate opens an existing database and creates one admin account. The
// password is resolved from (in order) an interactive TTY prompt, the
// ECV3_ADMIN_PASSWORD env var, or --generate; a generated passphrase is printed
// exactly once. seedSet tells whether --seed was explicitly set, so an unset
// seed draws from crypto/rand.
func adminCreate(ctx context.Context, email, dataDir string, generate, seedSet bool, seed uint64) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return errors.New("--email is required")
	}

	if seedSet && !generate {
		return errors.New("--seed only applies with --generate")
	}

	password, generated, err := resolveAdminPassword(generate, seedSet, seed)
	if err != nil {
		return err
	}

	st, err := store.Open(dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	if _, err := st.CreateAccount(ctx, email, password, true); err != nil {
		if errors.Is(err, store.ErrEmailExists) {
			return fmt.Errorf("an account with email %q already exists", email)
		}
		return err
	}

	fmt.Printf("created admin account %s\n", email)
	if generated {
		// Printed once: the operator must record it now (it is not recoverable).
		fmt.Printf("generated password: %s\n", password)
	}
	return nil
}

// resolveAdminPassword picks the admin password. --generate is an explicit
// opt-in and takes precedence; otherwise an interactive TTY is prompted (no
// echo, with confirmation), then ECV3_ADMIN_PASSWORD is consulted for
// non-interactive use. generated reports whether the caller should print the
// password once.
func resolveAdminPassword(generate, seedSet bool, seed uint64) (password string, generated bool, err error) {
	if generate {
		return generatePassphrase(seedSet, seed), true, nil
	}

	if term.IsTerminal(int(os.Stdin.Fd())) {
		pw, err := promptPassword()
		if err != nil {
			return "", false, err
		}
		return pw, false, nil
	}

	if pw, ok := os.LookupEnv(adminPasswordEnv); ok {
		if pw == "" {
			return "", false, fmt.Errorf("%s is set but empty", adminPasswordEnv)
		}
		return pw, false, nil
	}

	return "", false, fmt.Errorf("no password source: run on a terminal, set %s, or pass --generate", adminPasswordEnv)
}

// promptPassword reads a password twice from the terminal without echoing and
// requires the two entries to match.
func promptPassword() (string, error) {
	fd := int(os.Stdin.Fd())

	fmt.Fprint(os.Stderr, "password: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	if len(first) == 0 {
		return "", errors.New("password must not be empty")
	}

	fmt.Fprint(os.Stderr, "confirm password: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	if string(first) != string(second) {
		return "", errors.New("passwords do not match")
	}
	return string(first), nil
}

// generatePassphrase returns a 5-word, "."-separated passphrase. When seedSet
// is true the generator is pinned by seed (reproducible, for testing); the
// single seed is expanded into the two PCG seeds with SplitMix64 to avoid a
// degenerate NewPCG(seed, seed). When seedSet is false the two PCG seeds are
// drawn from crypto/rand so the password is unpredictable.
func generatePassphrase(seedSet bool, seed uint64) string {
	var s1, s2 uint64
	if seedSet {
		s := seed
		s1, s2 = splitmix64(&s), splitmix64(&s)
	} else {
		s1, s2 = cryptoSeed(), cryptoSeed()
	}
	r := mathrand.New(mathrand.NewPCG(s1, s2))
	return psg.Generate(r, 5, ".")
}

// splitmix64 advances a SplitMix64 state and returns the next well-mixed output.
// It is the canonical way to expand one uint64 seed into a stream of PCG seeds.
func splitmix64(x *uint64) uint64 {
	*x += 0x9e3779b97f4a7c15
	z := *x
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// cryptoSeed returns a uint64 drawn from the OS CSPRNG. crypto/rand.Read never
// fails on the platforms ec targets; a failure here is unrecoverable, so it
// panics rather than silently weakening the seed.
func cryptoSeed() uint64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("ec: crypto/rand unavailable: " + err.Error())
	}
	return binary.LittleEndian.Uint64(b[:])
}
