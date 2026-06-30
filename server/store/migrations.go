// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package store

import (
	"embed"
	"io/fs"
	"sort"
	"strings"

	"zombiezen.com/go/sqlite/sqlitemigration"
)

// appID is written to the database (PRAGMA application_id) by the migration
// machinery to identify ecv3 databases. It is the ASCII bytes "ecv3"
// (0x65 0x63 0x76 0x33) and must never change for the lifetime of the program.
const appID int32 = 0x65637633

//go:embed migrations/*.sql
var migrationFS embed.FS

// schema is the full, ordered migration set. Both Create and Open apply it, so
// the schema definition lives in exactly one place. sqlitemigration tracks how
// far a database has progressed, so re-applying a current schema is a no-op.
var schema = loadSchema()

// loadSchema reads every migrations/*.sql file in lexical order. The numeric
// filename prefix (0001_, 0002_, …) defines apply order, so new migrations are
// added by dropping in a higher-numbered file — never by editing an old one.
func loadSchema() sqlitemigration.Schema {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		panic("store: reading embedded migrations: " + err.Error())
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	migrations := make([]string, 0, len(names))
	for _, name := range names {
		b, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			panic("store: reading embedded migration " + name + ": " + err.Error())
		}
		migrations = append(migrations, string(b))
	}

	return sqlitemigration.Schema{
		AppID:      appID,
		Migrations: migrations,
	}
}
