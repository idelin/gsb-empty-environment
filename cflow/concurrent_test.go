package cflow_test

import (
	"sync"
	"testing"

	"example.org/netsec/uid1234/cflow"
)

// Many goroutines drive independent thread IDs through the same runtime.
// Every goroutine must observe a perfectly balanced shadow stack, proving
// the internal mutex serializes transitions without cross-thread leakage.
func TestConcurrentThreadsAreIsolated(t *testing.T) {
	rt := newFixture(t)

	const goroutines = 32
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		threadID := g + 1
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if v := rt.Process(
					entry(threadID, "main", 0),
					establish(threadID, 1),
					entry(threadID, "worker", 1),
					indirect(threadID, "worker", "handlerA", 2),
					entry(threadID, "handlerA", 2),
					unwind(threadID, 1),
					ret(threadID, "main"),
				); v != nil {
					t.Errorf("thread %d iteration %d: %s", threadID, i, v.Detail)
					return
				}
				if depth := rt.StackDepth(threadID); depth != 0 {
					t.Errorf("thread %d depth = %d, want 0", threadID, depth)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// A forged unwind racing a legitimate owner must never succeed on the
// forger's thread: recovery points never cross thread maps.
func TestConcurrentCrossThreadForgery(t *testing.T) {
	rt := newFixture(t)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if v := rt.Process(
				entry(1, "main", 0),
				establish(1, 5),
				entry(1, "worker", 1),
				unwind(1, 5),
				ret(1, "main"),
			); v != nil {
				t.Errorf("owner thread: %s", v.Detail)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			assertNoViolation(t, rt.Step(entry(2, "main", 0)))
			v := rt.Step(unwind(2, 5))
			if v == nil || v.Reason != cflow.ReasonRecoveryNotFound {
				t.Errorf("forged unwind reason = %v, want %q", v, cflow.ReasonRecoveryNotFound)
				return
			}
			assertNoViolation(t, rt.Step(ret(2, "main")))
		}
	}()
	wg.Wait()
}
