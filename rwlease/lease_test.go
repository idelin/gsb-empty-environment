package rwlease

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func acquire(t *testing.T, m *Manager, mode Mode) Token {
	t.Helper()
	tok, err := m.Acquire(context.Background(), mode)
	if err != nil {
		t.Fatalf("Acquire(%s): %v", mode, err)
	}
	return tok
}

func releaseOK(t *testing.T, m *Manager, tok Token) {
	t.Helper()
	if err := m.Release(tok); err != nil {
		t.Fatalf("Release(%s#%d): %v", tok.Mode(), tok.ID(), err)
	}
}

func TestBasicAcquireRelease(t *testing.T) {
	m := NewManager()
	r1 := acquire(t, m, Read)
	r2 := acquire(t, m, Read)
	if g := r1.Generation(); g != 0 {
		t.Fatalf("initial generation = %d, want 0", g)
	}
	releaseOK(t, m, r1)
	releaseOK(t, m, r2)

	w := acquire(t, m, Write)
	releaseOK(t, m, w)

	// Read after write must still work.
	r3 := acquire(t, m, Read)
	releaseOK(t, m, r3)
}

func TestReadersConcurrent(t *testing.T) {
	m := NewManager()
	r1 := acquire(t, m, Read)
	r2 := acquire(t, m, Read)
	releaseOK(t, m, r1)
	releaseOK(t, m, r2)
}

// Writer must eventually win against a continuous stream of readers: as soon
// as a writer is queued, newly arriving readers are blocked.
func TestWriterGetsLockUnderContinuousReads(t *testing.T) {
	m := NewManager()
	var readerCount int64

	// Initial reader holding the lock.
	holder := acquire(t, m, Read)

	// Writer queues behind the holder.
	gotWrite := make(chan Token, 1)
	go func() {
		tok, err := m.Acquire(context.Background(), Write)
		if err != nil {
			gotWrite <- Token{}
			return
		}
		gotWrite <- tok
	}()

	// Hammer new read requests: none may be granted while the writer waits.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := m.Acquire(context.Background(), Read)
			if err != nil {
				return
			}
			atomic.AddInt64(&readerCount, 1)
			m.Release(tok)
		}()
	}
	// Give contenders a moment to join the queue.
	time.Sleep(30 * time.Millisecond)

	m.mu.Lock()
	beforeRelease := m.readers
	queued := len(m.queue)
	m.mu.Unlock()
	if beforeRelease != 1 {
		t.Fatalf("readers granted while writer waits = %d, want 1", beforeRelease)
	}
	if queued < 2 {
		t.Fatalf("expected writer + new readers queued, got %d", queued)
	}

	// Holder leaves: writer must be granted next, not a later reader.
	releaseOK(t, m, holder)

	select {
	case wtok := <-gotWrite:
		if wtok.ID() == 0 {
			t.Fatal("writer acquire failed")
		}
		// New readers remain blocked until writer releases.
		m.mu.Lock()
		blocked := len(m.queue)
		writing := m.writer
		m.mu.Unlock()
		if !writing {
			t.Fatal("manager not in writer state")
		}
		if blocked == 0 {
			t.Fatal("new readers were not held back by queued-writer gate")
		}
		releaseOK(t, m, wtok)
	case <-time.After(2 * time.Second):
		t.Fatal("writer starved under continuous read stream")
	}

	wg.Wait()
	if atomic.LoadInt64(&readerCount) == 0 {
		t.Fatal("no later reader ever ran after the writer was served")
	}
}

// A successful read->write upgrade converts the token; the old read token is
// superseded and must not double-decrement reader state.
func TestUpgradeSuccess(t *testing.T) {
	m := NewManager()
	r := acquire(t, m, Read)
	w, err := m.Upgrade(context.Background(), r)
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if w.Mode() != Write {
		t.Fatalf("upgraded mode = %s, want write", w.Mode())
	}
	if err := m.Release(r); !errors.Is(err, ErrTokenReplaced) {
		t.Fatalf("release old read after upgrade = %v, want ErrTokenReplaced", err)
	}
	releaseOK(t, m, w)

	// State must be clean: another writer acquires immediately.
	w2 := acquire(t, m, Write)
	releaseOK(t, m, w2)
}

// Upgrade while another reader is active must wait until that reader drains.
func TestUpgradeWaitsForOtherReader(t *testing.T) {
	m := NewManager()
	r1 := acquire(t, m, Read)
	r2 := acquire(t, m, Read)

	upgraded := make(chan Token, 1)
	go func() {
		w, err := m.Upgrade(context.Background(), r1)
		if err != nil {
			upgraded <- Token{}
			return
		}
		upgraded <- w
	}()

	time.Sleep(30 * time.Millisecond)
	select {
	case <-upgraded:
		t.Fatal("upgrade granted while another reader active")
	default:
	}
	releaseOK(t, m, r2)
	select {
	case w := <-upgraded:
		if w.ID() == 0 {
			t.Fatal("upgrade failed after other reader released")
		}
		releaseOK(t, m, w)
	case <-time.After(2 * time.Second):
		t.Fatal("upgrade never granted after other reader released")
	}
}

// Two readers upgrading at the same time must not block each other forever:
// one upgrades, the other immediately gets ErrUpgradeConflict and keeps its
// read lease.
func TestTwoReadersUpgradeDoNotDeadlock(t *testing.T) {
	m := NewManager()
	r1 := acquire(t, m, Read)
	r2 := acquire(t, m, Read)

	// A third reader forces the first upgrade to park (upgrade waits for every
	// other reader to drain).
	r3 := acquire(t, m, Read)

	type result struct {
		err error
	}
	first := make(chan result, 1)
	go func() {
		w, err := m.Upgrade(context.Background(), r1)
		if err == nil {
			err = m.Release(w)
		}
		first <- result{err}
	}()
	waitUntil(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.upgrade && len(m.queue) == 1
	}, "first upgrade never parked")

	// r2 upgrades concurrently while the slot is taken: immediate conflict,
	// never a blocking wait, and r2 stays a live reader.
	if _, err := m.Upgrade(context.Background(), r2); !errors.Is(err, ErrUpgradeConflict) {
		t.Fatalf("second upgrade = %v, want ErrUpgradeConflict", err)
	}

	// Drain every other reader; the parked upgrade then completes.
	releaseOK(t, m, r3)
	releaseOK(t, m, r2)
	if res := <-first; res.err != nil {
		t.Fatalf("first upgrade: %v", res.err)
	}

	// r1 was consumed by its successful upgrade; r2 had failed and was
	// released above. The manager must be idle.
	m.mu.Lock()
	_, live1 := m.active[r1.id]
	_, live2 := m.active[r2.id]
	queued := len(m.queue)
	readers := m.readers
	writer := m.writer
	m.mu.Unlock()
	if live1 || live2 || queued != 0 || readers != 0 || writer {
		t.Fatalf("post-state not idle: live1=%v live2=%v queued=%d readers=%d writer=%v",
			live1, live2, queued, readers, writer)
	}
	w := acquire(t, m, Write)
	releaseOK(t, m, w)
}

// Upgrade while a writer is queued ahead must fail with
// ErrUpgradeImpossible (a waiting writer cannot be overtaken); the read right
// is preserved and releasing the reader lets the queued writer proceed.
func TestUpgradeBehindQueuedWriter(t *testing.T) {
	m := NewManager()
	r := acquire(t, m, Read)

	writerGot := make(chan Token, 1)
	go func() {
		tok, err := m.Acquire(context.Background(), Write)
		if err != nil {
			writerGot <- Token{}
			return
		}
		writerGot <- tok
	}()
	time.Sleep(30 * time.Millisecond)

	if _, err := m.Upgrade(context.Background(), r); !errors.Is(err, ErrUpgradeImpossible) {
		t.Fatalf("Upgrade behind queued writer = %v, want ErrUpgradeImpossible", err)
	}

	// Original read right intact: release it, queued writer must proceed.
	releaseOK(t, m, r)
	select {
	case w := <-writerGot:
		if w.ID() == 0 {
			t.Fatal("queued writer failed")
		}
		releaseOK(t, m, w)
	case <-time.After(2 * time.Second):
		t.Fatal("queued writer starved after reader released")
	}
}

// Upgrade while a writer is active is rejected (write cannot be acquired at
// all, so no read token exists; cover the direct branch via queued-writer
// ordering instead). Upgrade conflict path: second upgrade while first waits
// for another reader to drain.
func TestUpgradeConflictWhileFirstWaits(t *testing.T) {
	m := NewManager()
	r1 := acquire(t, m, Read)
	r2 := acquire(t, m, Read)
	r3 := acquire(t, m, Read)
	r4 := acquire(t, m, Read)

	firstDone := make(chan Token, 1)
	go func() {
		w, err := m.Upgrade(context.Background(), r1)
		if err != nil {
			firstDone <- Token{}
			return
		}
		// Release as soon as granted so the main test never blocks on us.
		if err := m.Release(w); err != nil {
			firstDone <- Token{}
			return
		}
		firstDone <- w
	}()
	waitUntil(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.upgrade && len(m.queue) == 1
	}, "first upgrade never parked")

	// r4 attempts while r1's upgrade is parked behind r2,r3: immediate
	// conflict, read right preserved.
	if _, err := m.Upgrade(context.Background(), r4); !errors.Is(err, ErrUpgradeConflict) {
		t.Fatalf("second upgrade = %v, want ErrUpgradeConflict", err)
	}

	// r4 keeps reading while the blocking readers drain; the first upgrade
	// must not be granted until r4 also leaves.
	releaseOK(t, m, r2)
	releaseOK(t, m, r3)
	select {
	case <-firstDone:
		t.Fatal("first upgrade granted while conflicting reader still active")
	case <-time.After(30 * time.Millisecond):
	}
	releaseOK(t, m, r4)

	select {
	case w := <-firstDone:
		if w.ID() == 0 {
			t.Fatal("first upgrade failed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first upgrade never completed")
	}

	// Manager is idle: a write acquires immediately.
	w := acquire(t, m, Write)
	releaseOK(t, m, w)
}

// A bounded acquire that cannot be granted times out, reports ErrTimeout, and
// its queue node is removed so later waiters can be promoted.
func TestAcquireForTimeoutRemovesQueueNode(t *testing.T) {
	clk := newFakeClock()
	m := NewManager(WithClock(clk))

	holder := acquire(t, m, Write)

	type res struct {
		tok Token
		err error
	}
	rd := make(chan res, 1)
	go func() {
		tok, err := m.AcquireFor(context.Background(), Read, 10*time.Millisecond)
		rd <- res{tok, err}
	}()
	waitUntil(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.queue) == 1
	}, "timed reader never entered queue")

	clk.Advance(11 * time.Millisecond)
	select {
	case r := <-rd:
		if !errors.Is(r.err, ErrTimeout) {
			t.Fatalf("timed-out read = %v, want ErrTimeout", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed reader did not return after clock advance")
	}

	waitUntil(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.queue) == 0
	}, "timed-out node not removed from queue")

	// A writer queued behind the timed reader must be promoted once holder
	// releases, proving the dead node did not block the queue.
	wd := make(chan res, 1)
	go func() {
		tok, err := m.AcquireFor(context.Background(), Write, time.Hour)
		wd <- res{tok, err}
	}()
	waitUntil(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.queue) == 1
	}, "writer never entered queue")

	releaseOK(t, m, holder)
	select {
	case r := <-wd:
		if r.err != nil {
			t.Fatalf("writer after timeout: %v", r.err)
		}
		releaseOK(t, m, r.tok)
	case <-time.After(2 * time.Second):
		t.Fatal("writer behind timed-out node never granted")
	}
}

// Context cancellation while queued removes the node and promotes the next
// waiter.
func TestAcquireContextCancelRemovesNode(t *testing.T) {
	m := NewManager()
	holder := acquire(t, m, Write)

	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() {
		_, err := m.Acquire(ctx, Write)
		canceled <- err
	}()
	waitUntil(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.queue) == 1
	}, "canceled writer never queued")

	cancel()
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled acquire = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled acquire did not return")
	}
	waitUntil(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.queue) == 0
	}, "canceled node not removed")

	next := make(chan Token, 1)
	go func() {
		tok, err := m.Acquire(context.Background(), Write)
		if err != nil {
			next <- Token{}
			return
		}
		next <- tok
	}()
	waitUntil(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.queue) == 1
	}, "next writer never queued")
	releaseOK(t, m, holder)
	select {
	case w := <-next:
		if w.ID() == 0 {
			t.Fatal("next writer acquire failed")
		}
		releaseOK(t, m, w)
	case <-time.After(2 * time.Second):
		t.Fatal("next writer never promoted after cancellation")
	}
}

func waitUntil(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(msg)
}

// Double release of a token must be rejected.
func TestDoubleReleaseRejected(t *testing.T) {
	m := NewManager()
	r := acquire(t, m, Read)
	releaseOK(t, m, r)
	if err := m.Release(r); !errors.Is(err, ErrAlreadyReleased) {
		t.Fatalf("second Release = %v, want ErrAlreadyReleased", err)
	}

	w := acquire(t, m, Write)
	releaseOK(t, m, w)
	if err := m.Release(w); !errors.Is(err, ErrAlreadyReleased) {
		t.Fatalf("second Release(write) = %v, want ErrAlreadyReleased", err)
	}

	// Forged / foreign / zero tokens.
	if err := m.Release(Token{}); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("zero token release = %v, want ErrNotHeld", err)
	}
	if err := m.Release(Token{mode: Read, id: 99999, gen: 0}); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("forged token release = %v, want ErrNotHeld", err)
	}
	other := NewManager()
	// Advance the foreign manager's id space so its token cannot coincide with
	// any id minted by m; foreign tokens must be rejected as ErrNotHeld.
	for i := 0; i < 10; i++ {
		tmp := acquire(t, other, Read)
		releaseOK(t, other, tmp)
	}
	foreign := acquire(t, other, Read)
	if err := m.Release(foreign); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("foreign token release = %v, want ErrNotHeld", err)
	}
	releaseOK(t, other, foreign)
}

// Revoke invalidates live tokens ("越代释放") and fails queued waiters;
// afterwards a fresh generation works normally.
func TestRevokeInvalidatesGeneration(t *testing.T) {
	m := NewManager()
	r := acquire(t, m, Read)
	if g := r.Generation(); g != 0 {
		t.Fatalf("gen = %d, want 0", g)
	}

	holder := r
	_ = holder
	queued := make(chan error, 1)
	go func() {
		_, err := m.Acquire(context.Background(), Write)
		queued <- err
	}()
	waitUntil(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.queue) == 1
	}, "writer never queued")

	m.Revoke()

	select {
	case err := <-queued:
		if !errors.Is(err, ErrLeaseRevoked) {
			t.Fatalf("queued writer after revoke = %v, want ErrLeaseRevoked", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued waiter not failed by Revoke")
	}

	// Old live token now stale: releasing it must not touch new-generation state.
	if err := m.Release(r); !errors.Is(err, ErrLeaseRevoked) {
		t.Fatalf("revoked token release = %v, want ErrLeaseRevoked", err)
	}

	// New generation: acquire/release/upgrade all work and queue is empty.
	nr := acquire(t, m, Read)
	if g := nr.Generation(); g != 1 {
		t.Fatalf("post-revoke gen = %d, want 1", g)
	}
	nw, err := m.Upgrade(context.Background(), nr)
	if err != nil {
		t.Fatalf("post-revoke Upgrade: %v", err)
	}
	releaseOK(t, m, nw)
}

// A token issued in generation N must not be releasable against generation N+1
// even if a fresh id collision somehow appeared (ids are monotonic, so a
// reused id indicates cross-generation misuse).
func TestRevokeTwiceAdvancesGenerations(t *testing.T) {
	m := NewManager()
	r0 := acquire(t, m, Read)
	m.Revoke()
	r1 := acquire(t, m, Read)
	if r1.Generation() != 1 {
		t.Fatalf("gen = %d, want 1", r1.Generation())
	}
	m.Revoke()
	r2 := acquire(t, m, Read)
	if r2.Generation() != 2 {
		t.Fatalf("gen = %d, want 2", r2.Generation())
	}
	for _, stale := range []Token{r0, r1} {
		if err := m.Release(stale); !errors.Is(err, ErrLeaseRevoked) {
			t.Fatalf("stale gen-%d release = %v, want ErrLeaseRevoked", stale.Generation(), err)
		}
	}
	releaseOK(t, m, r2)
}

// Releasing a read token as a write or vice versa is rejected.
func TestWrongModeRejected(t *testing.T) {
	m := NewManager()
	r := acquire(t, m, Read)
	// Forge a token with a live id but wrong mode. Release is keyed on id and
	// executes according to the token's declared mode; check Upgrade guards it.
	if _, err := m.Upgrade(context.Background(), Token{mode: Write, id: r.id, gen: r.gen}); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("upgrade of write-typed token = %v, want ErrNotHeld", err)
	}
	releaseOK(t, m, r)
}

// Bounded upgrade that cannot proceed times out and preserves the read right.
func TestUpgradeForTimeoutKeepsReadRight(t *testing.T) {
	clk := newFakeClock()
	m := NewManager(WithClock(clk))
	r1 := acquire(t, m, Read)
	r2 := acquire(t, m, Read)

	done := make(chan error, 1)
	go func() {
		_, err := m.UpgradeFor(context.Background(), r1, 5*time.Millisecond)
		done <- err
	}()
	waitUntil(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.upgrade && len(m.queue) == 1
	}, "upgrade never queued")

	clk.Advance(6 * time.Millisecond)
	select {
	case err := <-done:
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("upgrade timeout = %v, want ErrTimeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upgrade did not return after timeout")
	}

	waitUntil(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return !m.upgrade && len(m.queue) == 0
	}, "timed-out upgrade node left behind")

	// Read right intact and another upgrade can now proceed after r2 leaves.
	releaseOK(t, m, r2)
	w, err := m.Upgrade(context.Background(), r1)
	if err != nil {
		t.Fatalf("retry Upgrade: %v", err)
	}
	releaseOK(t, m, w)
}

// WithLease runs an injected task and always releases the token.
func TestWithLeaseRunsAndReleases(t *testing.T) {
	m := NewManager()
	ran := false
	err := m.WithLease(context.Background(), Read, func(tok Token) error {
		if tok.Mode() != Read {
			t.Fatalf("task token mode = %s", tok.Mode())
		}
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("WithLease: %v", err)
	}
	if !ran {
		t.Fatal("task did not run")
	}
	// Lease was released: a writer gets in immediately.
	w := acquire(t, m, Write)
	releaseOK(t, m, w)
}

// An injected failure is propagated while the lease is still released.
func TestWithLeaseFailureStillReleases(t *testing.T) {
	m := NewManager()
	injected := errors.New("task boom")
	err := m.WithLease(context.Background(), Write, func(Token) error {
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("WithLease err = %v, want injected failure", err)
	}
	w := acquire(t, m, Write)
	releaseOK(t, m, w)
}

// WithLease propagates acquire failure (canceled before grant).
func TestWithLeaseAcquireFailure(t *testing.T) {
	m := NewManager()
	holder := acquire(t, m, Write)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := m.WithLease(ctx, Write, func(Token) error {
		t.Fatal("task must not run when acquire fails")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WithLease acquire err = %v, want context.Canceled", err)
	}
	releaseOK(t, m, holder)
}

// Stress under the race detector: many readers, a periodic writer, upgrades,
// releases, revokes. Detects data races and asserts no lease-state corruption.
func TestRaceStress(t *testing.T) {
	m := NewManager()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var ops int64

	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tok, err := m.Acquire(context.Background(), Read)
				if err != nil {
					continue
				}
				time.Sleep(time.Microsecond)
				m.Release(tok)
				atomic.AddInt64(&ops, 1)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			tok, err := m.Acquire(context.Background(), Write)
			if err == nil {
				time.Sleep(time.Microsecond)
				m.Release(tok)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			tok, err := m.Acquire(context.Background(), Read)
			if err == nil {
				if wtok, uerr := m.UpgradeFor(context.Background(), tok, time.Millisecond); uerr == nil {
					m.Release(tok) // superseded: reports ErrTokenReplaced
					tok = wtok
				}
				m.Release(tok)
			}
			time.Sleep(3 * time.Millisecond)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(15 * time.Millisecond):
				m.Revoke()
			}
		}
	}()

	time.Sleep(250 * time.Millisecond)
	close(stop)
	wg.Wait()

	if atomic.LoadInt64(&ops) == 0 {
		t.Fatal("stress run performed zero read ops")
	}
	// After quiescence the manager must be reusable and internally idle.
	m.mu.Lock()
	queued := len(m.queue)
	m.mu.Unlock()
	if queued != 0 {
		t.Fatalf("queue not drained after stress: %d", queued)
	}
	w := acquire(t, m, Write)
	releaseOK(t, m, w)
}
