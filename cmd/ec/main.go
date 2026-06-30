// Command ec is the ecv3 server: a single static binary serving the
// JSON API + SSE and the embedded Ember SPA.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mdhender/ecv3"
	"github.com/mdhender/ecv3/server/store"
	"github.com/mdhender/ecv3/server/web"
	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "ec: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	rootFlags := ff.NewFlagSet("ec")
	rootCmd := &ff.Command{
		Name:      "ec",
		Usage:     "ec <SUBCOMMAND> [FLAGS]",
		ShortHelp: "ecv3 server and tools",
		Flags:     rootFlags,
	}

	serveFlags := ff.NewFlagSet("serve").SetParent(rootFlags)
	port := serveFlags.StringLong("port", defaultPort(), "TCP port to listen on")
	dataDir := serveFlags.StringLong("data", defaultData(), "data directory holding "+store.Filename)
	serveCmd := &ff.Command{
		Name:      "serve",
		Usage:     "ec serve [FLAGS]",
		ShortHelp: "run the HTTP server (API + embedded SPA)",
		Flags:     serveFlags,
		Exec: func(ctx context.Context, _ []string) error {
			return serve(ctx, *port, *dataDir)
		},
	}

	databaseCmd := newDatabaseCmd(rootFlags)

	versionCmd := &ff.Command{
		Name:      "version",
		Usage:     "ec version",
		ShortHelp: "print the version and exit",
		Flags:     ff.NewFlagSet("version").SetParent(rootFlags),
		Exec: func(context.Context, []string) error {
			fmt.Println(ecv3.Version().Core())
			return nil
		},
	}

	rootCmd.Subcommands = []*ff.Command{serveCmd, databaseCmd, versionCmd}
	// Bare `ec` (no subcommand) prints help.
	rootCmd.Exec = func(context.Context, []string) error {
		fmt.Fprint(os.Stderr, ffhelp.Command(rootCmd))
		return nil
	}

	if err := rootCmd.ParseAndRun(ctx, args); err != nil {
		if errors.Is(err, ff.ErrHelp) {
			fmt.Fprint(os.Stderr, ffhelp.Command(rootCmd))
			return nil
		}
		return err
	}
	return nil
}

// newDatabaseCmd builds the `ec database` command tree. Database creation is a
// deliberate, separate step (never a side effect of serving), so it lives under
// its own subcommand rather than as a flag on serve.
func newDatabaseCmd(rootFlags *ff.FlagSet) *ff.Command {
	dbFlags := ff.NewFlagSet("database").SetParent(rootFlags)
	dbCmd := &ff.Command{
		Name:      "database",
		Usage:     "ec database <SUBCOMMAND>",
		ShortHelp: "manage the SQLite database",
		Flags:     dbFlags,
	}

	createFlags := ff.NewFlagSet("create").SetParent(dbFlags)
	createCmd := &ff.Command{
		Name:      "create",
		Usage:     "ec database create <path>",
		ShortHelp: "create a new database in <path> (an existing dir) and apply migrations",
		Flags:     createFlags,
		Exec: func(_ context.Context, args []string) error {
			if len(args) != 1 {
				return errors.New("usage: ec database create <path>")
			}
			dir := args[0]
			st, err := store.Create(dir)
			if err != nil {
				return err
			}
			defer st.Close()
			fmt.Printf("created database at %s\n", dbLocation(dir))
			return nil
		},
	}

	dbCmd.Subcommands = []*ff.Command{createCmd}
	// Bare `ec database` (no subcommand) prints help.
	dbCmd.Exec = func(context.Context, []string) error {
		fmt.Fprint(os.Stderr, ffhelp.Command(dbCmd))
		return nil
	}
	return dbCmd
}

// dbLocation describes where the database lives for human-facing messages.
func dbLocation(dir string) string {
	if dir == store.MemoryPath {
		return "an in-memory database"
	}
	return filepath.Join(dir, store.Filename)
}

// defaultPort honors the PORT env var (set by air in dev) and otherwise
// defaults to 8080. The --port flag overrides it.
func defaultPort() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8080"
}

// defaultData honors the ECV3_DATA env var (set in production by the systemd
// unit) and otherwise defaults to a local ./data dir for dev. The --data flag
// overrides it.
func defaultData() string {
	if d := os.Getenv("ECV3_DATA"); d != "" {
		return d
	}
	return "data"
}

func serve(ctx context.Context, port, dataDir string) error {
	// Open the database and apply migrations BEFORE serving. serve never
	// creates a database; a missing one is a fast, clear failure.
	st, err := store.Open(dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	spa, err := web.Handler()
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := st.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/", spa)

	addr := ":" + port
	srv := &http.Server{Addr: addr, Handler: mux}

	// Graceful shutdown: on SIGINT/SIGTERM, stop accepting requests, drain
	// in-flight ones, then let the deferred st.Close() release the pool.
	shutdownCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("ec: listening on %s", addr)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-shutdownCtx.Done():
		log.Printf("ec: shutting down")
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(timeoutCtx)
	}
}
