package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

const testPageSize = 128

type testEnv struct {
	pool   *Pool
	host   *Host
	zeroer *PausableZeroer
}

func newTestEnv(t *testing.T, capacity int) *testEnv {
	t.Helper()
	z := NewPausableZeroer()
	pool, err := NewPool(testPageSize, capacity, z)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(func() {
		z.Resume()
		pool.Close()
	})
	return &testEnv{pool: pool, host: NewHost(pool), zeroer: z}
}

func (e *testEnv) drain(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.pool.WaitDrained(ctx); err != nil {
		t.Fatalf("WaitDrained: %v", err)
	}
}

func fillPattern(b []byte, v byte) {
	for i := range b {
		b[i] = v
	}
}

func snapshotPage(s *Snapshot, key Key) *PhysPage {
	return s.pages[key].page
}

func assertContent(t *testing.T, s *Snapshot, key Key, want []byte) {
	t.Helper()
	got, err := s.Read(key)
	if err != nil {
		t.Fatalf("Read(%q): %v", key, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("page %q content mismatch: got head %v, want head %v", key, got[:8], want[:8])
	}
}

// Forking must share the physical page and its reference count; writes must
// detach a private copy and leave every sibling snapshot untouched.
func TestForkCopyOnWrite(t *testing.T) {
	env := newTestEnv(t, 4)
	ctx := context.Background()

	root := env.host.NewSnapshot()
	if err := env.host.Grant(ctx, root, "secret", func(p []byte) { fillPattern(p, 0xAA) }); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	rootPage := snapshotPage(root, "secret")

	child1, err := env.host.Fork(root)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	child2, err := env.host.Fork(root)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	if got := env.pool.Refs(rootPage); got != 3 {
		t.Fatalf("shared page refs = %d, want 3", got)
	}
	id1, _ := child1.PageID("secret")
	id2, _ := child2.PageID("secret")
	id0, _ := root.PageID("secret")
	if id0 != id1 || id1 != id2 || id0 != rootPage.id {
		t.Fatalf("forked snapshots must share one physical page: %d %d %d", id0, id1, id2)
	}

	wantAA := bytes.Repeat([]byte{0xAA}, testPageSize)
	assertContent(t, root, "secret", wantAA)
	assertContent(t, child1, "secret", wantAA)
	assertContent(t, child2, "secret", wantAA)

	// First write on child1: the shared page must be copied before mutation.
	if err := child1.Write(ctx, "secret", func(p []byte) { p[0] = 0x01 }); err != nil {
		t.Fatalf("Write: %v", err)
	}
	newPage := snapshotPage(child1, "secret")
	if newPage.id == rootPage.id {
		t.Fatal("copy-on-write did not allocate a new physical page")
	}
	if got := env.pool.Refs(rootPage); got != 2 {
		t.Fatalf("old shared page refs = %d, want 2", got)
	}
	if got := env.pool.Refs(newPage); got != 1 {
		t.Fatalf("private page refs = %d, want 1", got)
	}

	wantMut := bytes.Repeat([]byte{0xAA}, testPageSize)
	wantMut[0] = 0x01
	assertContent(t, child1, "secret", wantMut)
	assertContent(t, root, "secret", wantAA)
	assertContent(t, child2, "secret", wantAA)

	// Second write on the now-private page must mutate in place.
	if err := child1.Write(ctx, "secret", func(p []byte) { p[1] = 0x02 }); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if snapshotPage(child1, "secret").id != newPage.id {
		t.Fatal("write to a private page must not copy again")
	}

	// Releasing one sibling must not zero the page the root still holds.
	child2.Revoke()
	if got := env.pool.Refs(rootPage); got != 1 {
		t.Fatalf("root page refs = %d, want 1", got)
	}
	assertContent(t, root, "secret", wantAA)

	// Last reference out: the old page is zeroed and returned to the pool,
	// while the still-authorized child keeps its own secret.
	root.Revoke()
	env.drain(t)
	if got := env.pool.pages[rootPage.id].state; got != stateFree {
		t.Fatalf("root page state = %v, want stateFree", got)
	}
	if !allZero(env.pool.pages[rootPage.id].mem) {
		t.Fatal("released secret page was not zeroed")
	}
	wantMut[1] = 0x02
	assertContent(t, child1, "secret", wantMut)

	child1.Revoke()
	env.drain(t)
	st := env.pool.Stats()
	if st.Live != 0 || st.Free != st.Created || st.Quarantined != 0 {
		t.Fatalf("unexpected final stats: %+v", st)
	}
	for _, pg := range env.pool.pages {
		if !allZero(pg.mem) {
			t.Fatalf("page %d still contains data after shutdown", pg.id)
		}
	}
}

// With the zeroer paused, fully released pages stay pending and the tiny pool
// must refuse reallocation; after resume the pages are wiped, reused, and the
// reference ledger stays consistent.
func TestRevokeZeroAndReuse(t *testing.T) {
	env := newTestEnv(t, 2)
	ctx := context.Background()

	snap := env.host.NewSnapshot()
	if err := env.host.Grant(ctx, snap, "k1", func(p []byte) { fillPattern(p, 0x11) }); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if err := env.host.Grant(ctx, snap, "k2", func(p []byte) { fillPattern(p, 0x22) }); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if st := env.pool.Stats(); st.Created != 2 || st.Live != 2 {
		t.Fatalf("stats after grants = %+v", st)
	}

	env.zeroer.Pause()
	snap.Revoke()
	if st := env.pool.Stats(); st.PendingZero != 2 || st.Free != 0 || st.Live != 0 {
		t.Fatalf("stats while paused = %+v", st)
	}
	if _, err := env.pool.TryAlloc(); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("TryAlloc before zeroing = %v, want ErrPoolExhausted", err)
	}

	allocCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err := env.pool.Alloc(allocCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocking Alloc = %v, want context deadline", err)
	}

	env.zeroer.Resume()
	env.drain(t)
	if st := env.pool.Stats(); st.Free != 2 || st.PendingZero != 0 {
		t.Fatalf("stats after drain = %+v", st)
	}
	if env.zeroer.Calls() != 2 {
		t.Fatalf("zeroer calls = %d, want 2", env.zeroer.Calls())
	}

	pg, err := env.pool.TryAlloc()
	if err != nil {
		t.Fatalf("TryAlloc after wipe: %v", err)
	}
	if !allZero(pg.mem) {
		t.Fatal("reused page still carries old secret bytes")
	}
	if pg.state != stateLive || pg.refs != 1 {
		t.Fatalf("reused page state/refs = %v/%d", pg.state, pg.refs)
	}
	env.pool.Release(pg)
	env.drain(t)
}

// A failed zero must quarantine the page forever: it keeps its secret bytes,
// never returns to the free list, and capacity shrinks permanently.
func TestZeroFailureQuarantine(t *testing.T) {
	env := newTestEnv(t, 2)
	ctx := context.Background()
	env.zeroer.FailPage(0)

	snap := env.host.NewSnapshot()
	secret := bytes.Repeat([]byte{0x5A}, testPageSize)
	if err := env.host.Grant(ctx, snap, "k", func(p []byte) { copy(p, secret) }); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	bad := snapshotPage(snap, "k")
	snap.Revoke()
	env.drain(t)

	st := env.pool.Stats()
	if st.Quarantined != 1 || st.Free != 0 || st.Created != 1 {
		t.Fatalf("stats after failed zero = %+v", st)
	}
	if bad.state != stateDead {
		t.Fatalf("failed page state = %v, want stateDead", bad.state)
	}
	if !bytes.Equal(bad.mem, secret) {
		t.Fatal("failed zero unexpectedly altered the quarantined page bytes")
	}
	if ids := env.zeroer.FailedIDs(); len(ids) != 1 || ids[0] != 0 {
		t.Fatalf("FailedIDs = %v, want [0]", ids)
	}

	// The dead ID must never come back, even across alloc/release cycles.
	for i := 0; i < 4; i++ {
		pg, err := env.pool.TryAlloc()
		if err != nil {
			t.Fatalf("TryAlloc cycle %d: %v", i, err)
		}
		if pg.id == bad.id {
			t.Fatal("quarantined page was reallocated")
		}
		if !allZero(pg.mem) {
			t.Fatal("new page must be zeroed")
		}
		env.pool.Release(pg)
		env.drain(t)
	}

	pg, err := env.pool.TryAlloc()
	if err != nil {
		t.Fatalf("TryAlloc healthy page: %v", err)
	}
	if _, err := env.pool.TryAlloc(); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("TryAlloc = %v, want ErrPoolExhausted", err)
	}
	env.pool.Release(pg)
	env.drain(t)

	st = env.pool.Stats()
	if st.Free+st.Live+st.PendingZero+st.Quarantined != st.Created {
		t.Fatalf("ledger buckets do not sum to Created: %+v", st)
	}
}

// Many concurrent writers forking one root must each get an isolated private
// copy while the root stays byte-for-byte unchanged, even with a tiny pool.
func TestConcurrentWrites(t *testing.T) {
	env := newTestEnv(t, 3)
	ctx := context.Background()

	root := env.host.NewSnapshot()
	if err := env.host.Grant(ctx, root, "k", func(p []byte) { fillPattern(p, 0x07) }); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	const goroutines = 25
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		marker := byte(i + 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			child, err := env.host.Fork(root)
			if err != nil {
				errs <- err
				return
			}
			if err := child.Write(ctx, "k", func(p []byte) {
				p[0] = marker
				p[testPageSize-1] = marker
			}); err != nil {
				errs <- err
				child.Revoke()
				return
			}
			got, err := child.Read("k")
			if err != nil {
				errs <- err
				child.Revoke()
				return
			}
			if got[0] != marker || got[testPageSize-1] != marker {
				errs <- fmt.Errorf("marker lost: got %d/%d want %d", got[0], got[testPageSize-1], marker)
			}
			for _, v := range got[1 : testPageSize-1] {
				if v != 0x07 {
					errs <- fmt.Errorf("shared interior corrupted: %d", v)
					break
				}
			}
			child.Revoke()
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	rootPage := snapshotPage(root, "k")
	if got := env.pool.Refs(rootPage); got != 1 {
		t.Fatalf("root page refs after children revoked = %d, want 1", got)
	}
	assertContent(t, root, "k", bytes.Repeat([]byte{0x07}, testPageSize))

	root.Revoke()
	env.drain(t)
	st := env.pool.Stats()
	if st.Quarantined != 0 || st.Live != 0 {
		t.Fatalf("unexpected stats after concurrent run: %+v", st)
	}
}

// A mixed workload of forks, writes, single-page releases and revokes must
// keep per-page contents isolated and the reference ledger balanced.
func TestConcurrentForkWriteRevoke(t *testing.T) {
	env := newTestEnv(t, 4)
	ctx := context.Background()

	root := env.host.NewSnapshot()
	keys := []Key{"a", "b"}
	if err := env.host.Grant(ctx, root, keys[0], func(p []byte) { fillPattern(p, 0xA0) }); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if err := env.host.Grant(ctx, root, keys[1], func(p []byte) { fillPattern(p, 0xB0) }); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	const rounds = 20
	var wg sync.WaitGroup
	errs := make(chan error, rounds)
	start := make(chan struct{})
	for r := 0; r < rounds; r++ {
		r := r
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			child, err := env.host.Fork(root)
			if err != nil {
				errs <- err
				return
			}
			key := keys[r%2]
			mark := byte(r)
			if err := child.Write(ctx, key, func(p []byte) { p[0] = mark }); err != nil {
				errs <- err
				child.Revoke()
				return
			}
			got, err := child.Read(key)
			if err != nil || got[0] != mark {
				errs <- fmt.Errorf("round %d read mismatch: %v %v", r, err, got[:1])
			}
			if r%3 == 0 {
				if err := child.Release(key); err != nil {
					errs <- err
				}
				if _, err := child.Read(key); !errors.Is(err, ErrNoPage) {
					errs <- fmt.Errorf("round %d released key still readable: %v", r, err)
				}
			}
			child.Revoke()
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	if got := env.pool.Refs(snapshotPage(root, keys[0])); got != 1 {
		t.Fatalf("root key a refs = %d, want 1", got)
	}
	if got := env.pool.Refs(snapshotPage(root, keys[1])); got != 1 {
		t.Fatalf("root key b refs = %d, want 1", got)
	}
	assertContent(t, root, keys[0], bytes.Repeat([]byte{0xA0}, testPageSize))
	assertContent(t, root, keys[1], bytes.Repeat([]byte{0xB0}, testPageSize))

	root.Revoke()
	env.drain(t)
	st := env.pool.Stats()
	if st.Live != 0 || st.Quarantined != 0 || st.Free != st.Created {
		t.Fatalf("unexpected final stats: %+v", st)
	}
}

// While the zeroer is paused, detached pages stay in the zeroing queue and
// cannot be reused; resume processes the queue in order and the page returns
// to the free list with its bytes wiped.
func TestPausedZeroerOrdering(t *testing.T) {
	env := newTestEnv(t, 1)
	ctx := context.Background()

	// First lifecycle runs with the zeroer active so a recycled page exists.
	snap := env.host.NewSnapshot()
	if err := env.host.Grant(ctx, snap, "k", func(p []byte) { p[0] = 1 }); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	pageID := snapshotPage(snap, "k").id
	snap.Revoke()
	env.drain(t)
	callsBefore := env.zeroer.Calls()

	// Pause, then create and release a secret: nothing must be wiped or
	// reallocated while the zeroer remains suspended.
	env.zeroer.Pause()
	snap = env.host.NewSnapshot()
	if err := env.host.Grant(ctx, snap, "k", func(p []byte) { p[0] = 2 }); err != nil {
		t.Fatalf("Grant while paused: %v", err)
	}
	snap.Revoke()
	if st := env.pool.Stats(); st.PendingZero != 1 || st.Free != 0 {
		t.Fatalf("paused stats = %+v", st)
	}
	if _, err := env.pool.TryAlloc(); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("TryAlloc while paused = %v, want ErrPoolExhausted", err)
	}
	if env.zeroer.Calls() != callsBefore {
		t.Fatal("paused zeroer must not wipe pages")
	}

	env.zeroer.Resume()
	env.drain(t)
	zeroed := env.zeroer.ZeroedIDs()
	if len(zeroed) != 2 || zeroed[1] != pageID {
		t.Fatalf("ZeroedIDs = %v, want last wipe of page %d", zeroed, pageID)
	}

	pg, err := env.pool.TryAlloc()
	if err != nil {
		t.Fatalf("TryAlloc after resume: %v", err)
	}
	if pg.id != pageID || !allZero(pg.mem) {
		t.Fatal("recycled page must be the wiped page with zeroed bytes")
	}
	env.pool.Release(pg)
	env.drain(t)
}

// Revoked snapshots must reject every mutating or reading API and revoke is
// idempotent; duplicate grants and unknown keys report dedicated errors.
func TestRevokedAndMissingKeys(t *testing.T) {
	env := newTestEnv(t, 2)
	ctx := context.Background()

	snap := env.host.NewSnapshot()
	if err := env.host.Grant(ctx, snap, "k", func(p []byte) { p[0] = 1 }); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if err := env.host.Grant(ctx, snap, "k", nil); !errors.Is(err, ErrPageExists) {
		t.Fatalf("duplicate Grant = %v, want ErrPageExists", err)
	}
	if _, err := snap.Read("missing"); !errors.Is(err, ErrNoPage) {
		t.Fatalf("Read missing = %v, want ErrNoPage", err)
	}
	if _, err := snap.PageID("missing"); !errors.Is(err, ErrNoPage) {
		t.Fatalf("PageID missing = %v, want ErrNoPage", err)
	}
	if err := snap.Write(ctx, "missing", nil); !errors.Is(err, ErrNoPage) {
		t.Fatalf("Write missing = %v, want ErrNoPage", err)
	}

	if _, err := env.host.Fork(snap); err != nil {
		t.Fatalf("Fork before revoke: %v", err)
	}
	snap.Revoke()
	snap.Revoke() // idempotent
	if !snap.Revoked() {
		t.Fatal("snapshot should report revoked")
	}
	if _, err := env.host.Fork(snap); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Fork revoked = %v, want ErrRevoked", err)
	}
	if _, err := snap.Read("k"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Read revoked = %v, want ErrRevoked", err)
	}
	if err := snap.Write(ctx, "k", nil); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Write revoked = %v, want ErrRevoked", err)
	}
	if err := snap.Release("k"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Release revoked = %v, want ErrRevoked", err)
	}
	if err := env.host.Grant(ctx, snap, "k2", nil); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Grant revoked = %v, want ErrRevoked", err)
	}

	env.drain(t)
}

// The accounting buckets must always sum to the number of created pages.
func TestStatsInvariant(t *testing.T) {
	env := newTestEnv(t, 3)
	ctx := context.Background()

	check := func(stage string) {
		t.Helper()
		st := env.pool.Stats()
		if st.Free+st.Live+st.PendingZero+st.Quarantined != st.Created {
			t.Fatalf("%s: ledger buckets %+v do not sum to Created %d", stage, st, st.Created)
		}
		if st.Created > st.Capacity {
			t.Fatalf("%s: created %d exceeds capacity %d", stage, st.Created, st.Capacity)
		}
	}

	s1 := env.host.NewSnapshot()
	s2 := env.host.NewSnapshot()
	for _, key := range []Key{"a", "b", "c"} {
		if err := env.host.Grant(ctx, s1, key, func(p []byte) { p[0] = 9 }); err != nil {
			t.Fatalf("Grant: %v", err)
		}
	}
	check("after grants")

	child, err := env.host.Fork(s1)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	check("after fork")

	env.zeroer.Pause()
	s1.Revoke()
	check("after partial revoke")
	if err := child.Write(ctx, "a", func(p []byte) { p[1] = 8 }); err != nil {
		t.Fatalf("Write: %v", err)
	}
	check("after cow")

	if err := env.host.TryGrant(s2, "x", nil); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("TryGrant = %v, want ErrPoolExhausted", err)
	}

	env.zeroer.Resume()
	child.Revoke()
	s2.Revoke()
	env.drain(t)
	check("after drain")
}
