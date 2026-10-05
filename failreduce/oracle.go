package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Verdict is the outcome of a single oracle judgement. The oracle program
// communicates the verdict through its exit code:
//
//	0 -> Pass         (the target failure does NOT reproduce)
//	1 -> TestFail     (the target test failure reproduces; interesting)
//	2 -> CompileFail  (the configuration does not compile; NOT interesting)
//
// Any other exit code is treated as an oracle malfunction and aborts the
// reduction, because the reduction guarantees rely on a deterministic oracle.
type Verdict int

const (
	Pass Verdict = iota
	TestFail
	CompileFail
)

func (v Verdict) String() string {
	switch v {
	case Pass:
		return "pass"
	case TestFail:
		return "test-failure"
	case CompileFail:
		return "compile-failure"
	}
	return "unknown"
}

// Oracle judges a configuration. Implementations must be deterministic.
type Oracle interface {
	Test(cfg []string) (Verdict, error)
}

// ExecOracle runs an external program as the oracle. The normalized
// configuration (sorted, deduplicated) is passed either as trailing
// arguments or, when useStdin is set, as newline-separated items on stdin.
type ExecOracle struct {
	Path     string
	Args     []string
	UseStdin bool
	Timeout  time.Duration
}

func (e *ExecOracle) Test(cfg []string) (Verdict, error) {
	items := normalize(cfg)
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := append([]string{}, e.Args...)
	var cmd *exec.Cmd
	if e.UseStdin {
		cmd = exec.CommandContext(ctx, e.Path, args...)
		cmd.Stdin = strings.NewReader(strings.Join(items, "\n") + "\n")
	} else {
		cmd = exec.CommandContext(ctx, e.Path, append(args, items...)...)
	}
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return Pass, fmt.Errorf("oracle timed out after %s", timeout)
	}
	if err == nil {
		return Pass, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		switch exitErr.ExitCode() {
		case 1:
			return TestFail, nil
		case 2:
			return CompileFail, nil
		default:
			return Pass, fmt.Errorf("oracle exited with unexpected code %d", exitErr.ExitCode())
		}
	}
	return Pass, fmt.Errorf("failed to run oracle: %w", err)
}

// Stats records how the oracle was exercised during a reduction.
type Stats struct {
	Invocations     int `json:"invocations"`     // actual oracle program runs (cache misses)
	CacheHits       int `json:"cacheHits"`       // judgements served from the cache
	Passes          int `json:"passes"`          // configs judged as passing
	TestFailures    int `json:"testFailures"`    // configs judged as target test failures
	CompileFailures int `json:"compileFailures"` // configs judged as compile failures
}

// CachingOracle memoizes verdicts by normalized configuration key, so
// equivalent configurations (same item set, any order) are judged once.
type CachingOracle struct {
	inner Oracle
	cache map[string]Verdict
	Stats Stats
}

func NewCachingOracle(inner Oracle) *CachingOracle {
	return &CachingOracle{inner: inner, cache: make(map[string]Verdict)}
}

func (c *CachingOracle) Test(cfg []string) (Verdict, error) {
	key := configKey(cfg)
	if verdict, ok := c.cache[key]; ok {
		c.Stats.CacheHits++
		return verdict, nil
	}
	verdict, err := c.inner.Test(cfg)
	if err != nil {
		return Pass, err
	}
	c.cache[key] = verdict
	c.Stats.Invocations++
	switch verdict {
	case Pass:
		c.Stats.Passes++
	case TestFail:
		c.Stats.TestFailures++
	case CompileFail:
		c.Stats.CompileFailures++
	}
	return verdict, nil
}
