package capture

import (
	"context"
	"io"
	"os/exec"
	"sync"
)

// Result holds the evidence collected from one trial run.
type Result struct {
	// Stdout and Stderr are final snapshots of the two output streams.
	Stdout Snapshot
	Stderr Snapshot

	// ExitErr is the process exit error (nil on clean exit).
	ExitErr error

	// Canceled reports that parent context cancellation stopped the trial.
	Canceled bool
}

// Run starts cmd and drains its stdout and stderr into bounded Captures.
//
// Run takes ownership of cmd.Stdout and cmd.Stderr; any prior values are
// overwritten. The same Config applies independently to each stream, so each
// stream is bounded on its own.
//
// On parent context cancellation the trial process (and its process group on
// Unix) is killed first; Run then blocks until both output pipes reach EOF,
// preserving every byte already received, and only then reaps the process.
// The returned Result always contains the evidence available at termination.
func Run(parent context.Context, cmd *exec.Cmd, cfg Config) (*Result, error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	stdoutCap := New(cfg)
	stderrCap := New(cfg)

	setNewProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	drain := func(dst *Capture, src io.Reader) {
		defer wg.Done()
		defer dst.Close()
		_, _ = io.Copy(dst, src)
	}
	go drain(stdoutCap, stdoutPipe)
	go drain(stderrCap, stderrPipe)

	drained := make(chan struct{})
	go func() {
		wg.Wait()
		close(drained)
	}()

	// Wait only after the pipes are fully drained: exec.Cmd.Wait closes the
	// read ends itself, and calling it early would truncate evidence.
	finished := make(chan error, 1)
	go func() {
		<-drained
		finished <- cmd.Wait()
	}()

	result := &Result{}
	select {
	case result.ExitErr = <-finished:
	case <-parent.Done():
		result.Canceled = true
		killProcessGroup(cmd)
		result.ExitErr = <-finished
	}

	result.Stdout = stdoutCap.Snapshot()
	result.Stderr = stderrCap.Snapshot()
	return result, nil
}
