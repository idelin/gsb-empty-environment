// Package runner executes a program against an input and classifies the
// outcome as a value, a run error, or a timeout.
package runner

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

// Kind is the classification of a single execution.
type Kind string

const (
	KindValue   Kind = "value"   // exited 0, stdout is the return value
	KindError   Kind = "error"   // non-zero exit or failed to start
	KindTimeout Kind = "timeout" // exceeded the configured deadline
)

// Outcome records everything observed from one execution.
type Outcome struct {
	Kind     Kind          `json:"kind"`
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	ExitCode int           `json:"exit_code"`
	Err      string        `json:"err,omitempty"` // spawn/wait error text, if any
	Duration time.Duration `json:"duration_ns"`
}

// Executor runs one input and reports an Outcome.
type Executor interface {
	Name() string
	Run(ctx context.Context, input []byte) Outcome
}

// CommandExecutor runs an external command, feeding the input on stdin and
// capturing stdout as the return value.
type CommandExecutor struct {
	Argv    []string
	Timeout time.Duration
}

func (c *CommandExecutor) Name() string { return strings.Join(c.Argv, " ") }

func (c *CommandExecutor) Run(ctx context.Context, input []byte) Outcome {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	out := Outcome{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}

	// Timeout is checked first: a process killed by the deadline also
	// returns an error from cmd.Run, and must not be misread as a run error.
	if ctx.Err() == context.DeadlineExceeded {
		out.Kind = KindTimeout
		out.ExitCode = -1
		return out
	}
	if err == nil {
		out.Kind = KindValue
		return out
	}
	out.Kind = KindError
	if exitErr, ok := err.(*exec.ExitError); ok {
		out.ExitCode = exitErr.ExitCode()
	} else {
		out.ExitCode = -1
		out.Err = err.Error()
	}
	return out
}
