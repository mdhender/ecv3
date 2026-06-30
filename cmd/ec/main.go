// Command ec is the ecv3 server: a single static binary serving the
// JSON API + SSE and the embedded Ember SPA.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "ec: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	return nil
}
