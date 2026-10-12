package preparedsetup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestFakeSubprocessBounds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	d := t.TempDir()
	p := filepath.Join(d, "fake")
	os.WriteFile(p, []byte("#!/bin/sh\nprintf ok\n"), 0700)
	r, e := runCommand(context.Background(), d, p)
	if e != nil || string(r.stdout) != "ok" {
		t.Fatalf("%v %q", e, r.stdout)
	}
	os.WriteFile(p, []byte("#!/bin/sh\nsleep 1\n"), 0700)
	if _, e := runCommandWithLimits(context.Background(), d, p, commandLimits{timeout: 20 * time.Millisecond, waitDelay: 10 * time.Millisecond}); e == nil {
		t.Fatal("deadline accepted")
	}
}

func TestFakeSubprocessDescendantHeldStdoutReturnsErrWaitDelay(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("assigned Darwin proof")
	}
	d := t.TempDir()
	script, handshake, pidfile := filepath.Join(d, "fake"), filepath.Join(d, "held"), filepath.Join(d, "pid")
	body := "#!/bin/sh\n( printf held > '" + handshake + "'; exec /bin/sleep 5 ) &\nprintf '%s' \"$!\" > '" + pidfile + "'\nwhile [ ! -f '" + handshake + "' ]; do :; done\nprintf ok\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	var descendant int
	t.Cleanup(func() {
		if descendant <= 0 {
			return
		}
		_ = syscall.Kill(descendant, syscall.SIGTERM)
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if err := syscall.Kill(descendant, 0); errors.Is(err, syscall.ESRCH) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Errorf("descendant %d did not terminate", descendant)
	})
	result, err := runCommandWithLimits(context.Background(), d, script, commandLimits{timeout: 2 * time.Second, waitDelay: 50 * time.Millisecond})
	raw, readErr := os.ReadFile(pidfile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	descendant, readErr = strconv.Atoi(string(raw))
	if readErr != nil || descendant <= 0 {
		t.Fatalf("descendant pid: %q/%v", raw, readErr)
	}
	if !errors.Is(err, exec.ErrWaitDelay) || string(result.stdout) != "ok" {
		t.Fatalf("wanted ErrWaitDelay after parent exit0, got %v/%q", err, result.stdout)
	}
	if raw, readErr = os.ReadFile(handshake); readErr != nil || string(raw) != "held" {
		t.Fatalf("descendant handshake: %q/%v", raw, readErr)
	}
}

func TestFakeSubprocessOverflow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("subprocess fixture")
	}
	if _, err := runCommand(context.Background(), t.TempDir(), os.Args[0], "-test.run=^TestPreparedSetupOverflowHelper$", "--", "overflow"); err == nil {
		t.Fatal("accepted stdout larger than 1 MiB")
	}
}

func TestPreparedSetupOverflowHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "overflow" {
		return
	}
	_, _ = os.Stdout.Write(bytes.Repeat([]byte{'x'}, maxTranscript+1))
	os.Exit(0)
}
