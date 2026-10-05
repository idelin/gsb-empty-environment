package pausecoord

import (
	"context"
	"sync"
	"testing"
	"time"
)

func pauseAsync(c *Coordinator) (chan uint64, chan error) {
	epochCh := make(chan uint64, 1)
	errCh := make(chan error, 1)
	go func() {
		epoch, err := c.Pause(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		epochCh <- epoch
	}()
	return epochCh, errCh
}

// waitPaused blocks until the pause initiated by pauseAsync is in effect, so
// that subsequent SafePoint calls cannot slip in before the epoch starts.
func waitPaused(t *testing.T, c *Coordinator) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !c.Paused() {
		if time.Now().After(deadline) {
			t.Fatal("pause did not start")
		}
		time.Sleep(time.Millisecond)
	}
}

func mustNotPause(t *testing.T, epochCh chan uint64, errCh chan error, d time.Duration) {
	t.Helper()
	select {
	case e := <-epochCh:
		t.Fatalf("pause completed unexpectedly with epoch %d", e)
	case err := <-errCh:
		t.Fatalf("pause failed unexpectedly: %v", err)
	case <-time.After(d):
	}
}

func mustPause(t *testing.T, epochCh chan uint64, errCh chan error) uint64 {
	t.Helper()
	select {
	case epoch := <-epochCh:
		return epoch
	case err := <-errCh:
		t.Fatalf("pause failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("pause did not complete")
	}
	return 0
}

func TestPauseWaitsForAllWorkers(t *testing.T) {
	c := New()
	ids := []uint64{c.Join(), c.Join(), c.Join()}

	epochCh, errCh := pauseAsync(c)
	waitPaused(t, c)

	// Workers arrive at their safe points one by one; the pause must not
	// complete until the last one confirms.
	released := make([]chan struct{}, len(ids))
	for i, id := range ids {
		released[i] = make(chan struct{})
		go func(id uint64, done chan<- struct{}) {
			c.SafePoint(id)
			close(done)
		}(id, released[i])
		time.Sleep(20 * time.Millisecond)
		if i < len(ids)-1 {
			mustNotPause(t, epochCh, errCh, 50*time.Millisecond)
		}
	}

	if epoch := mustPause(t, epochCh, errCh); epoch != 1 {
		t.Fatalf("got epoch %d, want 1", epoch)
	}

	// Workers stay blocked at their safe points until Resume.
	for _, done := range released {
		select {
		case <-done:
			t.Fatal("worker released before Resume")
		default:
		}
	}
	c.Resume()
	for _, done := range released {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("worker still blocked after Resume")
		}
	}
}

func TestWorkerMayLeaveDuringPause(t *testing.T) {
	c := New()
	stayer := c.Join()
	leaver := c.Join()

	epochCh, errCh := pauseAsync(c)
	waitPaused(t, c)

	// The leaver exits without confirming; that must count as confirmation.
	c.Leave(leaver)
	mustNotPause(t, epochCh, errCh, 50*time.Millisecond)

	go c.SafePoint(stayer)
	mustPause(t, epochCh, errCh)
	c.Resume()
}

func TestJoinBlocksDuringPause(t *testing.T) {
	c := New()
	id := c.Join()

	epochCh, errCh := pauseAsync(c)
	waitPaused(t, c)
	go c.SafePoint(id)
	mustPause(t, epochCh, errCh)

	joined := make(chan uint64, 1)
	go func() { joined <- c.Join() }()
	select {
	case <-joined:
		t.Fatal("Join returned while paused")
	case <-time.After(50 * time.Millisecond):
	}

	c.Resume()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("Join still blocked after Resume")
	}
}

func TestTwoConsecutivePauses(t *testing.T) {
	c := New()
	id1 := c.Join()
	id2 := c.Join()

	// First pause.
	epochCh, errCh := pauseAsync(c)
	waitPaused(t, c)
	go c.SafePoint(id1)
	go c.SafePoint(id2)
	epoch1 := mustPause(t, epochCh, errCh)
	c.Resume()
	time.Sleep(20 * time.Millisecond) // let workers leave SafePoint

	// Second pause must be a new epoch and require fresh confirmations:
	// the first pause's confirmations must not carry over.
	epochCh2, errCh2 := pauseAsync(c)
	waitPaused(t, c)
	mustNotPause(t, epochCh2, errCh2, 50*time.Millisecond)

	go c.SafePoint(id1)
	mustNotPause(t, epochCh2, errCh2, 50*time.Millisecond)

	go c.SafePoint(id2)
	if epoch2 := mustPause(t, epochCh2, errCh2); epoch2 != epoch1+1 {
		t.Fatalf("second pause epoch = %d, want %d", epoch2, epoch1+1)
	}
	c.Resume()
}

func TestPauseTimeoutAbandons(t *testing.T) {
	c := New()
	id := c.Join() // this worker never reaches a safe point

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	epoch, err := c.Pause(ctx)
	if err != context.DeadlineExceeded {
		t.Fatalf("pause error = %v, want %v", err, context.DeadlineExceeded)
	}
	if c.Paused() {
		t.Fatal("coordinator still paused after timeout abandonment")
	}

	// After abandonment, workers and new joiners must proceed freely.
	done := make(chan struct{})
	go func() { c.SafePoint(id); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SafePoint blocked after pause was abandoned")
	}
	joined := make(chan uint64, 1)
	go func() { joined <- c.Join() }()
	var newID uint64
	select {
	case newID = <-joined:
	case <-time.After(time.Second):
		t.Fatal("Join blocked after pause was abandoned")
	}

	// A later pause must still work with a fresh epoch.
	epochCh, errCh := pauseAsync(c)
	waitPaused(t, c)
	go c.SafePoint(id)
	go c.SafePoint(newID)
	if e := mustPause(t, epochCh, errCh); e != epoch+1 {
		t.Fatalf("post-abort pause epoch = %d, want %d", e, epoch+1)
	}
	c.Resume()
}

func TestStaleConfirmationNotCounted(t *testing.T) {
	c := New()
	id1 := c.Join()
	id2 := c.Join()

	// First pause: both workers confirm asynchronously.
	epochCh, errCh := pauseAsync(c)
	waitPaused(t, c)
	c.Confirm(id1, 1)
	c.Confirm(id2, 1)
	epoch1 := mustPause(t, epochCh, errCh)
	c.Resume()

	// Second pause. A stale confirmation replayed from epoch 1 must not
	// count, and neither must a duplicate of id1's current-epoch confirm.
	epochCh2, errCh2 := pauseAsync(c)
	waitPaused(t, c)

	c.Confirm(id1, epoch1)             // stale epoch: must be ignored
	c.Confirm(uint64(9999), c.Epoch()) // unknown worker: must be ignored
	mustNotPause(t, epochCh2, errCh2, 50*time.Millisecond)

	c.Confirm(id1, c.Epoch()) // genuine confirm for id1
	c.Confirm(id1, c.Epoch()) // duplicate: must not stand in for id2
	mustNotPause(t, epochCh2, errCh2, 50*time.Millisecond)

	c.Confirm(id2, c.Epoch())
	mustPause(t, epochCh2, errCh2)
	c.Resume()
}

func TestConcurrentSafePointsAndLeaves(t *testing.T) {
	c := New()
	var ids []uint64
	for i := 0; i < 8; i++ {
		ids = append(ids, c.Join())
	}

	epochCh, errCh := pauseAsync(c)
	waitPaused(t, c)

	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id uint64) {
			defer wg.Done()
			if i%2 == 0 {
				c.SafePoint(id)
			} else {
				c.Leave(id)
			}
		}(i, id)
	}
	mustPause(t, epochCh, errCh)
	c.Resume()
	wg.Wait()
}
