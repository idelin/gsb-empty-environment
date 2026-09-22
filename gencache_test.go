package gencache_test

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"gencache"
)

func TestPublishAndGet(t *testing.T) {
	c := gencache.NewCoordinator()
	gen := c.Start()

	out := c.Execute(gen, "answer", func() (interface{}, gencache.Cleanup, error) {
		return 42, nil, nil
	})
	if out.Status != gencache.StatusPublished || out.Value != 42 {
		t.Fatalf("unexpected outcome: %+v", out)
	}
	v, ok := c.Result(gen, "answer")
	if !ok || v != 42 {
		t.Fatalf("got (%v, %v), want (42, true)", v, ok)
	}
	if c.Current() != 0 {
		t.Fatalf("current generation changed: %d", c.Current())
	}
}

func TestFailureNotCached(t *testing.T) {
	c := gencache.NewCoordinator()
	gen := c.Start()
	wantErr := errors.New("boom")
	cleanupCalled := false

	out := c.Execute(gen, "k", func() (interface{}, gencache.Cleanup, error) {
		return nil, func() { cleanupCalled = true }, wantErr
	})
	if out.Status != gencache.StatusFailed || out.Err != wantErr {
		t.Fatalf("unexpected outcome: %+v", out)
	}
	if _, ok := c.Result(gen, "k"); ok {
		t.Fatal("failed task was cached")
	}
	if cleanupCalled {
		t.Fatal("cleanup must not run for a failed task")
	}
}

func TestOldGenerationRejectedRunsCleanup(t *testing.T) {
	c := gencache.NewCoordinator()
	old := c.Start()

	releaseCompute := make(chan struct{})
	cleanupDone := make(chan struct{})
	cleanupRan := make(chan struct{})
	started := make(chan struct{})

	go func() {
		_ = c.Execute(old, "k", func() (interface{}, gencache.Cleanup, error) {
			close(started)
			<-releaseCompute
			return "old-value", func() {
				close(cleanupRan)
				<-cleanupDone
			}, nil
		})
	}()

	<-started
	newGen := c.Switch()
	if newGen != 1 || c.Current() != 1 {
		t.Fatalf("unexpected current generation: %d", c.Current())
	}

	// The old task finishes computing concurrently with the already
	// completed switch; its publish must be rejected.
	close(releaseCompute)
	select {
	case <-cleanupRan:
	case <-time.After(5 * time.Second):
		t.Fatal("rejected task cleanup did not run")
	}

	if _, ok := c.Result(old, "k"); ok {
		t.Fatal("rejected result landed in old generation cache")
	}
	if _, ok := c.Result(newGen, "k"); ok {
		t.Fatal("rejected result leaked into new generation cache")
	}

	// New-generation task starts and publishes even while the rejected
	// task is still inside its cleanup.
	out := c.Execute(c.Start(), "k2", func() (interface{}, gencache.Cleanup, error) {
		return "new-value", nil, nil
	})
	if out.Status != gencache.StatusPublished {
		t.Fatalf("new task blocked by old cleanup: %+v", out)
	}

	close(cleanupDone)
	if err := c.WaitInflight(old); err != nil {
		t.Fatal(err)
	}
}

func TestPublishedBeforeSwitchLandsInOldGeneration(t *testing.T) {
	c := gencache.NewCoordinator()
	old := c.Start()

	cleanupCalled := false
	out := c.Execute(old, "k", func() (interface{}, gencache.Cleanup, error) {
		return "v", func() { cleanupCalled = true }, nil
	})
	if out.Status != gencache.StatusPublished {
		t.Fatalf("unexpected: %+v", out)
	}
	newGen := c.Switch()

	if v, ok := c.Result(old, "k"); !ok || v != "v" {
		t.Fatalf("old generation missing pre-switch result: (%v,%v)", v, ok)
	}
	if _, ok := c.Result(newGen, "k"); ok {
		t.Fatal("pre-switch result leaked into new generation")
	}
	if cleanupCalled {
		t.Fatal("cleanup must not run for a published result")
	}
}

// TestPublishSwitchRace repeatedly forces "old task finishes computing
// exactly when the switch happens". Every iteration the result must be
// either fully published in the old generation or fully rejected: it can
// never appear in the new generation's cache.
func TestPublishSwitchRace(t *testing.T) {
	const iterations = 400
	var published, rejected int

	for i := 0; i < iterations; i++ {
		c := gencache.NewCoordinator()
		old := c.Start()

		computed := make(chan struct{})
		goSignal := make(chan struct{})
		outcomes := make(chan gencache.Outcome, 1)
		switched := make(chan int, 1)

		go func() {
			outcomes <- c.Execute(old, "k", func() (interface{}, gencache.Cleanup, error) {
				// Announce that the (lock-free) computation is done, then
				// wait until the switch goroutine is also ready, so both
				// the publish and the switch contend at the same instant.
				close(computed)
				<-goSignal
				return "old-value-" + strconv.Itoa(i), nil, nil
			})
		}()

		<-computed
		go func() {
			<-goSignal
			switched <- c.Switch()
		}()
		close(goSignal)

		out := <-outcomes
		newGen := <-switched

		switch out.Status {
		case gencache.StatusPublished:
			published++
			if v, ok := c.Result(old, "k"); !ok || v != "old-value-"+strconv.Itoa(i) {
				t.Fatalf("iteration %d: published value missing from old gen", i)
			}
			if _, ok := c.Result(newGen, "k"); ok {
				t.Fatalf("iteration %d: old result leaked into new generation", i)
			}
		case gencache.StatusRejected:
			rejected++
			if _, ok := c.Result(old, "k"); ok {
				t.Fatalf("iteration %d: rejected result cached in old generation", i)
			}
			if _, ok := c.Result(newGen, "k"); ok {
				t.Fatalf("iteration %d: rejected result cached in new generation", i)
			}
		default:
			t.Fatalf("iteration %d: unexpected status %s", i, out.Status)
		}
	}

	// Both linearization orders must have been exercised.
	if published == 0 || rejected == 0 {
		t.Fatalf("expected both orderings, got published=%d rejected=%d", published, rejected)
	}
	t.Logf("linearization samples: published=%d rejected=%d", published, rejected)
}

// TestMultipleOldTasksAllRejected covers several in-flight old-generation
// tasks rejected by one switch, each running its own cleanup exactly once.
func TestMultipleOldTasksAllRejected(t *testing.T) {
	c := gencache.NewCoordinator()
	const n = 16
	var cleanupCount sync.WaitGroup
	cleanupCount.Add(n)
	release := make(chan struct{})
	var startWg sync.WaitGroup
	startWg.Add(n)

	for i := 0; i < n; i++ {
		gen := c.Start()
		if gen != 0 {
			t.Fatalf("task %d captured generation %d before switch", i, gen)
		}
		go func(i int) {
			out := c.Execute(0, "k"+strconv.Itoa(i), func() (interface{}, gencache.Cleanup, error) {
				startWg.Done()
				<-release
				return i, cleanupCount.Done, nil
			})
			if out.Status != gencache.StatusRejected {
				t.Errorf("task %d: got %s, want rejected", i, out.Status)
			}
		}(i)
	}

	startWg.Wait()
	newGen := c.Switch()
	close(release)

	done := make(chan struct{})
	go func() { cleanupCount.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("not every rejected task ran cleanup")
	}
	for i := 0; i < n; i++ {
		if _, ok := c.Result(newGen, "k"+strconv.Itoa(i)); ok {
			t.Fatalf("old task %d leaked into new generation", i)
		}
	}
}

// ExampleCoordinator demonstrates the lifecycle: publish in generation 0,
// switch, observe rejection with cleanup, and a fresh publish afterwards.
func ExampleCoordinator() {
	c := gencache.NewCoordinator()

	gen := c.Start()
	out := c.Execute(gen, "greeting", func() (interface{}, gencache.Cleanup, error) {
		return "hello", nil, nil
	})
	fmt.Println(out.Status)

	release := make(chan struct{})
	staleOut := make(chan gencache.Outcome, 1)
	go func() {
		staleOut <- c.Execute(gen, "late", func() (interface{}, gencache.Cleanup, error) {
			<-release
			return "stale", func() {}, nil
		})
	}()

	newGen := c.Switch()
	close(release)
	fmt.Println((<-staleOut).Status)
	_ = c.WaitInflight(gen)

	out = c.Execute(newGen, "greeting", func() (interface{}, gencache.Cleanup, error) {
		return "hola", nil, nil
	})
	fmt.Println(out.Status)

	// Output:
	// published
	// rejected
	// published
}
