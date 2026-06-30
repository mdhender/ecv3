// Command ec is the ecv3 server: a single static binary serving the
// JSON API + SSE and the embedded Ember SPA.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mdhender/ecv3"
	"github.com/mdhender/ecv3/server/api"
	"github.com/mdhender/ecv3/server/auth"
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
	// Default true: sessions are bound to their origin IP (re-auth on change).
	// Disable (--session-bind-ip=false) for clients whose IP rotates, e.g. a VPN.
	bindIP := serveFlags.BoolLongDefault("session-bind-ip", true,
		"bind each session to its origin IP; re-auth on IP change (disable for rotating-IP/VPN clients)")
	// Caddy terminates TLS on the same host in dev/prod, so loopback is the
	// trusted proxy by default. X-Forwarded-For is honored only from these.
	trustedProxies := serveFlags.StringLong("trusted-proxies", "127.0.0.0/8,::1/128",
		"comma-separated CIDRs of trusted reverse proxies whose X-Forwarded-For is honored")
	serveCmd := &ff.Command{
		Name:      "serve",
		Usage:     "ec serve [FLAGS]",
		ShortHelp: "run the HTTP server (API + embedded SPA)",
		Flags:     serveFlags,
		Exec: func(ctx context.Context, _ []string) error {
			return serve(ctx, *port, *dataDir, *bindIP, *trustedProxies)
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

	adminCmd := newAdminCmd(rootFlags)

	rootCmd.Subcommands = []*ff.Command{serveCmd, databaseCmd, adminCmd, versionCmd}
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

	seedFlags := ff.NewFlagSet("seed").SetParent(dbFlags)
	seedDataDir := seedFlags.StringLong("data", defaultData(), "data directory holding "+store.Filename)
	seedDev := seedFlags.BoolLong("dev", "confirm this is a dev/test database (required; seeding is never for prod)")
	seedCmd := &ff.Command{
		Name:      "seed",
		Usage:     "ec database seed --dev [--data DIR] <file>",
		ShortHelp: "load accounts/games from a JSON seed file (dev/test only)",
		Flags:     seedFlags,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) != 1 {
				return errors.New("usage: ec database seed --dev <file>")
			}
			return databaseSeed(ctx, args[0], *seedDataDir, *seedDev)
		},
	}

	dbCmd.Subcommands = []*ff.Command{createCmd, seedCmd}
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

// parseCIDRs parses a comma-separated list of CIDRs (e.g. the --trusted-proxies
// flag) into prefixes. An empty string yields no prefixes (trust no proxy).
func parseCIDRs(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", part, err)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func serve(ctx context.Context, port, dataDir string, bindIP bool, trustedProxies string) error {
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

	proxies, err := parseCIDRs(trustedProxies)
	if err != nil {
		return fmt.Errorf("parsing --trusted-proxies: %w", err)
	}

	// Session manager. Secure is always true: cookies are HTTPS-only (TLS is
	// terminated by Caddy in dev/prod, or directly by this binary via autocert).
	sessions := auth.New(st, auth.Config{BindIP: bindIP, Secure: true, TrustedProxies: proxies})

	mux := http.NewServeMux()
	api.New(st, sessions).Register(mux)
	mux.Handle("/", spa)

	// Handler chain (outermost first):
	//   CrossOriginProtection  - stdlib CSRF defense: rejects cross-origin
	//                            unsafe-method requests via Sec-Fetch-Site /
	//                            Origin-vs-Host. Safe methods (incl. SSE GETs)
	//                            pass through untouched.
	//   session middleware     - resolves the cookie into a request identity.
	csrf := http.NewCrossOriginProtection()
	handler := csrf.Handler(sessions.Middleware(mux))

	addr := ":" + port
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		// TLS 1.3 only on any direct-TLS path (autocert). When TLS is terminated
		// by Caddy the binary speaks plain HTTP and this is unused; the edge must
		// likewise be configured TLS 1.3 only. We do not support older clients.
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13},
	}

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
