package adapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	maxEventBytes      = 256 << 10
	maxOutputBytes     = 4 << 20
	maxStderrBytes     = 8 << 10
	maxPromptBytes     = 32 << 10
	maxArgvPromptBytes = 16 << 10
	maxDeltaBytes      = 4 << 10
	commandTimeout     = 10 * time.Minute
	statusTimeout      = 10 * time.Second
)

type commandFactory func(context.Context, ...string) *exec.Cmd

type outputBudget struct {
	mu     sync.Mutex
	total  int
	err    error
	cancel context.CancelFunc
}

func (b *outputBudget) fail(err error) error {
	b.mu.Lock()
	if b.err == nil {
		b.err = err
	}
	first := b.err
	b.mu.Unlock()
	b.cancel()
	return first
}

func (b *outputBudget) consume(n int) error {
	b.mu.Lock()
	b.total += n
	exceeded := b.total > maxOutputBytes
	b.mu.Unlock()
	if exceeded {
		return b.fail(&ExecutionError{Code: "output_limit", Stage: "stdout", Indeterminate: true})
	}
	return nil
}

// lineOutput never retains more than one bounded native event. Its callback runs
// on exec's stdout-copy goroutine, so downstream backpressure reaches the child.
type lineOutput struct {
	budget *outputBudget
	line   []byte
	onLine func([]byte) error
}

func (w *lineOutput) Write(p []byte) (int, error) {
	if err := w.budget.consume(len(p)); err != nil {
		return 0, err
	}
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		part := p
		if i >= 0 {
			part = p[:i]
		}
		if len(w.line)+len(part) > maxEventBytes {
			return 0, w.budget.fail(&ExecutionError{Code: "event_limit", Stage: "stdout", Indeterminate: true})
		}
		w.line = append(w.line, part...)
		if i < 0 {
			break
		}
		if err := w.flush(); err != nil {
			return 0, w.budget.fail(err)
		}
		p = p[i+1:]
	}
	return n, nil
}

func (w *lineOutput) flush() error {
	line := bytes.TrimSpace(w.line)
	if len(line) > 0 {
		if err := w.onLine(line); err != nil {
			return err
		}
	}
	w.line = w.line[:0]
	return nil
}

type boundedOutput struct {
	budget  *outputBudget
	stage   string
	limit   int
	n       int
	data    []byte
	capture bool
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	if err := w.budget.consume(len(p)); err != nil {
		return 0, err
	}
	if w.n+len(p) > w.limit {
		return 0, w.budget.fail(&ExecutionError{Code: "output_limit", Stage: w.stage, Indeterminate: true})
	}
	w.n += len(p)
	if w.capture {
		w.data = append(w.data, p...)
	}
	return len(p), nil
}

// runProcess owns cancellation, pipe draining, quotas and child-tree cleanup.
// Neither argv, stderr nor native stdout is included in returned diagnostics.
func runProcess(ctx context.Context, factory commandFactory, args []string, stdin string, onLine func([]byte) error, timeout time.Duration) ([]byte, error) {
	ctx, timeoutCancel := context.WithTimeout(ctx, timeout)
	defer timeoutCancel()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := factory(runCtx, args...)
	cmd.Stdin = nil
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.WaitDelay = 2 * time.Second
	attach, cleanup, err := configureProcess(cmd)
	if err != nil {
		return nil, &ExecutionError{Code: "process_setup", Stage: "start", Err: err}
	}
	defer cleanup()
	budget := &outputBudget{cancel: cancel}
	stderr := &boundedOutput{budget: budget, stage: "stderr", limit: maxStderrBytes}
	stdout := &boundedOutput{budget: budget, stage: "stdout", limit: maxEventBytes, capture: true}
	var lines *lineOutput
	if onLine != nil {
		lines = &lineOutput{budget: budget, onLine: onLine}
		cmd.Stdout = lines
	} else {
		cmd.Stdout = stdout
	}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, cancellationError(ctx.Err(), false)
		}
		return nil, &ExecutionError{Code: "start_failed", Stage: "start", Err: errors.New("native CLI could not be started")}
	}
	if err := attach(); err != nil {
		cancel()
		_ = cmd.Wait()
		return nil, &ExecutionError{Code: "process_setup", Stage: "start", Indeterminate: true, Err: err}
	}
	waitErr := cmd.Wait()
	budget.mu.Lock()
	outputErr := budget.err
	budget.mu.Unlock()
	if outputErr != nil {
		return nil, outputErr
	}
	if ctx.Err() != nil {
		return nil, cancellationError(ctx.Err(), true)
	}
	if waitErr != nil {
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			return nil, &ExecutionError{Code: "exit_failed", Stage: "execution", Indeterminate: true, Err: fmt.Errorf("native CLI exited with code %d; if resuming an unavailable session, create a new session explicitly", exit.ExitCode())}
		}
		return nil, &ExecutionError{Code: "process_failed", Stage: "execution", Indeterminate: true, Err: errors.New("native CLI output or process cleanup failed")}
	}
	if lines != nil {
		if err := lines.flush(); err != nil {
			return nil, err
		}
	}
	return stdout.data, nil
}

func cancellationError(err error, started bool) error {
	code := "canceled"
	if errors.Is(err, context.DeadlineExceeded) {
		code = "timeout"
	}
	return &ExecutionError{Code: code, Stage: "execution", Indeterminate: started, Err: err}
}

// Used only for small read-only CLI probes, never for model execution.
func captureCommand(ctx context.Context, factory commandFactory, args ...string) ([]byte, error) {
	return runProcess(ctx, factory, args, "", nil, statusTimeout)
}

var _ io.Writer = (*lineOutput)(nil)
var _ io.Writer = (*boundedOutput)(nil)
