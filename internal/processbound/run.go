// Package processbound executes one local child under the closed release-tool
// process contract. It has no network, retry, redirect, or shell surface.
package processbound

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

var errRefused = errors.New("processbound: refused")

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (writer *limitedBuffer) Write(raw []byte) (int, error) {
	if writer.buffer.Len()+len(raw) > writer.limit {
		return 0, errRefused
	}
	return writer.buffer.Write(raw)
}

// Run executes path directly with a closed environment and independently
// bounded stdout/stderr. Any stderr, timeout, overflow, or nonzero exit
// refuses and no invocation is retried.
func Run(parent context.Context, timeout time.Duration, directory, path string, limit int, arguments ...string) ([]byte, error) {
	if timeout <= 0 || directory == "" || path == "" || limit < 1 {
		return nil, errRefused
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	stdout := &limitedBuffer{limit: limit}
	stderr := &limitedBuffer{limit: limit}
	command := exec.CommandContext(ctx, path, arguments...)
	command.Dir = directory
	command.Env = []string{"LANG=C", "LC_ALL=C", "TZ=UTC"}
	command.Stdin = nil
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = time.Second
	err := command.Run()
	if ctx.Err() != nil || err != nil || stderr.buffer.Len() != 0 {
		return nil, errRefused
	}
	return bytes.Clone(stdout.buffer.Bytes()), nil
}
