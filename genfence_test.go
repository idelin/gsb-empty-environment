package genfence

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestPublishBeforeSwitchIsKept: a result published before the switch
// stays in its own (sealed) generation's cache and is not copied into
// the new generation.
func TestPublishBeforeSwitchIsKept(t *testing.T) {
	m := NewManager()
	done := make(chan Outcome, 1)
	m.Start(Task{
		Key:     "k",
		Compute: func() (any, error) { return "v", nil },
		Cleanup: func(o Outcome) { done <- o },
	})
	o := <-done // cleanup runs after the publish attempt
	if !o.Published || o.Rejected {
		t.Fatalf("expected published outcome, got %+v", o)
	}
	m.Switch()

	oldCache, ok := m.Snapshot(0)
	if !ok || oldCache["k"] != "v" {
		t.Fatalf("published result missing from sealed generation 0: %v", oldCache)
	}
	newCache, _ := m.Snapshot(1)
	if _, found := newCache["k"]; found {
		t.Fatalf("old result leaked into new generation cache: %v", newCache)
	}
}

// TestPublishRacingSwitchIsRejected: the task has finished computing
// and is about to publish when the switch lands first; the result
// must be rejected and written nowhere.
func TestPublishRacingSwitchIsRejected(t *testing.T) {
	m := NewManager()
	computed := make(chan struct{})
	proceed := make(chan struct{})
	done := make(chan Outcome, 1)
	m.Start(Task{
		Key: "k",
		Compute: func() (any, error) {
			close(computed) // compute finished, publish imminent
			<-proceed       // test holds the task here
			return "v", nil
		},
		Cleanup: func(o Outcome) { done <- o },
	})
	<-computed
	m.Switch()     // switch wins the race
	close(proceed) // now let the stale task try to publish
	o := <-done

	if o.Published || !o.Rejected {
		t.Fatalf("expected rejected outcome, got %+v", o)
	}
	oldCache, _ := m.Snapshot(0)
	if _, found := oldCache["k"]; found {
		t.Fatalf("rejected result written to old generation cache: %v", oldCache)
	}
	newCache, _ := m.Snapshot(1)
	if _, found := newCache["k"]; found {
		t.Fatalf("rejected result written to new generation cache: %v", newCache)
	}
}

// TestLinearizabilityInvariant hammers publish vs. switch: every task
// outcome is Published XOR Rejected, a published result appears in
// exactly its own generation's cache, and no result ever appears in a
// newer generation's cache.
func TestLinearizabilityInvariant(t *testing.T) {
	const tasks = 300
	m := NewManager()
	outcomes := make(chan Outcome, tasks)

	var starters sync.WaitGroup
	for i := 0; i < tasks; i++ {
		i := i
		// Interleave switches with task starts.
		if i%7 == 3 {
			m.Switch()
		}
		starters.Add(1)
		go func() {
			defer starters.Done()
			m.Start(Task{
				Key:     fmt.Sprintf("k%d", i),
				Compute: func() (any, error) { return i, nil },
				Cleanup: func(o Outcome) { outcomes <- o },
			})
		}()
	}
	starters.Wait()
	for gen := uint64(0); gen <= m.Generation(); gen++ {
		m.WaitGeneration(gen)
	}
	close(outcomes)

	caches := map[uint64]map[string]any{}
	for gen := uint64(0); gen <= m.Generation(); gen++ {
		c, ok := m.Snapshot(gen)
		if !ok {
			t.Fatalf("generation %d vanished", gen)
		}
		caches[gen] = c
	}

	count := 0
	for o := range outcomes {
		count++
		if o.Err != nil {
			t.Fatalf("unexpected compute error: %+v", o)
		}
		if o.Published == o.Rejected {
			t.Fatalf("outcome must be exactly one of published/rejected: %+v", o)
		}
		for gen, c := range caches {
			v, found := c[o.Key]
			if !found {
				continue
			}
			if !o.Published {
				t.Fatalf("rejected result %q found in generation %d cache", o.Key, gen)
			}
			if gen != o.Gen {
				t.Fatalf("result %q of generation %d found in generation %d cache", o.Key, o.Gen, gen)
			}
			if v != o.Value {
				t.Fatalf("cache value mismatch for %q: got %v want %v", o.Key, v, o.Value)
			}
		}
	}
	if count != tasks {
		t.Fatalf("got %d outcomes, want %d", count, tasks)
	}
}

// TestNewTasksDoNotWaitForOldCleanup: a new-generation task runs to
// completion while an old task is still blocked inside its Cleanup.
func TestNewTasksDoNotWaitForOldCleanup(t *testing.T) {
	m := NewManager()
	inCleanup := make(chan struct{})
	releaseCleanup := make(chan struct{})
	m.Start(Task{
		Key:     "old",
		Compute: func() (any, error) { return "old-v", nil },
		Cleanup: func(Outcome) {
			close(inCleanup)
			<-releaseCleanup // old cleanup blocked
		},
	})
	<-inCleanup
	m.Switch()

	newDone := make(chan Outcome, 1)
	m.Start(Task{
		Key:     "new",
		Compute: func() (any, error) { return "new-v", nil },
		Cleanup: func(o Outcome) { newDone <- o },
	})
	select {
	case o := <-newDone:
		if !o.Published {
			t.Fatalf("new task should have published, got %+v", o)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("new task blocked behind old generation cleanup")
	}

	close(releaseCleanup)
	m.WaitGeneration(0)
}

// TestComputeFailureSkipsPublish: an injected compute failure skips
// publishing; Cleanup still runs and observes the error.
func TestComputeFailureSkipsPublish(t *testing.T) {
	m := NewManager()
	want := errors.New("injected failure")
	done := make(chan Outcome, 1)
	m.Start(Task{
		Key:     "k",
		Compute: func() (any, error) { return nil, want },
		Cleanup: func(o Outcome) { done <- o },
	})
	o := <-done
	if !errors.Is(o.Err, want) {
		t.Fatalf("cleanup saw err=%v, want %v", o.Err, want)
	}
	if o.Published || o.Rejected {
		t.Fatalf("failed task must neither publish nor be rejected: %+v", o)
	}
	cache, _ := m.Snapshot(0)
	if len(cache) != 0 {
		t.Fatalf("failed task left entries in cache: %v", cache)
	}
}

// TestWaitGenerationBlocksUntilCleanup: WaitGeneration returns only
// after the old generation's cleanup has fully completed.
func TestWaitGenerationBlocksUntilCleanup(t *testing.T) {
	m := NewManager()
	inCleanup := make(chan struct{})
	finishCleanup := make(chan struct{})
	m.Start(Task{
		Key:     "k",
		Compute: func() (any, error) { return "v", nil },
		Cleanup: func(Outcome) {
			close(inCleanup)
			<-finishCleanup
		},
	})
	<-inCleanup
	m.Switch()

	waited := make(chan struct{})
	go func() {
		m.WaitGeneration(0)
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("WaitGeneration returned before cleanup finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(finishCleanup)
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitGeneration stuck after cleanup finished")
	}
}

// TestCleanupRunsExactlyOnce per task, across mixed outcomes.
func TestCleanupRunsExactlyOnce(t *testing.T) {
	m := NewManager()
	var mu sync.Mutex
	calls := map[string]int{}
	record := func(o Outcome) {
		mu.Lock()
		calls[o.Key]++
		mu.Unlock()
	}

	// One published, one rejected, one failed.
	m.Start(Task{Key: "published", Compute: func() (any, error) { return 1, nil }, Cleanup: record})
	blocked := make(chan struct{})
	m.Start(Task{
		Key:     "rejected",
		Compute: func() (any, error) { <-blocked; return 2, nil },
		Cleanup: record,
	})
	m.Start(Task{Key: "failed", Compute: func() (any, error) { return nil, errors.New("x") }, Cleanup: record})

	// Let "published" and "failed" finish, then switch and release "rejected".
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := calls["published"] + calls["failed"]
		mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tasks did not finish in time")
		}
		time.Sleep(time.Millisecond)
	}
	m.Switch()
	close(blocked)
	m.WaitGeneration(0)

	mu.Lock()
	defer mu.Unlock()
	for _, key := range []string{"published", "rejected", "failed"} {
		if calls[key] != 1 {
			t.Fatalf("cleanup for %q ran %d times, want 1", key, calls[key])
		}
	}
}
