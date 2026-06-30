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

	"github.com/mdhender/ecv3"
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
	serveCmd := &ff.Command{
		Name:      "serve",
		Usage:     "ec serve [FLAGS]",
		ShortHelp: "run the HTTP server (API + embedded SPA)",
		Flags:     serveFlags,
		Exec: func(ctx context.Context, _ []string) error {
			return serve(ctx, *port)
		},
	}

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

	rootCmd.Subcommands = []*ff.Command{serveCmd, versionCmd}
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

// defaultPort honors the PORT env var (set by air in dev) and otherwise
// defaults to 8080. The --port flag overrides it.
func defaultPort() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8080"
}

func serve(_ context.Context, port string) error {
	spa, err := web.Handler()
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/", spa)

	addr := ":" + port
	log.Printf("ec: listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}
