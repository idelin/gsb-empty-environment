package dagsched

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// virtualExecutor is a scripted, instrumented executor used to verify the
// scheduler from the outside: it tracks resource usage of concurrently
// running tasks, records start/cleanup order, and flags any grant that
// would exceed capacity.
type virtualExecutor struct {
	mu         sync.Mutex
	capacity   ResourceMap
	inUse      ResourceMap
	maxInUse   ResourceMap
	starts     []string
	ends       []string
	cleanups   []string
	violations []string
}

func newVirtualExecutor(capacity ResourceMap) *virtualExecutor {
	return &virtualExecutor{
		capacity: capacity,
		inUse:    ResourceMap{},
		maxInUse: ResourceMap{},
	}
}

// task builds a Task whose Run is wrapped with resource accounting.
// The script defines the task's behavior (block on gates, fail, etc.).
func (e *virtualExecutor) task(id string, deps []string, needs ResourceMap, script func(ctx context.Context) error) Task {
	return Task{
		ID:    id,
		Deps:  deps,
		Needs: needs,
		Run: func(ctx context.Context) error {
			e.acquire(id, needs)
			defer e.release(id, needs)
			return script(ctx)
		},
	}
}

func (e *virtualExecutor) acquire(id string, needs ResourceMap) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.starts = append(e.starts, id)
	for name, qty := range needs {
		e.inUse[name] += qty
		if e.inUse[name] > e.capacity[name] {
			e.violations = append(e.violations, fmt.Sprintf(
				"resource %q over capacity: in use %d > %d (task %q)",
				name, e.inUse[name], e.capacity[name], id))
		}
		if e.inUse[name] > e.maxInUse[name] {
			e.maxInUse[name] = e.inUse[name]
		}
	}
}

func (e *virtualExecutor) release(id string, needs ResourceMap) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for name, qty := range needs {
		e.inUse[name] -= qty
		if e.inUse[name] < 0 {
			e.violations = append(e.violations, fmt.Sprintf(
				"resource %q released below zero (task %q)", name, id))
		}
	}
	e.ends = append(e.ends, id)
}

func (e *virtualExecutor) recordCleanup(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cleanups = append(e.cleanups, id)
}

func (e *virtualExecutor) started(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.starts {
		if s == id {
			return true
		}
	}
	return false
}

func (e *virtualExecutor) startedCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.starts)
}

func (e *virtualExecutor) startsSnapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.starts...)
}

func (e *virtualExecutor) inUseOf(name string) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inUse[name]
}

// checkConservation asserts no capacity violation ever happened and all
// grants were returned.
func (e *virtualExecutor) checkConservation(t *testing.T) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, v := range e.violations {
		t.Errorf("resource conservation violated: %s", v)
	}
	for name, qty := range e.inUse {
		if qty != 0 {
			t.Errorf("resource %q leaked: %d still in use after run", name, qty)
		}
	}
}

// gate is a one-shot barrier a scripted task waits on.
type gate chan struct{}

func newGate() gate { return make(gate) }

func (g gate) open() { close(g) }

func (g gate) wait(ctx context.Context) error {
	select {
	case <-g:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitFor polls cond until it holds or the test times out.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// runAsync starts Run in the background.
func runAsync(s *Scheduler, ctx context.Context) (<-chan map[string]Result, <-chan error) {
	resCh := make(chan map[string]Result, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := s.Run(ctx)
		resCh <- res
		errCh <- err
	}()
	return resCh, errCh
}

func awaitRun(t *testing.T, resCh <-chan map[string]Result, errCh <-chan error) (map[string]Result, error) {
	t.Helper()
	select {
	case res := <-resCh:
		return res, <-errCh
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler Run did not return")
		return nil, nil
	}
}
