package main

import (
	"os"
	"testing"
	"time"

	"github.com/kurn-dev/kurn-search/internal/localproof"
)

// TestProfileServe runs the unchanged searchd serve path (localproof.Run)
// for a fixed duration, so a `go test -c` binary started with
// -test.cpuprofile/-test.memprofile captures searchd under external load.
// It is skipped unless KURN_SEARCHD_PROFILE_CONFIG names a searchd config;
// KURN_SEARCHD_PROFILE_DURATION bounds the run (it then stops as on SIGINT).
func TestProfileServe(t *testing.T) {
	config := os.Getenv("KURN_SEARCHD_PROFILE_CONFIG")
	if config == "" {
		t.Skip("profiling run disabled (set KURN_SEARCHD_PROFILE_CONFIG and KURN_SEARCHD_PROFILE_DURATION)")
	}
	d, err := time.ParseDuration(os.Getenv("KURN_SEARCHD_PROFILE_DURATION"))
	if err != nil || d <= 0 || d > 10*time.Minute {
		t.Fatalf("KURN_SEARCHD_PROFILE_DURATION must be a duration in (0, 10m]: %v", err)
	}
	signals := make(chan os.Signal, 1)
	stop := time.AfterFunc(d, func() { signals <- os.Interrupt })
	defer stop.Stop()
	if code := localproof.Run([]string{"-config", config}, signals, os.Stderr); code != 0 {
		t.Fatalf("searchd exited %d", code)
	}
}
