// Command kurn-searchd is the fail-closed private/local composition boundary.
package main

import (
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/kurn-dev/kurn-search/internal/localproof"
)

// defaultGCPercent replaces Go's default GC target (100) unless GOGC is set
// explicitly. searchd's live heap is a few MB of short-lived per-request
// garbage, so at 100 the collector ran almost continuously (about 17% of
// CPU). Measured on the W9 test host: about -30% CPU per request for about
// +13 MB resident memory.
const defaultGCPercent = 400

func main() {
	if _, set := os.LookupEnv("GOGC"); !set {
		debug.SetGCPercent(defaultGCPercent)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	os.Exit(localproof.Run(os.Args[1:], signals, os.Stderr))
}
