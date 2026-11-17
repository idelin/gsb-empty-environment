package netsec_test

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	netsec "example.org/netsec/q0257"
)

type recordingZeroizer struct {
	mu    sync.Mutex
	calls int
	pages [][]byte
}

func (z *recordingZeroizer) Zero(page []byte) error {
	z.mu.Lock()
	z.calls++
	z.pages = append(z.pages, append([]byte(nil), page...))
	z.mu.Unlock()
	clear(page)
	return nil
}

func (z *recordingZeroizer) snapshot() (int, [][]byte) {
	z.mu.Lock()
	defer z.mu.Unlock()
	pages := make([][]byte, len(z.pages))
	for i := range z.pages {
		pages[i] = append([]byte(nil), z.pages[i]...)
	}
	return z.calls, pages
}

type pausableZeroizer struct {
	started    chan struct{}
	resume     chan struct{}
	startOnce  sync.Once
	resumeOnce sync.Once
}

func newPausableZeroizer() *pausableZeroizer {
	return &pausableZeroizer{
		started: make(chan struct{}),
		resume:  make(chan struct{}),
	}
}

func (z *pausableZeroizer) Zero(page []byte) error {
	z.startOnce.Do(func() { close(z.started) })
	<-z.resume
	clear(page)
	return nil
}

func (z *pausableZeroizer) Resume() {
	z.resumeOnce.Do(func() { close(z.resume) })
}

type failingZeroizer struct {
	mu    sync.Mutex
	calls int
	err   error
}

type noopZeroizer struct{}

func (noopZeroizer) Zero([]byte) error {
	return nil
}

type panicZeroizer struct{}

func (panicZeroizer) Zero([]byte) error {
	panic("scrub device disappeared")
}

func (z *failingZeroizer) Zero(page []byte) error {
	z.mu.Lock()
	z.calls++
	z.mu.Unlock()
	// Simulate a scrub that happened to clear the bytes but still reported a
	// failure. A failed page must remain quarantined regardless.
	clear(page)
	return z.err
}

func (z *failingZeroizer) callCount() int {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.calls
}

func mustSnapshot(t *testing.T, pool *netsec.Pool, data []byte) *netsec.Snapshot {
	t.Helper()
	snapshot, err := pool.NewSnapshot(data)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	return snapshot
}

func mustFork(t *testing.T, snapshot *netsec.Snapshot) *netsec.Snapshot {
	t.Helper()
	fork, err := snapshot.Fork()
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	return fork
}

func mustRead(t *testing.T, snapshot *netsec.Snapshot) []byte {
	t.Helper()
	data, err := snapshot.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return data
}

func mustWrite(t *testing.T, snapshot *netsec.Snapshot, data []byte, offset int64) {
	t.Helper()
	n, err := snapshot.WriteAt(data, offset)
	if err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	if n != len(data) {
		t.Fatalf("WriteAt wrote %d bytes, want %d", n, len(data))
	}
}

func mustPageIDs(t *testing.T, snapshot *netsec.Snapshot) []netsec.PageID {
	t.Helper()
	ids, err := snapshot.PageIDs()
	if err != nil {
		t.Fatalf("PageIDs: %v", err)
	}
	return ids
}

func mustStats(t *testing.T, pool *netsec.Pool) netsec.Stats {
	t.Helper()
	stats := pool.Stats()
	if err := pool.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	return stats
}

func TestForkCopyOnWriteAndLastReference(t *testing.T) {
	zeroizer := &recordingZeroizer{}
	pool, err := netsec.NewPoolWithPageSize(8, 4, zeroizer)
	if err != nil {
		t.Fatal(err)
	}

	original := []byte("page0000page1111")
	base := mustSnapshot(t, pool, original)
	fork := mustFork(t, base)

	baseIDs := mustPageIDs(t, base)
	forkIDs := mustPageIDs(t, fork)
	if !slices.Equal(baseIDs, forkIDs) {
		t.Fatalf("fork mappings differ: base=%v fork=%v", baseIDs, forkIDs)
	}
	stats := mustStats(t, pool)
	if stats.InUse != 2 || stats.References != 4 || stats.Free != 2 {
		t.Fatalf("after fork: %+v", stats)
	}

	mustWrite(t, base, []byte("BASE"), 0)
	if got, want := mustRead(t, base), []byte("BASE0000page1111"); !bytes.Equal(got, want) {
		t.Fatalf("base content = %q, want %q", got, want)
	}
	if got := mustRead(t, fork); !bytes.Equal(got, original) {
		t.Fatalf("fork was corrupted by base write: %q", got)
	}

	newBaseIDs := mustPageIDs(t, base)
	if newBaseIDs[0] == forkIDs[0] {
		t.Fatalf("written page still shared: %d", newBaseIDs[0])
	}
	if newBaseIDs[1] != forkIDs[1] {
		t.Fatalf("untouched page was copied: base=%d fork=%d", newBaseIDs[1], forkIDs[1])
	}
	stats = mustStats(t, pool)
	if stats.InUse != 3 || stats.References != 4 || stats.Free != 1 {
		t.Fatalf("after COW: %+v", stats)
	}

	oldShared := forkIDs[0]
	info, err := pool.PageInfo(oldShared)
	if err != nil {
		t.Fatal(err)
	}
	if info.Refs != 1 || info.State != netsec.PageInUse {
		t.Fatalf("old shared page accounting: %+v", info)
	}

	if err := base.Close(); err != nil {
		t.Fatalf("close base: %v", err)
	}
	if got := mustRead(t, fork); !bytes.Equal(got, original) {
		t.Fatalf("fork content changed when base was released: %q", got)
	}
	calls, zeroed := zeroizer.snapshot()
	if calls != 1 {
		t.Fatalf("zero calls after releasing one private page = %d, want 1", calls)
	}
	if !bytes.Equal(zeroed[0], []byte("BASE0000")) {
		t.Fatalf("zeroizer saw %q, want private base page", zeroed[0])
	}
	stats = mustStats(t, pool)
	if stats.InUse != 2 || stats.References != 2 || stats.Free != 2 || stats.Quarantined != 0 {
		t.Fatalf("after releasing first owner: %+v", stats)
	}

	if err := fork.Close(); err != nil {
		t.Fatalf("close fork: %v", err)
	}
	calls, _ = zeroizer.snapshot()
	if calls != 3 {
		t.Fatalf("zero calls after last reference = %d, want 3", calls)
	}
	stats = mustStats(t, pool)
	if stats.InUse != 0 || stats.References != 0 || stats.Free != 4 {
		t.Fatalf("after releasing last owner: %+v", stats)
	}
	for _, info := range pool.Inspect() {
		data, err := pool.PageBytes(info.ID)
		if err != nil {
			t.Fatalf("PageBytes(%d): %v", info.ID, err)
		}
		if !bytes.Equal(data, make([]byte, 8)) {
			t.Fatalf("free page %d is not zeroed: %v", info.ID, data)
		}
	}
}

func TestUnalignedWriteCopiesOnlyTouchedPages(t *testing.T) {
	pool, err := netsec.NewPoolWithPageSize(8, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("0123456789abcdefghij")
	base := mustSnapshot(t, pool, original)
	fork := mustFork(t, base)
	originalIDs := mustPageIDs(t, fork)

	mustWrite(t, base, []byte("XYZ"), 6)
	if got, want := mustRead(t, base), []byte("012345XYZ9abcdefghij"); !bytes.Equal(got, want) {
		t.Fatalf("base = %q, want %q", got, want)
	}
	if got := mustRead(t, fork); !bytes.Equal(got, original) {
		t.Fatalf("fork = %q, want %q", got, original)
	}

	ids := mustPageIDs(t, base)
	if ids[0] == originalIDs[0] || ids[1] == originalIDs[1] {
		t.Fatalf("touched pages were not copied: before=%v after=%v", originalIDs, ids)
	}
	if ids[2] != originalIDs[2] {
		t.Fatalf("untouched third page was copied: before=%d after=%d", originalIDs[2], ids[2])
	}
	if err := base.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fork.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestRevokeAndForkRules(t *testing.T) {
	pool, err := netsec.NewPoolWithPageSize(8, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := mustSnapshot(t, pool, []byte("secret!!"))
	fork := mustFork(t, base)

	if err := base.Revoke(); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if base.Authorized() {
		t.Fatal("revoked snapshot still authorized")
	}
	if _, err := base.ReadAt(make([]byte, 1), 0); !errors.Is(err, netsec.ErrRevoked) {
		t.Fatalf("ReadAt after revoke: %v", err)
	}
	if _, err := base.WriteAt([]byte("x"), 0); !errors.Is(err, netsec.ErrRevoked) {
		t.Fatalf("WriteAt after revoke: %v", err)
	}
	if _, err := base.Fork(); !errors.Is(err, netsec.ErrRevoked) {
		t.Fatalf("Fork after revoke: %v", err)
	}
	if _, err := base.PageIDs(); !errors.Is(err, netsec.ErrRevoked) {
		t.Fatalf("PageIDs after revoke: %v", err)
	}
	if err := base.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if got := mustRead(t, fork); !bytes.Equal(got, []byte("secret!!")) {
		t.Fatalf("fork content after revoke: %q", got)
	}

	if err := fork.Close(); err != nil {
		t.Fatalf("close fork: %v", err)
	}
	stats := mustStats(t, pool)
	if stats.References != 0 || stats.Free != 2 || stats.InUse != 0 {
		t.Fatalf("final stats: %+v", stats)
	}
}

func TestPausedZeroizationPreventsReuseAndAccess(t *testing.T) {
	zeroizer := newPausableZeroizer()
	defer zeroizer.Resume()

	pool, err := netsec.NewPoolWithPageSize(8, 1, zeroizer)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := mustSnapshot(t, pool, []byte("secret!!"))
	pageID, err := snapshot.PhysicalPage(0)
	if err != nil {
		t.Fatal(err)
	}

	closeResult := make(chan error, 1)
	go func() { closeResult <- snapshot.Close() }()
	<-zeroizer.started

	stats := mustStats(t, pool)
	if stats.Zeroing != 1 || stats.Free != 0 || stats.References != 0 {
		t.Fatalf("while paused: %+v", stats)
	}
	info, err := pool.PageInfo(pageID)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != netsec.PageZeroing {
		t.Fatalf("page state while paused = %s", info.State)
	}
	if _, err := pool.PageBytes(pageID); !errors.Is(err, netsec.ErrPageUnavailable) {
		t.Fatalf("PageBytes while scrubbing: %v", err)
	}
	if _, err := pool.NewSnapshot([]byte("reuse!!!")); !errors.Is(err, netsec.ErrPoolExhausted) {
		t.Fatalf("page was reallocated while scrubbing: %v", err)
	}
	select {
	case err := <-closeResult:
		t.Fatalf("Close returned before zeroizer resumed: %v", err)
	default:
	}

	zeroizer.Resume()
	if err := <-closeResult; err != nil {
		t.Fatalf("Close: %v", err)
	}
	stats = mustStats(t, pool)
	if stats.Free != 1 || stats.Zeroing != 0 || stats.Quarantined != 0 {
		t.Fatalf("after resume: %+v", stats)
	}
	data, err := pool.PageBytes(pageID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, make([]byte, 8)) {
		t.Fatalf("scrubbed page = %v", data)
	}

	reused := mustSnapshot(t, pool, []byte("reused!!"))
	if got := mustRead(t, reused); !bytes.Equal(got, []byte("reused!!")) {
		t.Fatalf("reused page = %q", got)
	}
	if err := reused.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestZeroizationFailureQuarantinesPage(t *testing.T) {
	boom := errors.New("scrub device failed")
	zeroizer := &failingZeroizer{err: boom}
	pool, err := netsec.NewPoolWithPageSize(8, 1, zeroizer)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := mustSnapshot(t, pool, []byte("secret!!"))
	pageID, err := snapshot.PhysicalPage(0)
	if err != nil {
		t.Fatal(err)
	}

	err = snapshot.Close()
	if !errors.Is(err, boom) {
		t.Fatalf("Close error = %v, want injected scrub error", err)
	}
	var zeroErr *netsec.ZeroError
	if !errors.As(err, &zeroErr) || zeroErr.Page != pageID {
		t.Fatalf("Close error = %#v, want ZeroError for page %d", err, pageID)
	}
	stats := mustStats(t, pool)
	if stats.Quarantined != 1 || stats.Free != 0 || stats.References != 0 {
		t.Fatalf("stats after scrub failure: %+v", stats)
	}
	info, err := pool.PageInfo(pageID)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != netsec.PageQuarantined || info.ZeroError == nil || info.Refs != 0 {
		t.Fatalf("quarantined page info: %+v", info)
	}
	if _, err := pool.NewSnapshot([]byte("newpage!")); !errors.Is(err, netsec.ErrPoolExhausted) {
		t.Fatalf("quarantined page was reallocated: %v", err)
	}
	if zeroizer.callCount() != 1 {
		t.Fatalf("zero attempts = %d, want 1", zeroizer.callCount())
	}

	err = snapshot.Close()
	if !errors.Is(err, boom) {
		t.Fatalf("repeated Close error = %v, want original failure", err)
	}
	if zeroizer.callCount() != 1 {
		t.Fatalf("repeated Close retried zeroization: attempts=%d", zeroizer.callCount())
	}
	if err := pool.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestZeroizationVerificationAndPanicQuarantine(t *testing.T) {
	tests := []struct {
		name    string
		zeroer  netsec.Zeroizer
		wantErr error
	}{
		{
			name:    "verification",
			zeroer:  noopZeroizer{},
			wantErr: netsec.ErrZeroizeVerification,
		},
		{
			name:    "panic",
			zeroer:  panicZeroizer{},
			wantErr: netsec.ErrZeroizePanic,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, err := netsec.NewPoolWithPageSize(8, 1, test.zeroer)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := mustSnapshot(t, pool, []byte("secret!!"))
			if err := snapshot.Close(); !errors.Is(err, test.wantErr) {
				t.Fatalf("Close error = %v, want %v", err, test.wantErr)
			}
			stats := mustStats(t, pool)
			if stats.Quarantined != 1 || stats.Free != 0 {
				t.Fatalf("stats = %+v", stats)
			}
			if _, err := pool.NewSnapshot([]byte("newpage!")); !errors.Is(err, netsec.ErrPoolExhausted) {
				t.Fatalf("quarantined page was reused: %v", err)
			}
		})
	}
}

func TestFailedCOWDoesNotChangeSnapshot(t *testing.T) {
	pool, err := netsec.NewPoolWithPageSize(8, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := mustSnapshot(t, pool, []byte("original"))
	fork := mustFork(t, base)

	if _, err := base.WriteAt([]byte("changed!"), 0); !errors.Is(err, netsec.ErrPoolExhausted) {
		t.Fatalf("WriteAt error = %v, want ErrPoolExhausted", err)
	}
	if got := mustRead(t, base); !bytes.Equal(got, []byte("original")) {
		t.Fatalf("base changed after failed COW: %q", got)
	}
	if got := mustRead(t, fork); !bytes.Equal(got, []byte("original")) {
		t.Fatalf("fork changed after failed COW: %q", got)
	}
	stats := mustStats(t, pool)
	if stats.References != 2 || stats.InUse != 1 || stats.Free != 0 {
		t.Fatalf("stats after failed COW: %+v", stats)
	}

	if err := fork.Close(); err != nil {
		t.Fatal(err)
	}
	if err := base.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentWritesAcrossForks(t *testing.T) {
	const (
		pageSize = 16
		forks    = 8
	)
	zeroizer := &recordingZeroizer{}
	pool, err := netsec.NewPoolWithPageSize(pageSize, forks+1, zeroizer)
	if err != nil {
		t.Fatal(err)
	}
	baseData := bytes.Repeat([]byte{0xa5}, pageSize)
	base := mustSnapshot(t, pool, baseData)

	branches := make([]*netsec.Snapshot, forks)
	for i := range branches {
		branches[i] = mustFork(t, base)
	}

	errs := make(chan error, forks)
	var wg sync.WaitGroup
	for i, branch := range branches {
		wg.Add(1)
		go func(i int, branch *netsec.Snapshot) {
			defer wg.Done()
			marker := bytes.Repeat([]byte{byte(0xb0 + i)}, pageSize)
			_, err := branch.WriteAt(marker, 0)
			errs <- err
		}(i, branch)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
	}

	if got := mustRead(t, base); !bytes.Equal(got, baseData) {
		t.Fatalf("base changed during fork writes: %v", got)
	}
	unique := make(map[netsec.PageID]bool, forks)
	for i, branch := range branches {
		want := bytes.Repeat([]byte{byte(0xb0 + i)}, pageSize)
		if got := mustRead(t, branch); !bytes.Equal(got, want) {
			t.Fatalf("branch %d content = %v, want %v", i, got, want)
		}
		id, err := branch.PhysicalPage(0)
		if err != nil {
			t.Fatal(err)
		}
		if unique[id] {
			t.Fatalf("branches share writable page %d", id)
		}
		unique[id] = true
	}
	stats := mustStats(t, pool)
	if stats.InUse != forks+1 || stats.References != forks+1 || stats.Free != 0 {
		t.Fatalf("stats after concurrent writes: %+v", stats)
	}

	all := append([]*netsec.Snapshot{base}, branches...)
	closeErrs := make(chan error, len(all))
	for _, snapshot := range all {
		wg.Add(1)
		go func(snapshot *netsec.Snapshot) {
			defer wg.Done()
			closeErrs <- snapshot.Close()
		}(snapshot)
	}
	wg.Wait()
	close(closeErrs)
	for err := range closeErrs {
		if err != nil {
			t.Fatalf("concurrent close: %v", err)
		}
	}
	stats = mustStats(t, pool)
	if stats.InUse != 0 || stats.References != 0 || stats.Free != forks+1 {
		t.Fatalf("stats after concurrent close: %+v", stats)
	}
	calls, _ := zeroizer.snapshot()
	if calls != forks+1 {
		t.Fatalf("zero calls = %d, want %d", calls, forks+1)
	}
}

func TestConcurrentWriteAndRevokeKeepsSurvivorIntact(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		pool, err := netsec.NewPoolWithPageSize(8, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		base := mustSnapshot(t, pool, []byte("abcdefgh"))
		fork := mustFork(t, base)

		start := make(chan struct{})
		var writeErr, revokeErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, writeErr = base.WriteAt([]byte("WXYZ"), 0)
		}()
		go func() {
			defer wg.Done()
			<-start
			revokeErr = fork.Revoke()
		}()
		close(start)
		wg.Wait()
		if writeErr != nil {
			t.Fatalf("iteration %d write: %v", iteration, writeErr)
		}
		if revokeErr != nil {
			t.Fatalf("iteration %d revoke: %v", iteration, revokeErr)
		}
		if got := mustRead(t, base); !bytes.Equal(got, []byte("WXYZefgh")) {
			t.Fatalf("iteration %d survivor = %q", iteration, got)
		}
		if err := base.Close(); err != nil {
			t.Fatalf("iteration %d close: %v", iteration, err)
		}
		if err := pool.Check(); err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
	}
}

func TestConcurrentRepeatedCloseWaitsForScrub(t *testing.T) {
	const closers = 8
	for iteration := 0; iteration < 20; iteration++ {
		zeroizer := newPausableZeroizer()
		pool, err := netsec.NewPoolWithPageSize(8, 1, zeroizer)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := mustSnapshot(t, pool, []byte("secret!!"))

		var wg sync.WaitGroup
		var completed atomic.Int32
		errs := make(chan error, closers)
		for i := 0; i < closers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- snapshot.Close()
				completed.Add(1)
			}()
		}

		<-zeroizer.started
		if got := completed.Load(); got != 0 {
			t.Fatalf("iteration %d: %d Close calls returned before scrub", iteration, got)
		}
		zeroizer.Resume()
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("iteration %d: %v", iteration, err)
			}
		}
		if completed.Load() != closers {
			t.Fatalf("iteration %d: completed=%d", iteration, completed.Load())
		}
		if err := pool.Check(); err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
	}
}

func TestBoundsAndEmptySnapshot(t *testing.T) {
	pool, err := netsec.NewPoolWithPageSize(8, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	empty := mustSnapshot(t, pool, nil)
	if got := mustRead(t, empty); len(got) != 0 {
		t.Fatalf("empty snapshot = %v", got)
	}
	if n, err := empty.WriteAt(nil, 0); err != nil || n != 0 {
		t.Fatalf("empty WriteAt = (%d, %v)", n, err)
	}
	if _, err := empty.WriteAt([]byte("x"), 0); !errors.Is(err, netsec.ErrOutOfBounds) {
		t.Fatalf("oversized WriteAt error = %v", err)
	}
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot := mustSnapshot(t, pool, []byte("abcdefgh"))
	if _, err := snapshot.WriteAt([]byte("x"), 8); !errors.Is(err, netsec.ErrOutOfBounds) {
		t.Fatalf("write at end error = %v", err)
	}
	if _, err := snapshot.WriteAt([]byte("x"), 9); !errors.Is(err, netsec.ErrOutOfBounds) {
		t.Fatalf("write past end error = %v", err)
	}
	if n, err := snapshot.ReadAt(nil, 8); err != nil || n != 0 {
		t.Fatalf("zero-length read at end = (%d, %v)", n, err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Check(); err != nil {
		t.Fatal(err)
	}
}

func ExamplePool() {
	pool, _ := netsec.NewPoolWithPageSize(8, 2, nil)
	base, _ := pool.NewSnapshot([]byte("topsecret"))
	branch, _ := base.Fork()

	_, _ = base.WriteAt([]byte("public"), 0)

	safe, _ := branch.Bytes()
	fmt.Printf("%s\n", safe)

	_ = base.Close()
	_ = branch.Close()
	// Output: topsecret
}
