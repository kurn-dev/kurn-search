package processbound

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

func TestClosedProcessBoundary(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(timeout time.Duration, mode string) ([]byte, error) {
		return Run(context.Background(), timeout, t.TempDir(), executable, 1<<20, "-test.run=TestProcessFixture", "--", mode)
	}
	output, err := run(5*time.Second, "success")
	if err != nil || string(output) != "ok" {
		t.Fatalf("bounded success mismatch: %q %v", output, err)
	}
	for _, mode := range []string{"failure", "stderr", "overflow", "timeout"} {
		limit := 5 * time.Second
		if mode == "timeout" {
			limit = time.Millisecond
		}
		if _, err := run(limit, mode); err == nil {
			t.Fatalf("bounded process %s accepted", mode)
		}
	}
}

func TestProcessFixture(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	switch os.Args[separator+1] {
	case "success":
		_, _ = os.Stdout.WriteString("ok")
	case "failure":
		os.Exit(7)
	case "stderr":
		_, _ = os.Stderr.WriteString("unexpected")
	case "overflow":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte{'x'}, 1<<20+1))
	case "timeout":
		time.Sleep(time.Second)
	default:
		os.Exit(8)
	}
	os.Exit(0)
}
