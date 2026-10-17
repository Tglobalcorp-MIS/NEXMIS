package helper

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/sys"
)

// maxOutput caps stdout and stderr captured from a user-session script.
const maxOutput = 1 << 20 // 1 MiB

// cappedBuffer is an io.Writer that keeps at most max bytes and records whether
// anything beyond that was discarded.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       []byte
	max       int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.buf) < c.max {
		room := c.max - len(c.buf)
		if room >= len(p) {
			c.buf = append(c.buf, p...)
		} else {
			c.buf = append(c.buf, p[:room]...)
			c.truncated = true
		}
	} else {
		c.truncated = true
	}
	return len(p), nil // always claim success so the process is never blocked
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buf)
}

// runScript executes a script in the current (user) session, enforcing a
// timeout and output caps. It is used by TRunScript handling.
func runScript(ctx context.Context, req helperipc.RunScript) helperipc.RunResult {
	res := helperipc.RunResult{ID: req.ID}

	timeout := time.Duration(req.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd, cleanup, err := sys.ScriptCommand(runCtx, req.Shell, req.Script, req.Env)
	if err != nil {
		res.Error = err.Error()
		res.ExitCode = -1
		return res
	}
	defer cleanup()

	var stdout, stderr cappedBuffer
	stdout.max, stderr.max = maxOutput, maxOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	kill, err := sys.StartTracked(cmd)
	if err != nil {
		res.Error = err.Error()
		res.ExitCode = -1
		return res
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var waitErr error
	select {
	case <-runCtx.Done():
		kill()
		<-done // reap
		waitErr = runCtx.Err()
	case waitErr = <-done:
	}

	res.Stdout = stdout.String()
	res.Stderr = stderr.String()
	res.Truncated = stdout.truncated || stderr.truncated

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		res.ExitCode = -1
		res.Error = "script timed out"
	case waitErr == nil:
		res.ExitCode = 0
	default:
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
			res.Error = waitErr.Error()
		}
	}
	return res
}
