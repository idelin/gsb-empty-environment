package dbuf

import (
	"context"
	"testing"
	"time"
)

func mustBegin(t *testing.T, s *Store) *Update {
	t.Helper()
	u, err := s.BeginUpdate(context.Background())
	if err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}
	return u
}

func mustView(t *testing.T, s *Store, version uint64, data string) *View {
	t.Helper()
	v := s.Acquire()
	if v.Version() != version || string(v.Data()) != data {
		t.Fatalf("view = (v%d, %q), want (v%d, %q)", v.Version(), v.Data(), version, data)
	}
	return v
}

// blocked asserts that nothing is received from ch within a short window.
func blocked(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s: expected to block, but proceeded", what)
	case <-time.After(50 * time.Millisecond):
	}
}

func unblocked(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: expected to proceed, but blocked", what)
	}
}

// beginAsync starts BeginUpdate in a goroutine and reports the outcome.
func beginAsync(s *Store, ctx context.Context) (<-chan struct{}, <-chan error) {
	done := make(chan struct{})
	errc := make(chan error, 1)
	go func() {
		_, err := s.BeginUpdate(ctx)
		if err == nil {
			close(done)
		}
		errc <- err
	}()
	return done, errc
}

// TestCommitSwitchesActiveBuffer verifies that committing swaps the prepared
// buffer into the active slot while pre-commit readers keep the old version.
func TestCommitSwitchesActiveBuffer(t *testing.T) {
	s := New([]byte("v1"))

	old := mustView(t, s, 1, "v1")
	defer old.Release()

	u := mustBegin(t, s)
	if !u.Commit([]byte("v2")) {
		t.Fatal("Commit returned false")
	}

	// New readers see the new version on the other buffer.
	mustView(t, s, 2, "v2").Release()
	// The reader acquired before the commit still sees the old version.
	if old.Version() != 1 || string(old.Data()) != "v1" {
		t.Fatalf("stale view = (v%d, %q), want (v1, %q)", old.Version(), old.Data(), "v1")
	}
}

// TestAbortKeepsOldVersion verifies that a failed write (Abort) leaves the
// active version untouched and frees the spare buffer for the next attempt.
func TestAbortKeepsOldVersion(t *testing.T) {
	s := New([]byte("v1"))

	u := mustBegin(t, s)
	u.Abort()

	mustView(t, s, 1, "v1").Release()

	// The spare buffer is immediately reusable after the abort.
	u2 := mustBegin(t, s)
	if !u2.Commit([]byte("v2")) {
		t.Fatal("Commit after Abort returned false")
	}
	mustView(t, s, 2, "v2").Release()
}

// TestRetiredBufferNotReusedWhileRead verifies that with both buffers busy
// (active + retired-but-still-read), a new update waits until the reader of
// the retired buffer releases it.
func TestRetiredBufferNotReusedWhileRead(t *testing.T) {
	s := New([]byte("v1"))

	reader := mustView(t, s, 1, "v1")

	u := mustBegin(t, s)
	if !u.Commit([]byte("v2")) {
		t.Fatal("Commit returned false")
	}
	// Now: v2 active, v1 retired but still held by reader. No spare buffer.
	done, errc := beginAsync(s, context.Background())
	blocked(t, done, "BeginUpdate while retired buffer is read")

	reader.Release()
	unblocked(t, done, "BeginUpdate after reader release")
	if err := <-errc; err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}
}

// TestThirdUpdateWaits verifies that with at most two buffers, a second
// concurrent update (the "third" buffer request) must wait: it stays blocked
// while one buffer is active and the other is checked out for writing, even
// after that write commits, until the retired buffer's readers drain.
func TestThirdUpdateWaits(t *testing.T) {
	s := New([]byte("v1"))

	reader := mustView(t, s, 1, "v1")
	u1 := mustBegin(t, s) // both buffers now busy: active + pending

	done, errc := beginAsync(s, context.Background())
	blocked(t, done, "third buffer request while update pending")

	if !u1.Commit([]byte("v2")) {
		t.Fatal("Commit returned false")
	}
	// v1 is retired but still read, so the waiter must keep waiting.
	blocked(t, done, "third buffer request while retired buffer is read")

	reader.Release()
	unblocked(t, done, "third buffer request after reader release")
	if err := <-errc; err != nil {
		t.Fatalf("BeginUpdate: %v", err)
	}
}

// TestCancelWait verifies that a waiting update can be cancelled via context
// and that the store remains fully functional afterwards.
func TestCancelWait(t *testing.T) {
	s := New([]byte("v1"))

	reader := mustView(t, s, 1, "v1")
	defer reader.Release()
	u1 := mustBegin(t, s) // both buffers busy

	ctx, cancel := context.WithCancel(context.Background())
	done, errc := beginAsync(s, ctx)
	blocked(t, done, "waiting update before cancel")

	cancel()
	select {
	case err := <-errc:
		if err != context.Canceled {
			t.Fatalf("BeginUpdate error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled BeginUpdate did not return")
	}

	// Store is unaffected: pending update still commits, old view intact.
	if !u1.Commit([]byte("v2")) {
		t.Fatal("Commit returned false")
	}
	mustView(t, s, 2, "v2").Release()
	if reader.Version() != 1 || string(reader.Data()) != "v1" {
		t.Fatalf("stale view = (v%d, %q), want (v1, %q)", reader.Version(), reader.Data(), "v1")
	}
}

// TestRepeatedCommit verifies that committing the same update twice is a
// no-op the second time, and that sequential commits advance the version.
func TestRepeatedCommit(t *testing.T) {
	s := New([]byte("v1"))

	u := mustBegin(t, s)
	if !u.Commit([]byte("v2")) {
		t.Fatal("first Commit returned false")
	}
	if u.Commit([]byte("v2-duplicate")) {
		t.Fatal("second Commit on same update returned true")
	}
	mustView(t, s, 2, "v2").Release()

	// Abort after Commit is also a no-op and does not free the active buffer.
	u.Abort()
	mustView(t, s, 2, "v2").Release()

	// Sequential commits keep advancing the version on alternating buffers.
	u2 := mustBegin(t, s)
	if !u2.Commit([]byte("v3")) {
		t.Fatal("third version Commit returned false")
	}
	mustView(t, s, 3, "v3").Release()
}

// TestReleaseIdempotent verifies that double Release does not corrupt the
// reader count or free a buffer twice.
func TestReleaseIdempotent(t *testing.T) {
	s := New([]byte("v1"))

	v := mustView(t, s, 1, "v1")
	v.Release()
	v.Release()

	u := mustBegin(t, s)
	if !u.Commit([]byte("v2")) {
		t.Fatal("Commit returned false")
	}
	mustView(t, s, 2, "v2").Release()
}
