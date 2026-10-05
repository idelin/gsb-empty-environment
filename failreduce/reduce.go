package main

import (
	"errors"
	"fmt"
)

// ErrIllegalConfig marks an initial configuration that violates dependencies.
var ErrIllegalConfig = errors.New("illegal initial configuration")

// ErrNotFailing marks an initial configuration that does not reproduce the
// target test failure, so there is nothing to reduce.
var ErrNotFailing = errors.New("initial configuration does not fail")

// Reduce shrinks a failing configuration to a 1-minimal one: in the result,
// deleting any single item (together with everything that depends on it, to
// keep the configuration legal) no longer reproduces the target test
// failure.
//
// Only configurations that remain dependency-legal are ever presented to the
// oracle. Compile failures are not treated as the target failure: a
// candidate is accepted only when the oracle reports TestFail.
func Reduce(initial []string, deps *Deps, oracle Oracle) ([]string, error) {
	cfg := normalize(initial)
	if len(cfg) == 0 {
		return nil, fmt.Errorf("%w: configuration is empty", ErrIllegalConfig)
	}
	if err := deps.CheckLegal(cfg); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIllegalConfig, err)
	}
	verdict, err := oracle.Test(cfg)
	if err != nil {
		return nil, err
	}
	if verdict != TestFail {
		return nil, fmt.Errorf("%w (verdict: %s)", ErrNotFailing, verdict)
	}

	if cfg, err = reduceByChunks(cfg, deps, oracle); err != nil {
		return nil, err
	}
	if cfg, err = reduceToOneMinimal(cfg, deps, oracle); err != nil {
		return nil, err
	}
	return cfg, nil
}

// reduceByChunks is the coarse ddmin-style phase: it tries to discard large
// groups of items at once so the expensive single-item phase runs on an
// already small configuration.
func reduceByChunks(cfg []string, deps *Deps, oracle Oracle) ([]string, error) {
	n := 2
	for len(cfg) >= 2 {
		if n > len(cfg) {
			n = len(cfg)
		}
		reduced := false
		for _, chunk := range partition(cfg, n) {
			candidate := deps.RemovalClosure(cfg, chunk)
			if len(candidate) == len(cfg) {
				continue
			}
			verdict, err := oracle.Test(candidate)
			if err != nil {
				return nil, err
			}
			if verdict == TestFail {
				cfg = candidate
				n = 2
				reduced = true
				break
			}
		}
		if !reduced {
			if n >= len(cfg) {
				return cfg, nil
			}
			n *= 2
		}
	}
	return cfg, nil
}

// reduceToOneMinimal is the guarantee phase: it repeatedly removes any
// single item (plus its dependents) whose removal keeps the failure, until
// no such item remains.
func reduceToOneMinimal(cfg []string, deps *Deps, oracle Oracle) ([]string, error) {
	for {
		removed := false
		for _, item := range append([]string{}, cfg...) {
			candidate := deps.RemovalClosure(cfg, []string{item})
			if len(candidate) == len(cfg) {
				continue
			}
			verdict, err := oracle.Test(candidate)
			if err != nil {
				return nil, err
			}
			if verdict == TestFail {
				cfg = candidate
				removed = true
				break
			}
		}
		if !removed {
			return cfg, nil
		}
	}
}

// partition splits cfg into n contiguous, near-equal, non-empty chunks.
func partition(cfg []string, n int) [][]string {
	if n < 1 {
		n = 1
	}
	if n > len(cfg) {
		n = len(cfg)
	}
	chunks := make([][]string, 0, n)
	base, extra := len(cfg)/n, len(cfg)%n
	start := 0
	for i := 0; i < n; i++ {
		size := base
		if i < extra {
			size++
		}
		chunks = append(chunks, cfg[start:start+size])
		start += size
	}
	return chunks
}
