package doublebuf

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// marker builds version-tagged content so tests can assert exactly which
// buffer a reader observes.
func marker(v int) []byte {
	return []byte(fmt.Sprintf("v%d-content", v))
}

func mustAcquire(t *testing.T, s *Store, wantVersion uint64, wantContent string) *View {
	v := s.Acquire()
	if got := v.Version(); got != wantVersion {
		t.Fatalf("Acquire: version = %d, want %d", got, wantVersion)
	}
	if got := string(v.Bytes()); got != wantContent {
		t.Fatalf("Acquire: content = %q, want %q", got, wantContent)
	}
	return v
}

func mustCommit(t *testing.T, s *Store, content []byte, wantVersion uint64) {
	u, err := s.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := u.Write(content); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := u.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got != wantVersion {
		t.Fatalf("Commit: version = %d, want %d", got, wantVersion)
	}
}

func TestCommitSwitchesActiveBuffer(t *testing.T) {
	s := New(marker(1))

	old := mustAcquire(t, s, 1, "v1-content")
	mustCommit(t, s, marker(2), 2)

	// New readers observe the switched buffer.
	cur := mustAcquire(t, s, 2, "v2-content")

	// The reader that pinned the old buffer still sees version 1.
	if got := old.Version(); got != 1 {
		t.Fatalf("old view version = %d, want 1", got)
	}
	if got := string(old.Bytes()); got != "v1-content" {
		t.Fatalf("old view content = %q, want %q", got, "v1-content")
	}

	old.Release()
	cur.Release()
}

func TestAbortKeepsOldVersion(t *testing.T) {
	s := New(marker(1))

	u, err := s.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := u.Write(marker(2)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := u.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	// The failed write never became visible.
	v := mustAcquire(t, s, 1, "v1-content")
	v.Release()
	if got := s.Version(); got != 1 {
		t.Fatalf("Store version = %d, want 1", got)
	}

	// The standby slot is free again after the abort.
	mustCommit(t, s, marker(2), 2)
	v = mustAcquire(t, s, 2, "v2-content")
	v.Release()
}

func TestThirdUpdateWaitsForReaderRelease(t *testing.T) {
	s := New(marker(1))

	// Reader pins v1; a commit retires v1 into the standby slot.
	pin := mustAcquire(t, s, 1, "v1-content")
	mustCommit(t, s, marker(2), 2)

	// The standby slot (old v1 buffer) is still pinned, so the next
	// Begin must wait.
	type result struct {
		u   *Update
		err error
	}
	done := make(chan result, 1)
	go func() {
		u, err := s.Begin(context.Background())
		done <- result{u, err}
	}()

	select {
	case r := <-done:
		r.u.Abort()
		t.Fatal("Begin returned while the standby buffer was still pinned")
	case <-time.After(100 * time.Millisecond):
	}

	// Releasing the last reader of the retired buffer unblocks Begin.
	pin.Release()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Begin: %v", r.err)
		}
		if err := r.u.Write(marker(3)); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if got, err := r.u.Commit(); err != nil || got != 3 {
			t.Fatalf("Commit = (%d, %v), want (3, nil)", got, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Begin still blocked after the reader released the buffer")
	}

	v := mustAcquire(t, s, 3, "v3-content")
	v.Release()
}

func TestBeginCancelWhileWaiting(t *testing.T) {
	s := New(marker(1))

	pin := mustAcquire(t, s, 1, "v1-content")
	mustCommit(t, s, marker(2), 2)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		u, err := s.Begin(ctx)
		if err == nil {
			u.Abort()
		}
		done <- err
	}()

	// Give the goroutine a chance to block, then cancel the wait.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Begin error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Begin did not return after cancellation")
	}

	// A canceled wait leaves no state behind: once the reader releases,
	// a fresh Begin succeeds immediately.
	pin.Release()
	u, err := s.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin after cancel: %v", err)
	}
	u.Abort()
}

func TestBeginWithCanceledContext(t *testing.T) {
	s := New(marker(1))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Begin(ctx); err != context.Canceled {
		t.Fatalf("Begin error = %v, want context.Canceled", err)
	}
}

func TestRepeatedCommits(t *testing.T) {
	s := New(marker(1))

	for i := 2; i <= 6; i++ {
		mustCommit(t, s, marker(i), uint64(i))
		v := mustAcquire(t, s, uint64(i), fmt.Sprintf("v%d-content", i))
		v.Release()
	}
	if got := s.Version(); got != 6 {
		t.Fatalf("Store version = %d, want 6", got)
	}

	// Committing the same update twice fails.
	u, err := s.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	u.Write(marker(7))
	if _, err := u.Commit(); err != nil {
		t.Fatalf("first Commit: %v", err)
	}
	if _, err := u.Commit(); err != ErrUpdateClosed {
		t.Fatalf("second Commit error = %v, want ErrUpdateClosed", err)
	}
	if err := u.Abort(); err != ErrUpdateClosed {
		t.Fatalf("Abort after Commit error = %v, want ErrUpdateClosed", err)
	}

	// Committing after an abort fails, and the active version is intact.
	u, err = s.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	u.Write(marker(8))
	if err := u.Abort(); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if _, err := u.Commit(); err != ErrUpdateClosed {
		t.Fatalf("Commit after Abort error = %v, want ErrUpdateClosed", err)
	}
	v := mustAcquire(t, s, 7, "v7-content")
	v.Release()
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	s := New(marker(1))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				u, err := s.Begin(ctx)
				if err != nil {
					return
				}
				u.Write(marker(i))
				u.Commit()
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				v := s.Acquire()
				_ = v.Version()
				_ = v.Bytes()
				v.Release()
			}
		}()
	}
	wg.Wait()

	// Every commit advanced the version monotonically.
	if got := s.Version(); got < 2 {
		t.Fatalf("Store version = %d, want >= 2", got)
	}
}
