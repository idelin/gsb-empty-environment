package secretlease

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var secretBytes = []byte("TOP-SECRET-SIGNING-KEY-0123456789")

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newTestHost(t *testing.T, chunkSize, capacity int) (*Host, *MemPool) {
	t.Helper()
	pool := NewMemPool(chunkSize, capacity)
	return NewHost(pool), pool
}

// Authorization is a closure over the specific step ID: derived steps and
// unrelated steps are rejected even though they share a host.
func TestAuthorizationClosure(t *testing.T) {
	host, pool := newTestHost(t, 64, 8)
	lease, err := host.NewLease("signing-key", secretBytes)
	if err != nil {
		t.Fatalf("NewLease: %v", err)
	}
	defer lease.Release()

	root := host.NewRootStep("root")
	lease.Grant(root.ID())

	var saw []byte
	err = root.Run(func(ctx StepContext) error {
		if err := lease.Use(ctx, func(s []byte) error {
			saw = append(saw[:0], s...)
			return nil
		}); err != nil {
			return fmt.Errorf("root Use: %w", err)
		}

		derived := root.SubStep("parallel-child")
		sibling := host.NewRootStep("other-root")

		errDerived := derived.Run(func(childCtx StepContext) error {
			return lease.Use(childCtx, func([]byte) error { return nil })
		})
		if !errors.Is(errDerived, ErrNotAuthorized) {
			return fmt.Errorf("derived step: got %v, want ErrNotAuthorized", errDerived)
		}

		errSibling := sibling.Run(func(otherCtx StepContext) error {
			return lease.Use(otherCtx, func([]byte) error { return nil })
		})
		if !errors.Is(errSibling, ErrNotAuthorized) {
			return fmt.Errorf("sibling step: got %v, want ErrNotAuthorized", errSibling)
		}

		grantedChild := root.SubStep("granted-child")
		lease.Grant(grantedChild.ID())
		errGranted := grantedChild.Run(func(childCtx StepContext) error {
			return lease.Use(childCtx, func(s []byte) error {
				if string(s) != string(secretBytes) {
					return errors.New("derived step saw wrong secret")
				}
				return nil
			})
		})
		if errGranted != nil {
			return fmt.Errorf("explicitly granted derived step: %w", errGranted)
		}

		lease.Revoke(grantedChild.ID())
		if lease.Authorized(grantedChild.ID()) || !lease.Authorized(root.ID()) {
			return errors.New("revoke/grant bookkeeping wrong")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(saw) != string(secretBytes) {
		t.Fatalf("callback saw %q", saw)
	}
	if !pool.Contains(secretBytes) {
		t.Fatal("secret should remain while lease is held")
	}
}

// Derived buffers are tracked per step and zeroed as soon as the step ends.
func TestStepZeroesDerivedBuffers(t *testing.T) {
	host, pool := newTestHost(t, 64, 4)

	var derived *Buffer
	err := host.NewRootStep("build").Run(func(ctx StepContext) error {
		buf, err := ctx.Alloc(32)
		if err != nil {
			return err
		}
		copy(buf.Bytes(), []byte("DERIVED-SIGNATURE-PAYLOAD-0001"))
		derived = buf
		if !buf.Alive() {
			return errors.New("buffer should be alive inside step")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if derived.Alive() {
		t.Fatal("derived buffer must be destroyed when step finishes")
	}
	if pool.Contains([]byte("DERIVED-SIGNATURE-PAYLOAD-0001")) {
		t.Fatal("derived bytes remain in pool arena after step cleanup")
	}
	if pool.Outstanding() != 0 || !pool.FreeChunksZeroed() {
		t.Fatalf("pool not fully reclaimed: outstanding=%d", pool.Outstanding())
	}
}

// Concurrent sub-steps each hold derived buffers behind a pause gate. Parent
// cleanup must wait until all of them finish, then zero every buffer.
func TestConcurrentZeroingWithPausableSubsteps(t *testing.T) {
	host, pool := newTestHost(t, 64, 8)
	lease, err := host.NewLease("key", secretBytes)
	if err != nil {
		t.Fatalf("NewLease: %v", err)
	}

	const workers = 4
	derivedMarker := []byte("DERIVED-WORKER-MARKER-000000000001")
	gates := make([]*Gate, workers)
	subs := make([]*Step, workers)

	root := host.NewRootStep("root")

	var finished atomic.Int64
	runErr := make(chan error, 1)

	go func() {
		runErr <- root.Run(func(ctx StepContext) error {
			for i := 0; i < workers; i++ {
				gates[i] = NewGate()
				gates[i].Pause()
				subs[i] = root.SubStep(fmt.Sprintf("worker-%d", i), WithGate(gates[i]))
				lease.Grant(subs[i].ID())

				root.Go(subs[i], func(childCtx StepContext) {
					buf, allocErr := childCtx.Alloc(32)
					if allocErr != nil {
						t.Errorf("worker alloc: %v", allocErr)
						return
					}
					copy(buf.Bytes(), derivedMarker)
					if cpErr := childCtx.Checkpoint(); cpErr != nil {
						t.Errorf("worker checkpoint: %v", cpErr)
						return
					}
					if useErr := lease.Use(childCtx, func(s []byte) error {
						if string(s) != string(secretBytes) {
							return errors.New("bad secret in worker")
						}
						return nil
					}); useErr != nil {
						t.Errorf("worker Use: %v", useErr)
						return
					}
					finished.Add(1)
				})
			}

			for _, g := range gates {
				waitFor(t, 2*time.Second, func() bool { return g.Blocked() == 1 }, "worker pause")
			}
			if pool.Outstanding() < workers {
				return fmt.Errorf("expected %d live derived buffers, got %d", workers, pool.Outstanding())
			}
			for _, g := range gates {
				g.Resume()
			}
			return nil
		})
	}()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("root Run did not return after resuming sub-steps")
	}

	if finished.Load() != workers {
		t.Fatalf("only %d/%d workers finished", finished.Load(), workers)
	}
	if !root.Finished() {
		t.Fatal("root step must be marked finished after Run")
	}
	if pool.Outstanding() != 1 {
		t.Fatalf("only the lease chunk should remain, got outstanding=%d", pool.Outstanding())
	}
	if pool.Contains(derivedMarker) {
		t.Fatal("secret-derived bytes remain in pool after concurrent cleanup")
	}

	lease.Release()
	if pool.Outstanding() != 0 {
		t.Fatalf("%d chunks still outstanding after release", pool.Outstanding())
	}
	if pool.Contains(secretBytes) {
		t.Fatal("lease release must zero the secret chunk")
	}
	select {
	case <-lease.Done():
	default:
		t.Fatal("lease Done must close after release")
	}
}

// Releasing a lease waits for in-flight callbacks and then rejects all new use.
func TestReleaseWaitsForInFlight(t *testing.T) {
	host, pool := newTestHost(t, 64, 4)
	lease, err := host.NewLease("key", secretBytes)
	if err != nil {
		t.Fatalf("NewLease: %v", err)
	}
	step := host.NewRootStep("step")
	lease.Grant(step.ID())

	inside := make(chan struct{})
	release := make(chan struct{})

	go func() {
		_ = step.Run(func(ctx StepContext) error {
			return lease.Use(ctx, func([]byte) error {
				close(inside)
				<-release
				return nil
			})
		})
	}()

	<-inside
	released := make(chan struct{})
	go func() {
		lease.Release()
		close(released)
	}()

	select {
	case <-released:
		t.Fatal("Release returned while a callback was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	waitFor(t, time.Second, func() bool {
		select {
		case <-released:
			return true
		default:
			return false
		}
	}, "lease release")

	if pool.Contains(secretBytes) {
		t.Fatal("secret bytes remain after release")
	}
	err = step.Run(func(ctx StepContext) error {
		return lease.Use(ctx, func([]byte) error { return nil })
	})
	if !errors.Is(err, ErrLeaseReleased) {
		t.Fatalf("post-release Use: got %v, want ErrLeaseReleased", err)
	}

	lease.Release()
}

// A panicking step still performs full cleanup and re-panics after zeroing.
func TestPanicReclaimsBuffers(t *testing.T) {
	host, pool := newTestHost(t, 64, 4)
	var buf *Buffer

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic to propagate")
			}
		}()
		_ = host.NewRootStep("boom").Run(func(ctx StepContext) error {
			b, err := ctx.Alloc(32)
			if err != nil {
				return err
			}
			copy(b.Bytes(), []byte("PANIC-DERIVED-BYTES-000000000001"))
			buf = b
			panic("kaboom")
		})
	}()

	if buf.Alive() {
		t.Fatal("buffer must be destroyed after panic")
	}
	if pool.Contains([]byte("PANIC-DERIVED-BYTES")) {
		t.Fatal("panic path left secret-derived bytes in pool")
	}
	if pool.Outstanding() != 0 {
		t.Fatalf("outstanding=%d after panic cleanup", pool.Outstanding())
	}
}

// A panicking lease callback is converted to an error and the lease survives.
func TestLeaseCallbackPanic(t *testing.T) {
	host, pool := newTestHost(t, 64, 4)
	lease, err := host.NewLease("key", secretBytes)
	if err != nil {
		t.Fatalf("NewLease: %v", err)
	}
	defer lease.Release()
	step := host.NewRootStep("step")
	lease.Grant(step.ID())

	err = step.Run(func(ctx StepContext) error {
		return lease.Use(ctx, func([]byte) error { panic("callback boom") })
	})
	if err == nil || !strings.Contains(err.Error(), "callback panicked") {
		t.Fatalf("got %v, want recovered panic error", err)
	}
	if pool.Outstanding() != 1 {
		t.Fatalf("lease chunk should remain held, outstanding=%d", pool.Outstanding())
	}
}

// Cancellation unblocks a paused sub-step, and the parent still waits for
// cleanup confirmation before returning.
func TestCancellationUnblocksAndCleansUp(t *testing.T) {
	host, pool := newTestHost(t, 64, 8)
	baseCtx, cancel := context.WithCancel(context.Background())

	root := host.NewRootStep("root", WithContext(baseCtx))
	gate := NewGate()
	gate.Pause()
	child := root.SubStep("paused", WithGate(gate))

	var buf *Buffer
	var childErr error
	var wg sync.WaitGroup
	wg.Add(1)

	rootDone := make(chan struct{})
	go func() {
		_ = root.Run(func(ctx StepContext) error {
			root.Go(child, func(childCtx StepContext) {
				defer wg.Done()
				b, err := childCtx.Alloc(32)
				if err != nil {
					childErr = err
					return
				}
				copy(b.Bytes(), []byte("CANCELED-DERIVED-BYTES-000000001"))
				buf = b
				if err := childCtx.Checkpoint(); !errors.Is(err, context.Canceled) {
					childErr = fmt.Errorf("checkpoint: got %v, want context.Canceled", err)
				}
			})
			return nil
		})
		close(rootDone)
	}()

	waitFor(t, 2*time.Second, func() bool { return gate.Blocked() == 1 }, "child pause")
	cancel()
	wg.Wait()

	if childErr != nil {
		t.Fatal(childErr)
	}
	<-rootDone
	if !root.Finished() {
		t.Fatal("root must finish cleanup after cancellation")
	}
	if buf.Alive() || pool.Contains([]byte("CANCELED-DERIVED-BYTES")) || pool.Outstanding() != 0 {
		t.Fatalf("cancellation left residue: alive=%v outstanding=%d", buf.Alive(), pool.Outstanding())
	}
}

// Exhausted allocations must not leak chunks, and freed chunks return to the
// pool after failed steps.
func TestPoolExhaustionReclaim(t *testing.T) {
	host, pool := newTestHost(t, 64, 2)

	err := host.NewRootStep("greedy").Run(func(ctx StepContext) error {
		first, err := ctx.Alloc(64)
		if err != nil {
			return fmt.Errorf("first alloc: %w", err)
		}
		copy(first.Bytes(), []byte("EXHAUSTION-MARKER-00000000000000001"))
		if _, err := ctx.Alloc(64); err != nil {
			return fmt.Errorf("second alloc: %w", err)
		}
		if _, err := ctx.Alloc(64); !errors.Is(err, ErrPoolExhausted) {
			return fmt.Errorf("third alloc: got %v, want ErrPoolExhausted", err)
		}
		return errors.New("step fails after exhaustion")
	})
	if err == nil {
		t.Fatal("expected step error to propagate")
	}

	if pool.Outstanding() != 0 || !pool.FreeChunksZeroed() {
		t.Fatalf("failed step leaked chunks: outstanding=%d", pool.Outstanding())
	}
	if pool.Contains([]byte("EXHAUSTION-MARKER")) {
		t.Fatal("exhaustion marker survived step cleanup")
	}

	// Reclaimed chunks serve new allocations.
	roundTrip := host.NewRootStep("reuse")
	if err := roundTrip.Run(func(ctx StepContext) error {
		buf, err := ctx.Alloc(64)
		if err != nil {
			return err
		}
		if !buf.Alive() {
			return errors.New("reused buffer not alive")
		}
		return nil
	}); err != nil {
		t.Fatalf("reuse step: %v", err)
	}
	if pool.Outstanding() != 0 {
		t.Fatalf("outstanding=%d after reuse cleanup", pool.Outstanding())
	}
}
