// Command ec is the ecv3 server: a single static binary serving the
// JSON API + SSE and the embedded Ember SPA.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/mdhender/ecv3/server/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ec: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// TODO: adopt ff/v4 (FlagSet/Command) for config per CLAUDE.md.
	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}

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

	log.Printf("ec: listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}
