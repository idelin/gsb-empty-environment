package difftest

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

// OutcomeKind classifies how a single executor run ended.
type OutcomeKind int

const (
	// OutcomeValue means the process exited 0 and produced output.
	OutcomeValue OutcomeKind = iota
	// OutcomeRunError means the process failed to start or exited non-zero.
	OutcomeRunError
	// OutcomeTimeout means the process was killed after the deadline.
	OutcomeTimeout
)

func (k OutcomeKind) String() string {
	switch k {
	case OutcomeValue:
		return "value"
	case OutcomeRunError:
		return "run_error"
	case OutcomeTimeout:
		return "timeout"
	}
	return "unknown"
}

// maxCapture bounds captured stdout/stderr to keep artifacts small.
const maxCapture = 1 << 20

// Outcome is the recorded result of one executor run.
type Outcome struct {
	Kind      OutcomeKind `json:"kind"`
	Stdout    string      `json:"stdout"`
	Stderr    string      `json:"stderr"`
	ExitCode  int         `json:"exit_code"` // -1 when not applicable
	Duration  Duration    `json:"duration"`
	SpawnErr  string      `json:"spawn_err,omitempty"`
	Truncated bool        `json:"truncated,omitempty"`
}

// Executor is one side of the differential pair.
type Executor struct {
	Name     string   `json:"name"`
	Argv     []string `json:"argv"`
	Timeout  Duration `json:"timeout"`
	ExtraEnv []string `json:"extra_env,omitempty"`
}

// limitedBuffer caps how much output is retained.
type limitedBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := maxCapture - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

// Run executes the command with input on stdin and records the outcome.
// A timeout is reported as OutcomeTimeout, never as an error or a value.
func (e Executor) Run(ctx context.Context, input []byte) Outcome {
	if e.Timeout <= 0 {
		e.Timeout = Duration(5 * time.Second)
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(e.Timeout))
	defer cancel()

	cmd := exec.CommandContext(runCtx, e.Argv[0], e.Argv[1:]...)
	setupCmd(cmd)
	if len(e.ExtraEnv) > 0 {
		cmd.Env = append(os.Environ(), e.ExtraEnv...)
	}
	cmd.Stdin = bytes.NewReader(input)
	stdout := &limitedBuffer{}
	stderr := &limitedBuffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()
	runErr := cmd.Run()
	out := Outcome{
		Stdout:    stdout.buf.String(),
		Stderr:    stderr.buf.String(),
		ExitCode:  -1,
		Duration:  Duration(time.Since(start)),
		Truncated: stdout.truncated || stderr.truncated,
	}

	if runCtx.Err() == context.DeadlineExceeded {
		out.Kind = OutcomeTimeout
		return out
	}
	if runErr == nil {
		out.Kind = OutcomeValue
		out.ExitCode = 0
		return out
	}
	out.Kind = OutcomeRunError
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		out.ExitCode = exitErr.ExitCode()
	} else {
		out.SpawnErr = runErr.Error()
	}
	return out
}
