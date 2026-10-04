// Package faultinj provides executors that inject controlled faults, used
// to prove the judge never confuses parse failure, run failure, timeout,
// and semantic difference.
package faultinj

import (
	"context"
	"time"

	"difftest/internal/runner"
)

// Behavior selects which fault to inject.
type Behavior string

const (
	Value        Behavior = "value"         // clean run, emit Output
	ParseGarbage Behavior = "parse_garbage" // exit 0 but emit non-numeric junk
	RunFail      Behavior = "runfail"       // non-zero exit
	Hang         Behavior = "hang"          // never finish within Timeout
)

// Executor is a runner.Executor that injects a configured fault.
type Executor struct {
	Label    string
	Behavior Behavior
	Output   string        // stdout for Value / ParseGarbage
	ExitCode int           // exit code for RunFail
	Timeout  time.Duration // deadline used for Hang
}

func (e *Executor) Name() string { return e.Label }

func (e *Executor) Run(ctx context.Context, _ []byte) runner.Outcome {
	switch e.Behavior {
	case Value, ParseGarbage:
		return runner.Outcome{Kind: runner.KindValue, Stdout: e.Output}
	case RunFail:
		code := e.ExitCode
		if code == 0 {
			code = 3
		}
		return runner.Outcome{
			Kind:     runner.KindError,
			Stderr:   "injected run failure",
			ExitCode: code,
		}
	case Hang:
		deadline := e.Timeout
		if deadline <= 0 {
			deadline = time.Second
		}
		timer := time.NewTimer(deadline)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return runner.Outcome{Kind: runner.KindTimeout}
		case <-timer.C:
			return runner.Outcome{Kind: runner.KindTimeout}
		}
	default:
		return runner.Outcome{Kind: runner.KindError, Err: "unknown behavior"}
	}
}
