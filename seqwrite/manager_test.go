package seqwrite

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"
)

// barrier is a reusable rendezvous for n goroutines.
type barrier struct {
	mu    sync.Mutex
	n     int
	count int
	ch    chan struct{}
}

func newBarrier(n int) *barrier { return &barrier{n: n, ch: make(chan struct{})} }

func (b *barrier) wait() {
	b.mu.Lock()
	b.count++
	if b.count == b.n {
		b.count = 0
		close(b.ch)
		b.ch = make(chan struct{})
		b.mu.Unlock()
		return
	}
	ch := b.ch
	b.mu.Unlock()
	<-ch
}

// test record layout inside a committed reservation:
//   [0:4]   "DATA"
//   [4:12]  offset of the record
//   [12:20] length of the record
//   [20:]   fill byte derived from the offset
var dataMagic = []byte{'D', 'A', 'T', 'A'}

const dataHeaderSize = 20

func genData(offset, length uint64) []byte {
	b := make([]byte, length)
	copy(b[0:4], dataMagic)
	binary.LittleEndian.PutUint64(b[4:12], offset)
	binary.LittleEndian.PutUint64(b[12:20], length)
	fill := byte(offset % 251)
	for i := dataHeaderSize; i < len(b); i++ {
		b[i] = fill
	}
	return b
}

// verifyPrefix walks the visible prefix and checks that it is a contiguous
// sequence of non-overlapping records: either intact data records or valid
// hole records, each starting exactly where the previous one ended.
func verifyPrefix(t *testing.T, m *Manager) {
	t.Helper()
	vis := m.Visible()
	pos := uint64(0)
	for pos < vis {
		hdr := make([]byte, HoleHeaderSize)
		if _, err := m.ReadAt(hdr, int64(pos)); err != nil {
			t.Fatalf("read header at %d: %v", pos, err)
		}
		if hole, ok := ParseHole(hdr); ok {
			if hole.Offset != pos {
				t.Fatalf("hole at %d claims offset %d", pos, hole.Offset)
			}
			if hole.Length == 0 || pos+hole.Length > vis {
				t.Fatalf("hole at %d has bad length %d (visible %d)", pos, hole.Length, vis)
			}
			pos += hole.Length
			continue
		}
		if !bytes.Equal(hdr[0:4], dataMagic) {
			t.Fatalf("record at %d is neither hole nor data", pos)
		}
		off := binary.LittleEndian.Uint64(hdr[4:12])
		length := binary.LittleEndian.Uint64(hdr[12:20])
		if off != pos {
			t.Fatalf("data record at %d claims offset %d", pos, off)
		}
		if length < dataHeaderSize || pos+length > vis {
			t.Fatalf("data record at %d has bad length %d (visible %d)", pos, length, vis)
		}
		body := make([]byte, length-dataHeaderSize)
		if _, err := m.ReadAt(body, int64(pos+dataHeaderSize)); err != nil {
			t.Fatalf("read body at %d: %v", pos, err)
		}
		fill := byte(pos % 251)
		for i, got := range body {
			if got != fill {
				t.Fatalf("data record at %d corrupted at +%d: got %d want %d", pos, i, got, fill)
			}
		}
		pos += length
	}
	if pos != vis {
		t.Fatalf("prefix walk ended at %d, visible is %d", pos, vis)
	}
}

// checkNonOverlapping verifies all reserved ranges are disjoint, sorted,
// and tile [0, total) with no gaps.
func checkNonOverlapping(t *testing.T, rs []*Reservation) {
	t.Helper()
	sorted := make([]*Reservation, len(rs))
	copy(sorted, rs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Offset < sorted[j].Offset })
	expect := uint64(0)
	for _, r := range sorted {
		if r.Offset != expect {
			t.Fatalf("gap or overlap: reservation at %d, expected %d", r.Offset, expect)
		}
		expect += r.Length
	}
}

func TestOutOfOrderPublish(t *testing.T) {
	m := NewManager(NewMemStorage())
	r0, _ := m.Reserve(64)
	r1, _ := m.Reserve(64)
	r2, _ := m.Reserve(64)

	for _, r := range []*Reservation{r0, r1, r2} {
		if _, err := r.WriteAt(genData(r.Offset, r.Length), 0); err != nil {
			t.Fatal(err)
		}
	}

	// Complete out of order: tail first.
	if err := r2.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := m.Visible(); got != 0 {
		t.Fatalf("visible = %d after tail commit, want 0", got)
	}
	if err := r0.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := m.Visible(); got != 64 {
		t.Fatalf("visible = %d, want 64 (blocked by r1)", got)
	}
	if err := r1.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := m.Visible(); got != 192 {
		t.Fatalf("visible = %d, want 192", got)
	}
	verifyPrefix(t, m)
	checkNonOverlapping(t, []*Reservation{r0, r1, r2})
}

func TestFailFillsRecognizableHole(t *testing.T) {
	st := NewMemStorage()
	m := NewManager(st)
	r0, _ := m.Reserve(128)
	r1, _ := m.Reserve(128)
	r2, _ := m.Reserve(128)

	for _, r := range []*Reservation{r0, r1, r2} {
		r.WriteAt(genData(r.Offset, r.Length), 0)
	}
	cause := errors.New("disk hiccup")
	if err := r1.Fail(cause); err != nil {
		t.Fatal(err)
	}
	if got := m.Visible(); got != 0 {
		t.Fatalf("visible = %d, want 0 (r0 still pending)", got)
	}
	r0.Commit()
	r2.Commit()
	if got := m.Visible(); got != 384 {
		t.Fatalf("visible = %d, want 384", got)
	}

	hdr := make([]byte, HoleHeaderSize)
	if _, err := m.ReadAt(hdr, 128); err != nil {
		t.Fatal(err)
	}
	hole, ok := ParseHole(hdr)
	if !ok {
		t.Fatal("no recognizable hole record at 128")
	}
	if hole.Offset != 128 || hole.Length != 128 || hole.Reason != HoleReasonFailed {
		t.Fatalf("bad hole: %+v", hole)
	}
	if hole.Cause != "disk hiccup" {
		t.Fatalf("hole cause = %q", hole.Cause)
	}
	verifyPrefix(t, m)
}

func TestCancelNeverReassignsRange(t *testing.T) {
	m := NewManager(NewMemStorage())
	r0, _ := m.Reserve(64)
	r0.WriteAt(genData(0, 64), 0)
	if err := r0.Cancel(); err != nil {
		t.Fatal(err)
	}
	// The canceled range must not be handed out again.
	r1, err := m.Reserve(64)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Offset != 64 {
		t.Fatalf("canceled range reused: new reservation at %d, want 64", r1.Offset)
	}
	r1.WriteAt(genData(r1.Offset, r1.Length), 0)
	if err := r1.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := m.Visible(); got != 128 {
		t.Fatalf("visible = %d, want 128", got)
	}
	hdr := make([]byte, HoleHeaderSize)
	m.ReadAt(hdr, 0)
	hole, ok := ParseHole(hdr)
	if !ok || hole.Reason != HoleReasonCanceled {
		t.Fatalf("range 0 not a cancel hole: %+v ok=%v", hole, ok)
	}
	verifyPrefix(t, m)
}

func TestHoleFailurePoisonsLog(t *testing.T) {
	st := NewMemStorage()
	m := NewManager(st)
	r0, _ := m.Reserve(64)
	r1, _ := m.Reserve(64)
	r0.WriteAt(genData(0, 64), 0)
	r1.WriteAt(genData(64, 64), 0)

	// Make persistence fail: Fail cannot write its hole -> poison.
	st.SyncErr = errors.New("fsync exploded")
	if err := r0.Fail(errors.New("boom")); err == nil {
		t.Fatal("Fail should report the hole persistence failure")
	}
	if m.Poisoned() == nil {
		t.Fatal("manager should be poisoned")
	}
	// Subsequent publishes must fail.
	if _, err := m.Reserve(64); err == nil {
		t.Fatal("Reserve on poisoned log should fail")
	}
	if err := r1.Commit(); err == nil {
		t.Fatal("Commit on poisoned log should fail")
	}
	if got := m.Visible(); got != 0 {
		t.Fatalf("visible prefix moved on poisoned log: %d", got)
	}
}

func TestCommitSyncFailureFallsBackToHole(t *testing.T) {
	st := NewMemStorage()
	m := NewManager(st)
	r0, _ := m.Reserve(64)
	r0.WriteAt(genData(0, 64), 0)

	// First Sync (commit) fails; hole write then succeeds after we clear
	// the error. Simulate a transient failure by clearing on first call.
	st.SyncErr = errors.New("transient")
	if err := r0.Commit(); err == nil {
		t.Fatal("commit should surface the durability failure")
	}
	// Log must be poisoned because the fallback hole could not be persisted.
	if m.Poisoned() == nil {
		t.Fatal("expected poisoned log after unpersistable hole")
	}
}

func TestWriteOutsideReservationRejected(t *testing.T) {
	m := NewManager(NewMemStorage())
	r, _ := m.Reserve(64)
	if _, err := r.WriteAt(make([]byte, 65), 0); err != ErrOutOfRange {
		t.Fatalf("err = %v, want ErrOutOfRange", err)
	}
	if _, err := r.WriteAt(make([]byte, 1), 64); err != ErrOutOfRange {
		t.Fatalf("err = %v, want ErrOutOfRange", err)
	}
	if _, err := r.WriteAt(make([]byte, 1), -1); err != ErrOutOfRange {
		t.Fatalf("err = %v, want ErrOutOfRange", err)
	}
	r.Commit()
	if _, err := r.WriteAt(make([]byte, 1), 0); err != ErrSealed {
		t.Fatalf("err = %v, want ErrSealed", err)
	}
	if err := r.Commit(); err != ErrSealed {
		t.Fatalf("double commit err = %v, want ErrSealed", err)
	}
}

// TestBarrierInterleaved orchestrates producers through phase barriers:
// all reserve, then all write, then all finalize in reverse order with
// mixed outcomes. Verifies non-overlap and prefix contiguity.
func TestBarrierInterleaved(t *testing.T) {
	st := NewMemStorage()
	m := NewManager(st)
	const producers = 8
	b := newBarrier(producers)
	rs := make([]*Reservation, producers)
	errs := make([]error, producers)
	var wg sync.WaitGroup

	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			size := uint64(64 + 16*i)
			r, err := m.Reserve(size)
			if err != nil {
				errs[i] = err
				return
			}
			rs[i] = r
			b.wait() // phase 1: everyone reserved

			if _, err := r.WriteAt(genData(r.Offset, r.Length), 0); err != nil {
				errs[i] = err
				return
			}
			b.wait() // phase 2: everyone wrote

			// phase 3: finalize in reverse index order, mixed outcomes.
			time.Sleep(time.Duration(producers-i) * 2 * time.Millisecond)
			switch i % 3 {
			case 0:
				errs[i] = r.Commit()
			case 1:
				errs[i] = r.Fail(fmt.Errorf("producer %d gave up", i))
			case 2:
				errs[i] = r.Cancel()
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("producer %d: %v", i, err)
		}
	}
	checkNonOverlapping(t, rs)
	verifyPrefix(t, m)

	// Everything finalized: visible must equal the sum of all lengths.
	var total uint64
	for _, r := range rs {
		total += r.Length
	}
	if got := m.Visible(); got != total {
		t.Fatalf("visible = %d, want %d", got, total)
	}
}

// TestConcurrentStress hammers the manager with randomized producers and
// concurrent readers, then verifies the invariants.
func TestConcurrentStress(t *testing.T) {
	st := NewMemStorage()
	m := NewManager(st)
	const producers = 32

	rs := make([]*Reservation, producers)
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rng := rand.New(rand.NewSource(int64(i) + 1))
			size := uint64(64 + 8*rng.Intn(120))
			r, err := m.Reserve(size)
			if err != nil {
				t.Errorf("reserve: %v", err)
				return
			}
			rs[i] = r
			if _, err := r.WriteAt(genData(r.Offset, r.Length), 0); err != nil {
				t.Errorf("write: %v", err)
				return
			}
			time.Sleep(time.Duration(rng.Intn(3)) * time.Millisecond)
			switch n := rng.Intn(10); {
			case n < 7:
				if err := r.Commit(); err != nil {
					t.Errorf("commit: %v", err)
				}
			case n < 9:
				if err := r.Fail(errors.New("flaky")); err != nil {
					t.Errorf("fail: %v", err)
				}
			default:
				if err := r.Cancel(); err != nil {
					t.Errorf("cancel: %v", err)
				}
			}
		}(i)
	}

	// Concurrent readers must only ever see a contiguous valid prefix.
	readers := 4
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	for i := 0; i < readers; i++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			ctx := context.Background()
			for {
				select {
				case <-stop:
					return
				default:
				}
				vis := m.Visible()
				if vis == 0 {
					continue
				}
				if err := m.WaitVisible(ctx, vis); err != nil {
					t.Errorf("WaitVisible: %v", err)
					return
				}
				// Invariant: a read never exposes bytes beyond the
				// visible prefix at the moment of the read.
				buf := make([]byte, 16)
				n, err := m.ReadAt(buf, int64(vis))
				if err != nil && err != io.EOF {
					t.Errorf("read at visible: err = %v", err)
					return
				}
				if uint64(int64(vis)+int64(n)) > m.Visible() {
					t.Errorf("read exposed %d bytes past visible prefix", n)
					return
				}
			}
		}()
	}

	close(start)
	wg.Wait()
	close(stop)
	rwg.Wait()

	checkNonOverlapping(t, rs)
	verifyPrefix(t, m)

	var total uint64
	for _, r := range rs {
		total += r.Length
	}
	if got := m.Visible(); got != total {
		t.Fatalf("visible = %d, want %d", got, total)
	}
}

func TestWaitVisibleUnblocks(t *testing.T) {
	m := NewManager(NewMemStorage())
	r0, _ := m.Reserve(64)
	r1, _ := m.Reserve(64)
	r0.WriteAt(genData(0, 64), 0)
	r1.WriteAt(genData(64, 64), 0)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.WaitVisible(ctx, 128) }()

	r1.Commit() // out of order: waiter must not unblock yet
	select {
	case err := <-done:
		t.Fatalf("WaitVisible returned early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	r0.Commit()
	if err := <-done; err != nil {
		t.Fatalf("WaitVisible: %v", err)
	}
}
