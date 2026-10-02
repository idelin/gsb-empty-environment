package logpipe

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// readLogFile parses the WAL and returns the LSNs it contains, in
// file order.
func readLogFile(t *testing.T, path string) []uint64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	defer f.Close()
	var lsns []uint64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		fields := strings.SplitN(sc.Text(), " ", 2)
		n, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			t.Fatalf("corrupt wal line %q: %v", sc.Text(), err)
		}
		lsns = append(lsns, n)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan wal: %v", err)
	}
	return lsns
}

// assertContiguousPrefix verifies lsns is exactly the sequence
// 1,2,...,len(lsns) and that its length is within [min, max].
func assertContiguousPrefix(t *testing.T, what string, lsns []uint64, min, max uint64) {
	t.Helper()
	if uint64(len(lsns)) < min || uint64(len(lsns)) > max {
		t.Fatalf("%s: got %d entries, want within [%d, %d]", what, len(lsns), min, max)
	}
	for i, l := range lsns {
		if l != uint64(i)+1 {
			t.Fatalf("%s: gap at index %d: got LSN %d, want %d", what, i, l, i+1)
		}
	}
}

// TestBarrierRollbackUnderFault is the acceptance scenario: 10
// concurrent producers append 2000 records, persistence barriers are
// pinned at LSN 500 and 1200, and persistence of record 1250 fails.
// Afterwards every record up to LSN 1200 must be durable on disk and
// the buffer accounting must be conserved.
func TestBarrierRollbackUnderFault(t *testing.T) {
	const (
		producers = 10
		total     = 2000
		barrier1  = 500
		barrier2  = 1200
		faultLSN  = 1250
	)
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	injected := errors.New("simulated disk failure")
	p := New(f, Options{BatchSize: 16, FaultLSN: faultLSN, FaultErr: injected})

	b1, err := p.InsertBarrierAt(barrier1)
	if err != nil {
		t.Fatalf("insert barrier@%d: %v", barrier1, err)
	}
	b2, err := p.InsertBarrierAt(barrier2)
	if err != nil {
		t.Fatalf("insert barrier@%d: %v", barrier2, err)
	}

	var subWG sync.WaitGroup
	subWG.Add(1)
	var published []uint64
	go func() {
		defer subWG.Done()
		for r := range p.Subscribe() {
			published = append(published, r.LSN)
		}
	}()

	handles := make([]Handle, total)
	var ticket, hcount int64
	var wg sync.WaitGroup
	for w := 0; w < producers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				i := atomic.AddInt64(&ticket, 1)
				if i > total {
					return
				}
				h, err := p.Append([]byte(fmt.Sprintf("producer-%d-msg-%d", id, i)))
				if err != nil { // pipeline already failed
					return
				}
				handles[atomic.AddInt64(&hcount, 1)-1] = h
			}
		}(w)
	}
	wg.Wait()
	n := atomic.LoadInt64(&hcount)
	if n < faultLSN {
		t.Fatalf("only %d records appended, need at least %d to exercise the fault", n, faultLSN)
	}

	// Both barriers sit before the fault and must be durable.
	if err := b1.Wait(); err != nil {
		t.Fatalf("barrier@%d: %v", barrier1, err)
	}
	if err := b2.Wait(); err != nil {
		t.Fatalf("barrier@%d: %v", barrier2, err)
	}

	// Every handle must resolve: durable up to the fault, rolled back
	// from the fault LSN onward.
	for i := int64(0); i < n; i++ {
		h := handles[i]
		err := h.Wait()
		switch {
		case h.LSN() <= barrier2 && err != nil:
			t.Fatalf("LSN %d is before barrier@%d and must be durable, got %v", h.LSN(), barrier2, err)
		case h.LSN() >= faultLSN && err == nil:
			t.Fatalf("LSN %d is at/after the fault and must be rolled back", h.LSN())
		case h.LSN() >= faultLSN && !errors.Is(err, injected):
			t.Fatalf("LSN %d rolled back with %v, want %v", h.LSN(), err, injected)
		}
	}
	subWG.Wait()

	// Memory accounting must be conserved.
	if got := p.Outstanding(); got != 0 {
		t.Fatalf("outstanding buffers = %d, want 0 (allocated=%d released=%d)",
			got, p.Allocated(), p.Released())
	}
	if p.Allocated() != n || p.Released() != n {
		t.Fatalf("allocated=%d released=%d, want both %d", p.Allocated(), p.Released(), n)
	}
	if !errors.Is(p.Failed(), injected) {
		t.Fatalf("pipeline failure = %v, want %v", p.Failed(), injected)
	}

	// Everything up to barrier2 must be on disk; the on-disk content
	// must be a contiguous prefix of the stream ending before the
	// fault LSN.
	lsns := readLogFile(t, path)
	assertContiguousPrefix(t, "wal", lsns, barrier2, faultLSN-1)

	// Published records are a contiguous prefix covered by the WAL.
	assertContiguousPrefix(t, "published", published, barrier2, faultLSN-1)
	if uint64(len(published)) > uint64(len(lsns)) {
		t.Fatalf("published %d records but WAL holds only %d", len(published), len(lsns))
	}
	if got := p.DurableLSN(); got < barrier2 || got >= faultLSN {
		t.Fatalf("durable LSN = %d, want within [%d, %d)", got, barrier2, faultLSN)
	}

	if err := p.Close(); !errors.Is(err, injected) {
		t.Fatalf("Close = %v, want %v", err, injected)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close wal: %v", err)
	}
}

// TestOrderedDeliveryAndClose exercises the fault-free path: 10
// producers, 2000 records, two barriers, clean close. Every record
// must be durable, published exactly once in LSN order, and all
// buffers released.
func TestOrderedDeliveryAndClose(t *testing.T) {
	const (
		producers = 10
		total     = 2000
	)
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	p := New(f, Options{BatchSize: 32})

	b1, err := p.InsertBarrierAt(500)
	if err != nil {
		t.Fatalf("insert barrier@500: %v", err)
	}
	b2, err := p.InsertBarrierAt(1200)
	if err != nil {
		t.Fatalf("insert barrier@1200: %v", err)
	}

	var subWG sync.WaitGroup
	subWG.Add(1)
	var published []uint64
	go func() {
		defer subWG.Done()
		for r := range p.Subscribe() {
			published = append(published, r.LSN)
		}
	}()

	handles := make([]Handle, total)
	var ticket, hcount int64
	var wg sync.WaitGroup
	for w := 0; w < producers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				i := atomic.AddInt64(&ticket, 1)
				if i > total {
					return
				}
				h, err := p.Append([]byte(fmt.Sprintf("producer-%d-msg-%d", id, i)))
				if err != nil {
					t.Errorf("append: %v", err)
					return
				}
				handles[atomic.AddInt64(&hcount, 1)-1] = h
			}
		}(w)
	}
	wg.Wait()
	if n := atomic.LoadInt64(&hcount); n != total {
		t.Fatalf("appended %d records, want %d", n, total)
	}

	if err := b1.Wait(); err != nil {
		t.Fatalf("barrier@500: %v", err)
	}
	if err := b2.Wait(); err != nil {
		t.Fatalf("barrier@1200: %v", err)
	}
	for _, h := range handles {
		if err := h.Wait(); err != nil {
			t.Fatalf("LSN %d: %v", h.LSN(), err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	subWG.Wait()

	assertContiguousPrefix(t, "wal", readLogFile(t, path), total, total)
	assertContiguousPrefix(t, "published", published, total, total)
	if got := p.Outstanding(); got != 0 {
		t.Fatalf("outstanding buffers = %d, want 0", got)
	}
	if p.Allocated() != total || p.Released() != total {
		t.Fatalf("allocated=%d released=%d, want both %d", p.Allocated(), p.Released(), total)
	}
	if got := p.DurableLSN(); got != total {
		t.Fatalf("durable LSN = %d, want %d", got, total)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close wal: %v", err)
	}
}

// TestTailBarrier verifies the dynamic tail-barrier form: records
// appended after the barrier must not be published before the barrier
// is durable.
func TestTailBarrier(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create wal: %v", err)
	}
	p := New(f, Options{BatchSize: 1000}) // large batch: only barriers force syncs

	var subWG sync.WaitGroup
	subWG.Add(1)
	var published []uint64
	go func() {
		defer subWG.Done()
		for r := range p.Subscribe() {
			published = append(published, r.LSN)
		}
	}()

	var first []Handle
	for i := 0; i < 10; i++ {
		h, err := p.Append([]byte(fmt.Sprintf("before-%d", i)))
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		first = append(first, h)
	}
	b, err := p.InsertBarrier()
	if err != nil {
		t.Fatalf("insert barrier: %v", err)
	}
	var second []Handle
	for i := 0; i < 10; i++ {
		h, err := p.Append([]byte(fmt.Sprintf("after-%d", i)))
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		second = append(second, h)
	}
	if err := b.Wait(); err != nil {
		t.Fatalf("barrier: %v", err)
	}
	// Once the barrier is durable, everything before it is durable.
	if got := p.DurableLSN(); got < 10 {
		t.Fatalf("durable LSN = %d, want >= 10", got)
	}
	for _, h := range first {
		if err := h.Wait(); err != nil {
			t.Fatalf("pre-barrier LSN %d: %v", h.LSN(), err)
		}
	}
	for _, h := range second {
		if err := h.Wait(); err != nil {
			t.Fatalf("post-barrier LSN %d: %v", h.LSN(), err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	subWG.Wait()
	assertContiguousPrefix(t, "wal", readLogFile(t, path), 20, 20)
	assertContiguousPrefix(t, "published", published, 20, 20)
	if got := p.Outstanding(); got != 0 {
		t.Fatalf("outstanding buffers = %d, want 0", got)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close wal: %v", err)
	}
}
