package handlepool

import (
	"errors"
	"os"
	"sync"
	"testing"
)

// fakeHandle is a Handle double that records every close and carries the
// tenant it was created for, letting tests detect cross-tenant leakage.
type fakeHandle struct {
	mu       sync.Mutex
	id       int
	tenant   TenantID
	fd       uintptr
	closes   int
	closed   bool
	closeErr error
	// persistent, when true, makes every Close fail without flipping closed,
	// modeling a descriptor whose close(2) keeps failing.
	persistent bool
}

func (f *fakeHandle) Fd() uintptr { return f.fd }

func (f *fakeHandle) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	if f.persistent {
		return f.closeErr
	}
	if f.closed {
		return os.ErrClosed
	}
	f.closed = true
	return f.closeErr
}

func (f *fakeHandle) setPersistent(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persistent = v
}

func (f *fakeHandle) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

// recordingCloser delegates to the handle and counts calls per descriptor.
type recordingCloser struct {
	mu     sync.Mutex
	calls  map[uintptr]int
	failed map[uintptr]int
}

func newRecordingCloser() *recordingCloser {
	return &recordingCloser{calls: map[uintptr]int{}, failed: map[uintptr]int{}}
}

func (r *recordingCloser) Close(h Handle) error {
	r.mu.Lock()
	r.calls[h.Fd()]++
	r.mu.Unlock()

	err := h.Close()
	if err != nil {
		r.mu.Lock()
		r.failed[h.Fd()]++
		r.mu.Unlock()
	}
	return err
}

func (r *recordingCloser) callsFor(fd uintptr) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[fd]
}

func newTestPool(t *testing.T, capacity int) (*Pool, *recordingCloser) {
	t.Helper()
	closer := newRecordingCloser()
	return New(capacity, WithCloser(closer)), closer
}

func mustRegister(t *testing.T, p *Pool, tenant TenantID, h Handle) Token {
	t.Helper()
	tok, err := p.Register(tenant, h)
	if err != nil {
		t.Fatalf("Register(%q) unexpected error: %v", tenant, err)
	}
	return tok
}

func assertOwners(t *testing.T, p *Pool, tokens []Token, want TenantID) {
	t.Helper()
	for _, tok := range tokens {
		owner, err := p.OwnerOf(tok)
		if err != nil {
			t.Errorf("OwnerOf: %v", err)
			continue
		}
		if owner != want {
			t.Errorf("owner = %q, want %q", owner, want)
		}
	}
}

func TestRegisterReleaseClosesExactlyOnce(t *testing.T) {
	p, closer := newTestPool(t, 4)
	h := &fakeHandle{id: 1, tenant: "tenant-a", fd: 100}
	tok := mustRegister(t, p, "tenant-a", h)

	if err := p.Release(tok); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := h.closeCount(); got != 1 {
		t.Fatalf("handle closed %d times, want exactly 1", got)
	}
	if got := closer.callsFor(h.fd); got != 1 {
		t.Fatalf("closer invoked %d times, want 1", got)
	}

	// Releasing the same token again must never reach the close system call.
	if err := p.Release(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second Release err = %v, want ErrInvalidToken", err)
	}
	if got := h.closeCount(); got != 1 {
		t.Fatalf("handle closed %d times after re-release, want still 1", got)
	}

	stats := p.Stats()
	if stats.Live != 0 || stats.FreeSlots != 4 || stats.CloseSuccess != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestZeroAndForgedTokensAreRejected(t *testing.T) {
	p, _ := newTestPool(t, 2)
	h := &fakeHandle{fd: 7}
	tok := mustRegister(t, p, "tenant-a", h)

	for _, bad := range []Token{
		{},
		{slot: tok.slot, generation: 0},
		{slot: tok.slot, generation: tok.generation + 999},
		{slot: 99, generation: 1},
		{slot: -1, generation: 1},
	} {
		if err := p.Release(bad); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("Release(%+v) err = %v, want ErrInvalidToken", bad, err)
		}
	}
	if h.closeCount() != 0 {
		t.Fatalf("forged token triggered close: count=%d", h.closeCount())
	}
}

func TestSlotReuseRejectsLateOldToken(t *testing.T) {
	p, closer := newTestPool(t, 1)

	first := &fakeHandle{id: 1, tenant: "tenant-a", fd: 11}
	oldToken := mustRegister(t, p, "tenant-a", first)
	if err := p.Release(oldToken); err != nil {
		t.Fatalf("Release first: %v", err)
	}

	// The single slot is reused for a new tenant/handle.
	second := &fakeHandle{id: 2, tenant: "tenant-b", fd: 22}
	newToken, err := p.Register("tenant-b", second)
	if err != nil {
		t.Fatalf("Register into reused slot: %v", err)
	}
	if newToken.slot != oldToken.slot {
		t.Fatalf("expected slot reuse: old=%d new=%d", oldToken.slot, newToken.slot)
	}
	if newToken.generation == oldToken.generation {
		t.Fatal("reused slot must mint a new generation")
	}

	// A late arrival of the old token must not touch the new occupant.
	for _, op := range []string{"release", "owner", "borrow"} {
		var err error
		switch op {
		case "release":
			err = p.Release(oldToken)
		case "owner":
			_, err = p.OwnerOf(oldToken)
		case "borrow":
			_, err = p.Borrow("tenant-a", oldToken)
		}
		if !errors.Is(err, ErrInvalidToken) {
			t.Errorf("late token %s err = %v, want ErrInvalidToken", op, err)
		}
	}
	if second.closeCount() != 0 {
		t.Fatalf("new occupant closed by stale token: %d", second.closeCount())
	}

	owner, err := p.OwnerOf(newToken)
	if err != nil || owner != "tenant-b" {
		t.Fatalf("new occupant owner = %q, %v; want tenant-b", owner, err)
	}
	if err := p.Release(newToken); err != nil {
		t.Fatalf("Release new occupant: %v", err)
	}
	if first.closeCount() != 1 || second.closeCount() != 1 {
		t.Fatalf("close counts first=%d second=%d", first.closeCount(), second.closeCount())
	}
	if closer.callsFor(first.fd) != 1 || closer.callsFor(second.fd) != 1 {
		t.Fatal("each descriptor must be closed through the closer exactly once")
	}
}

func TestInjectedFaultKeepsHandleOpenAndOwned(t *testing.T) {
	p, closer := newTestPool(t, 2)
	h := &fakeHandle{fd: 33, tenant: "tenant-a"}
	tok := mustRegister(t, p, "tenant-a", h)

	var faultOnce sync.Once
	p.SetFault(func(Handle) error {
		var err error
		faultOnce.Do(func() { err = ErrFaultInjected })
		return err
	})

	err := p.Release(tok)
	if !errors.Is(err, ErrFaultInjected) {
		t.Fatalf("faulted Release err = %v, want ErrFaultInjected", err)
	}
	if h.closeCount() != 0 || closer.callsFor(h.fd) != 0 {
		t.Fatal("fault must prevent the close system call entirely")
	}
	if owner, _ := p.OwnerOf(tok); owner != "tenant-a" {
		t.Fatalf("owner after fault = %q, want tenant-a", owner)
	}
	if stats := p.Stats(); stats.Live != 1 || stats.FreeSlots != 1 || stats.CloseFailures != 1 {
		t.Fatalf("slot must stay occupied after fault: %+v", stats)
	}

	// Clear the fault and retry: the handle now closes exactly once.
	p.SetFault(nil)
	if err := p.Release(tok); err != nil {
		t.Fatalf("retry Release: %v", err)
	}
	if h.closeCount() != 1 {
		t.Fatalf("close count = %d, want 1", h.closeCount())
	}
}

func TestCloserErrorDoesNotFreeSlotOrCloseTwice(t *testing.T) {
	p, _ := newTestPool(t, 1)
	h := &fakeHandle{fd: 44, closeErr: errors.New("boom"), persistent: true}
	tok := mustRegister(t, p, "tenant-a", h)

	if err := p.Release(tok); err == nil || err.Error() != "boom" {
		t.Fatalf("Release err = %v, want boom", err)
	}
	if _, err := p.Register("tenant-b", &fakeHandle{fd: 45}); err == nil {
		t.Fatal("slot must not be reused while close is failing")
	}
	if err := p.Release(tok); err == nil || err.Error() != "boom" {
		t.Fatalf("retry Release err = %v, want boom", err)
	}
	if h.closeCount() != 2 {
		t.Fatalf("each failed attempt reaches close once; count=%d", h.closeCount())
	}

	// After the fault clears, the same token still releases and closes once.
	h.setPersistent(false)
	h.mu.Lock()
	h.closeErr = nil
	h.mu.Unlock()
	if err := p.Release(tok); err != nil {
		t.Fatalf("Release after fault cleared: %v", err)
	}
	if h.closeCount() != 3 {
		t.Fatalf("close count = %d, want 3 (2 failed + 1 success)", h.closeCount())
	}
	if err := p.Release(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("post-close Release err = %v, want ErrInvalidToken", err)
	}
}

func TestTransferIsAtomicAllOrNothing(t *testing.T) {
	p, _ := newTestPool(t, 8)
	tenantA, tenantB := TenantID("a"), TenantID("b")

	handles := make([]*fakeHandle, 4)
	tokens := make([]Token, 4)
	for i := range handles {
		handles[i] = &fakeHandle{id: i, fd: uintptr(50 + i), tenant: tenantA}
		tokens[i] = mustRegister(t, p, tenantA, handles[i])
	}
	other := mustRegister(t, p, tenantB, &fakeHandle{fd: 99, tenant: tenantB})

	origGen := tokens[0].generation

	t.Run("foreign owner rolls back whole batch", func(t *testing.T) {
		batch := []Token{tokens[0], tokens[1], other}
		err := p.Transfer(tenantA, tenantB, batch)
		if !errors.Is(err, ErrNotOwner) {
			t.Fatalf("Transfer err = %v, want ErrNotOwner", err)
		}
		assertOwners(t, p, tokens, tenantA)
	})

	t.Run("duplicate token rolls back", func(t *testing.T) {
		err := p.Transfer(tenantA, tenantB, []Token{tokens[0], tokens[0], tokens[1]})
		if !errors.Is(err, ErrDuplicateToken) {
			t.Fatalf("Transfer err = %v, want ErrDuplicateToken", err)
		}
		assertOwners(t, p, tokens, tenantA)
	})

	t.Run("borrowed handle rolls back", func(t *testing.T) {
		if _, err := p.Borrow(tenantA, tokens[2]); err != nil {
			t.Fatalf("Borrow: %v", err)
		}
		err := p.Transfer(tenantA, tenantB, []Token{tokens[2], tokens[3]})
		if !errors.Is(err, ErrBorrowed) {
			t.Fatalf("Transfer err = %v, want ErrBorrowed", err)
		}
		assertOwners(t, p, []Token{tokens[2], tokens[3]}, tenantA)
		if err := p.Return(tenantA, tokens[2]); err != nil {
			t.Fatalf("Return: %v", err)
		}
	})

	t.Run("stale token rolls back", func(t *testing.T) {
		stale := tokens[1]
		if err := p.Release(tokens[1]); err != nil {
			t.Fatalf("Release: %v", err)
		}
		err := p.Transfer(tenantA, tenantB, []Token{tokens[0], stale})
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("Transfer err = %v, want ErrInvalidToken", err)
		}
		if owner, _ := p.OwnerOf(tokens[0]); owner != tenantA {
			t.Fatalf("valid member changed owner during rollback: %q", owner)
		}
	})

	t.Run("valid batch transfers entirely", func(t *testing.T) {
		live := []Token{tokens[0], tokens[2], tokens[3]}
		if err := p.Transfer(tenantA, tenantB, live); err != nil {
			t.Fatalf("Transfer: %v", err)
		}
		assertOwners(t, p, live, tenantB)
		for _, idx := range []int{0, 2, 3} {
			if handles[idx].closeCount() != 0 {
				t.Fatalf("transfer must not close handles: idx %d closed=%d", idx, handles[idx].closeCount())
			}
		}
		if tokens[0].generation != origGen {
			t.Fatal("transfer must not mint new tokens")
		}
	})

	t.Run("empty batch is a no-op", func(t *testing.T) {
		if err := p.Transfer(tenantA, tenantB, nil); err != nil {
			t.Fatalf("empty Transfer: %v", err)
		}
	})
}

func TestTenantIsolationOnBorrowAndTransfer(t *testing.T) {
	p, _ := newTestPool(t, 4)
	a, b := TenantID("alice"), TenantID("bob")
	h := &fakeHandle{fd: 60, tenant: a}
	tok := mustRegister(t, p, a, h)

	if _, err := p.Borrow(b, tok); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("cross-tenant Borrow err = %v, want ErrNotOwner", err)
	}
	if err := p.Transfer(b, a, []Token{tok}); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("cross-tenant Transfer err = %v, want ErrNotOwner", err)
	}

	got, err := p.Borrow(a, tok)
	if err != nil {
		t.Fatalf("owner Borrow: %v", err)
	}
	if got != h {
		t.Fatal("Borrow must return the exact registered handle")
	}
	if _, err := p.Borrow(a, tok); !errors.Is(err, ErrBorrowed) {
		t.Fatalf("double Borrow err = %v, want ErrBorrowed", err)
	}
	if err := p.Release(tok); !errors.Is(err, ErrBorrowed) {
		t.Fatalf("Release while borrowed err = %v, want ErrBorrowed", err)
	}
	if err := p.Transfer(a, b, []Token{tok}); !errors.Is(err, ErrBorrowed) {
		t.Fatalf("Transfer while borrowed err = %v, want ErrBorrowed", err)
	}
	if err := p.Return(b, tok); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("foreign Return err = %v, want ErrNotOwner", err)
	}
	if err := p.Return(a, tok); err != nil {
		t.Fatalf("owner Return: %v", err)
	}
	if err := p.Return(a, tok); err == nil {
		t.Fatal("returning a non-borrowed handle must fail")
	}
}

func TestCapacityAndRegistrationRules(t *testing.T) {
	p, _ := newTestPool(t, 1)
	tok := mustRegister(t, p, "a", &fakeHandle{fd: 1})

	if _, err := p.Register("a", &fakeHandle{fd: 2}); err == nil {
		t.Fatal("Register beyond capacity must fail")
	}
	if _, err := p.Register("a", nil); err == nil {
		t.Fatal("Register of nil handle must fail")
	}
	if err := p.Release(tok); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// Freed slot is available again.
	if _, err := p.Register("b", &fakeHandle{fd: 3}); err != nil {
		t.Fatalf("Register after release: %v", err)
	}
}

// TestConcurrentBorrowAndReturn hammers one handle with many tenants; at most
// one borrower at a time may win, it must always see the exact registered
// handle, and a foreign tenant can never succeed. Run under -race.
func TestConcurrentBorrowAndReturn(t *testing.T) {
	p, _ := newTestPool(t, 1)
	owner := TenantID("owner")
	h := &fakeHandle{fd: 700, tenant: owner}
	tok := mustRegister(t, p, owner, h)

	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		tenant := owner
		if w%2 == 0 {
			tenant = TenantID("intruder")
		}
		wg.Add(1)
		go func(tenant TenantID) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				got, err := p.Borrow(tenant, tok)
				switch {
				case err == nil:
					if tenant != owner {
						t.Errorf("foreign tenant %s borrowed owner handle", tenant)
					}
					if got.(*fakeHandle) != h {
						t.Errorf("borrow returned wrong handle")
					}
					if err := p.Return(tenant, tok); err != nil {
						t.Errorf("Return: %v", err)
					}
				case errors.Is(err, ErrBorrowed):
					// Another goroutine currently holds it: expected.
				case errors.Is(err, ErrNotOwner):
					if tenant == owner {
						t.Errorf("owner denied borrow: %v", err)
					}
				default:
					t.Errorf("Borrow: unexpected err %v", err)
				}
			}
		}(tenant)
	}
	wg.Wait()

	if stats := p.Stats(); stats.Borrowed != 0 || stats.Live != 1 {
		t.Fatalf("handle not returned cleanly: %+v", stats)
	}
	if err := p.Release(tok); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if h.closeCount() != 1 {
		t.Fatalf("closed %d times, want 1", h.closeCount())
	}
}

// TestConcurrentAtomicTransfers runs barrier-synchronized phases. In each
// phase every worker fires one transfer concurrently: exactly one direction
// matches the current owner, the rest are rejected with ErrNotOwner, and once
// the phase quiesces the whole batch must share a single owner. The quiescent
// invariant is what proves all-or-nothing atomicity under contention.
func TestConcurrentAtomicTransfers(t *testing.T) {
	p, closer := newTestPool(t, 8)
	const (
		batchSize = 6
		workers   = 8
		phases    = 100
	)
	a, b := TenantID("a"), TenantID("b")

	tokens := make([]Token, batchSize)
	handles := make([]*fakeHandle, batchSize)
	for i := range tokens {
		handles[i] = &fakeHandle{id: i, fd: uintptr(800 + i), tenant: a}
		tokens[i] = mustRegister(t, p, a, handles[i])
	}

	var (
		wg           sync.WaitGroup
		startBarrier sync.WaitGroup
		doneBarrier  sync.WaitGroup
	)

	for phase := 0; phase < phases; phase++ {
		startBarrier.Add(1)
		doneBarrier.Add(workers)

		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				startBarrier.Wait()

				src, dst := a, b
				if w%2 == 0 {
					src, dst = b, a
				}
				err := p.Transfer(src, dst, tokens)
				if err != nil && !errors.Is(err, ErrNotOwner) {
					t.Errorf("Transfer: unexpected err %v", err)
				}
				doneBarrier.Done()
			}(w)
		}

		startBarrier.Done() // release all workers at once
		doneBarrier.Wait()  // phase quiesces before checking invariants

		want, err := p.OwnerOf(tokens[0])
		if err != nil {
			t.Fatalf("OwnerOf after phase %d: %v", phase, err)
		}
		for idx, tok := range tokens[1:] {
			owner, err := p.OwnerOf(tok)
			if err != nil {
				t.Fatalf("OwnerOf token %d: %v", idx+1, err)
			}
			if owner != want {
				t.Fatalf("phase %d: split ownership %q vs %q", phase, want, owner)
			}
		}
	}
	wg.Wait()

	for i, tok := range tokens {
		if err := p.Release(tok); err != nil {
			t.Fatalf("Release %d: %v", i, err)
		}
		if handles[i].closeCount() != 1 {
			t.Fatalf("handle %d closed %d times, want 1", i, handles[i].closeCount())
		}
		if closer.callsFor(handles[i].fd) != 1 {
			t.Fatalf("handle %d closer calls != 1", i)
		}
	}
}

// TestLateTokenDuringConcurrentReuse races many read-only late-token
// challengers against the single goroutine that releases a slot. The current
// occupant must close exactly once; stale reads can only fail with
// ErrInvalidToken and must never touch the next occupant of the reused slot.
func TestLateTokenDuringConcurrentReuse(t *testing.T) {
	p, closer := newTestPool(t, 1)
	const iterations = 200

	for i := 0; i < iterations; i++ {
		h := &fakeHandle{id: i, fd: uintptr(2000 + i), tenant: "rotating"}
		tok, err := p.Register("rotating", h)
		if err != nil {
			t.Fatalf("Register: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Release(tok); err != nil {
				t.Errorf("Release: %v", err)
			}
		}()

		for c := 0; c < 4; c++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := p.OwnerOf(tok); err != nil && !errors.Is(err, ErrInvalidToken) {
					t.Errorf("OwnerOf: unexpected err %v", err)
				}
			}()
		}
		wg.Wait()

		if h.closeCount() != 1 {
			t.Fatalf("iteration %d: handle closed %d times, want 1", i, h.closeCount())
		}
		if closer.callsFor(h.fd) != 1 {
			t.Fatalf("iteration %d: closer calls=%d, want 1", i, closer.callsFor(h.fd))
		}

		next := &fakeHandle{id: i, fd: uintptr(3000 + i), tenant: "next"}
		newToken, err := p.Register("next", next)
		if err != nil {
			t.Fatalf("reuse Register: %v", err)
		}
		if err := p.Release(tok); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("stale Release err = %v, want ErrInvalidToken", err)
		}
		if next.closeCount() != 0 {
			t.Fatal("stale token closed the new slot occupant")
		}
		if err := p.Release(newToken); err != nil {
			t.Fatalf("Release new occupant: %v", err)
		}
		if next.closeCount() != 1 {
			t.Fatalf("new occupant closed %d times, want 1", next.closeCount())
		}
	}
}

// TestSyscallCloserWithRealFile verifies the default closer against a real
// *os.File descriptor (the actual close(2) path).
func TestSyscallCloserWithRealFile(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "handlepool-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}

	p := New(1) // default SyscallCloser
	tok, err := p.Register("os-tenant", f)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	owner, err := p.OwnerOf(tok)
	if err != nil || owner != "os-tenant" {
		t.Fatalf("OwnerOf = %q, %v", owner, err)
	}
	if err := p.Release(tok); err != nil {
		t.Fatalf("Release real file: %v", err)
	}
	if err := f.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("underlying file should already be closed, got %v", err)
	}
	if err := p.Release(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("re-release real file err = %v, want ErrInvalidToken", err)
	}
}
