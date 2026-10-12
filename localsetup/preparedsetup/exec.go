package preparedsetup

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

const maxTranscript = 1 << 20

type commandResult struct{ stdout, stderr []byte }

type commandLimits struct {
	timeout   time.Duration
	waitDelay time.Duration
}

type commandError struct{ cause error }

func (e *commandError) Error() string { return errRefused.Error() }
func (e *commandError) Unwrap() error { return e.cause }

type cappedWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	cancel   context.CancelFunc
	overflow bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len()+len(p) > maxTranscript {
		w.overflow = true
		w.cancel()
		return 0, errRefused
	}
	return w.buf.Write(p)
}
func (w *cappedWriter) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.buf.Bytes())
}

func runCommand(parent context.Context, cwd, exe string, args ...string) (commandResult, error) {
	return runCommandWithLimits(parent, cwd, exe, commandLimits{timeout: 10 * time.Minute, waitDelay: 5 * time.Second}, args...)
}

func runCommandWithLimits(parent context.Context, cwd, exe string, limits commandLimits, args ...string) (commandResult, error) {
	if limits.timeout <= 0 || limits.waitDelay <= 0 {
		return commandResult{}, &commandError{cause: errRefused}
	}
	ctx, cancel := context.WithTimeout(parent, limits.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = cwd
	cmd.Env = []string{"TMPDIR=" + cwd}
	cmd.Stdin = nil
	cmd.WaitDelay = limits.waitDelay
	out, errout := &cappedWriter{cancel: cancel}, &cappedWriter{cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = errout
	err := cmd.Run()
	result := commandResult{stdout: out.bytes(), stderr: errout.bytes()}
	if err != nil || out.overflow || errout.overflow || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		cause := err
		if cause == nil {
			cause = ctx.Err()
		}
		if cause == nil {
			cause = errRefused
		}
		return result, &commandError{cause: cause}
	}
	return result, nil
}
