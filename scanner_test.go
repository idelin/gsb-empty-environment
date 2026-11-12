package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeMemory simulates a live process image with a single monotonic
// generation counter shared by commit and per-page write generations.
type fakeMemory struct {
	mu      sync.Mutex
	counter uint64
	pages   map[uint64]*fakePage
	// history, when non-nil, records the digest tree root after every
	// mutation, keyed by the resulting commit generation.
	history map[uint64][sha256.Size]byte
}

type fakePage struct {
	data     []byte
	writeGen uint64
}

func newFakeMemory() *fakeMemory {
	return &fakeMemory{pages: make(map[uint64]*fakePage)}
}

func (m *fakeMemory) CommitGen() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counter
}

func (m *fakeMemory) ListPages() ([]uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]uint64, 0, len(m.pages))
	for addr := range m.pages {
		out = append(out, addr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (m *fakeMemory) ReadPage(addr uint64) ([]byte, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pages[addr]
	if !ok {
		return nil, 0, ErrPageUnmapped
	}
	return append([]byte(nil), p.data...), p.writeGen, nil
}

func (m *fakeMemory) PageWriteGen(addr uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pages[addr]
	if !ok {
		return 0, ErrPageUnmapped
	}
	return p.writeGen, nil
}

func (m *fakeMemory) Write(addr uint64, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counter++
	p, ok := m.pages[addr]
	if !ok {
		p = &fakePage{}
		m.pages[addr] = p
	}
	p.data = append([]byte(nil), data...)
	p.writeGen = m.counter
	m.recordLocked()
}

func (m *fakeMemory) Map(addr uint64, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counter++
	m.pages[addr] = &fakePage{data: append([]byte(nil), data...), writeGen: m.counter}
	m.recordLocked()
}

func (m *fakeMemory) Unmap(addr uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counter++
	delete(m.pages, addr)
	m.recordLocked()
}

func (m *fakeMemory) recordLocked() {
	if m.history == nil {
		return
	}
	order := make([]uint64, 0, len(m.pages))
	states := make(map[uint64]pageState, len(m.pages))
	for addr, p := range m.pages {
		order = append(order, addr)
		states[addr] = pageState{content: p.data, writeGen: p.writeGen}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	m.history[m.counter] = merkleRoot(order, states)
}

const (
	pageA = 0x1000
	pageB = 0x2000
	pageC = 0x3000
	pageD = 0x4000
)

func fill(b byte, n int) []byte { return bytes.Repeat([]byte{b}, n) }

func newThreePageMemory() *fakeMemory {
	m := newFakeMemory()
	m.Map(pageA, fill(0xAA, 4096))
	m.Map(pageB, fill(0xBB, 4096))
	m.Map(pageC, fill(0xCC, 4096))
	return m
}

type scanOutcome struct {
	res Result
	err error
}

func scanAsync(s *Scanner) chan scanOutcome {
	ch := make(chan scanOutcome, 1)
	go func() {
		r, err := s.Scan()
		ch <- scanOutcome{r, err}
	}()
	return ch
}

func awaitScan(t *testing.T, ch chan scanOutcome) scanOutcome {
	t.Helper()
	select {
	case oc := <-ch:
		return oc
	case <-time.After(10 * time.Second):
		t.Fatal("scan did not finish in time")
		return scanOutcome{}
	}
}

func mustCommitted(t *testing.T, oc scanOutcome) Result {
	t.Helper()
	if oc.err != nil {
		t.Fatalf("scan failed: %v", oc.err)
	}
	if !oc.res.Committed {
		t.Fatal("scan did not commit a digest tree")
	}
	return oc.res
}

func TestStaticSnapshotCommits(t *testing.T) {
	mem := newThreePageMemory()
	res, err := NewScanner(mem, Options{}).Scan()
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if !res.Committed {
		t.Fatal("expected committed digest tree")
	}
	if res.Pages != 3 || res.Rounds != 0 || res.PagesReRead != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.CommitGen != mem.CommitGen() {
		t.Fatalf("commit gen %d, want %d", res.CommitGen, mem.CommitGen())
	}

	// Pin the digest tree format: leaves are sha256(addr|len|content),
	// odd nodes duplicate, parents are sha256(left|right).
	leaf := func(addr uint64, content []byte) [sha256.Size]byte {
		h := sha256.New()
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], addr)
		h.Write(buf[:])
		binary.BigEndian.PutUint64(buf[:], uint64(len(content)))
		h.Write(buf[:])
		h.Write(content)
		var sum [sha256.Size]byte
		h.Sum(sum[:0])
		return sum
	}
	la, lb, lc := leaf(pageA, fill(0xAA, 4096)), leaf(pageB, fill(0xBB, 4096)), leaf(pageC, fill(0xCC, 4096))
	ab := sha256.Sum256(append(append([]byte{}, la[:]...), lb[:]...))
	cc := sha256.Sum256(append(append([]byte{}, lc[:]...), lc[:]...))
	want := sha256.Sum256(append(append([]byte{}, ab[:]...), cc[:]...))
	if res.Root != want {
		t.Fatal("root does not match the pinned digest tree format")
	}

	// A content change must change the committed root.
	mem.Write(pageB, fill(0xBD, 4096))
	res2, err := NewScanner(mem, Options{}).Scan()
	if err != nil {
		t.Fatalf("rescan failed: %v", err)
	}
	if res2.Root == res.Root {
		t.Fatal("root unchanged after content change")
	}
}

func TestRepeatedWritesRepairOnlyAffectedPage(t *testing.T) {
	mem := newThreePageMemory()
	src := NewPausableSource(mem)
	gate := NewGate()
	// Pause right before the last page of the first pass is read, so page B
	// has already been snapshotted when the writes land.
	src.SetHook(func(op ReadOp, addr uint64) {
		if op == OpReadPage && addr == pageC {
			gate.Hold()
		}
	})
	gate.Pause()
	out := scanAsync(NewScanner(src, Options{RepairBudget: 8}))
	gate.WaitEntered()
	for i := 0; i < 5; i++ {
		mem.Write(pageB, fill(byte(0xB0+i), 4096))
	}
	gate.Resume()

	res := mustCommitted(t, awaitScan(t, out))
	if res.PagesReRead != 1 {
		t.Fatalf("PagesReRead = %d, want 1", res.PagesReRead)
	}
	if got := src.ReadCount(pageB); got != 2 {
		t.Fatalf("page B read %d times, want 2", got)
	}
	if got := src.ReadCount(pageA); got != 1 {
		t.Fatalf("page A read %d times, want 1 (unaffected)", got)
	}
	if got := src.ReadCount(pageC); got != 1 {
		t.Fatalf("page C read %d times, want 1 (unaffected)", got)
	}
	if res.CommitGen != mem.CommitGen() {
		t.Fatalf("commit gen %d, want %d", res.CommitGen, mem.CommitGen())
	}
	// The committed tree must equal a fresh scan of the now-static image.
	again, err := NewScanner(mem, Options{}).Scan()
	if err != nil {
		t.Fatalf("rescan failed: %v", err)
	}
	if res.Root != again.Root {
		t.Fatal("committed root does not match the final image")
	}
}

func TestPageRemovalMidScan(t *testing.T) {
	t.Run("already read page", func(t *testing.T) {
		mem := newThreePageMemory()
		src := NewPausableSource(mem)
		gate := NewGate()
		src.SetHook(func(op ReadOp, addr uint64) {
			if op == OpReadPage && addr == pageC {
				gate.Hold()
			}
		})
		gate.Pause()
		out := scanAsync(NewScanner(src, Options{RepairBudget: 8}))
		gate.WaitEntered()
		mem.Unmap(pageB)
		gate.Resume()

		res := mustCommitted(t, awaitScan(t, out))
		if res.Pages != 2 {
			t.Fatalf("Pages = %d, want 2", res.Pages)
		}
		again, err := NewScanner(mem, Options{}).Scan()
		if err != nil {
			t.Fatalf("rescan failed: %v", err)
		}
		if res.Root != again.Root {
			t.Fatal("committed root does not match the image after removal")
		}
	})

	t.Run("not yet read page", func(t *testing.T) {
		mem := newThreePageMemory()
		src := NewPausableSource(mem)
		gate := NewGate()
		src.SetHook(func(op ReadOp, addr uint64) {
			if op == OpReadPage && addr == pageB {
				gate.Hold()
			}
		})
		gate.Pause()
		out := scanAsync(NewScanner(src, Options{RepairBudget: 8}))
		gate.WaitEntered()
		mem.Unmap(pageC)
		gate.Resume()

		res := mustCommitted(t, awaitScan(t, out))
		if res.Pages != 2 {
			t.Fatalf("Pages = %d, want 2", res.Pages)
		}
		if got := src.ReadCount(pageC); got != 0 {
			t.Fatalf("removed page C read %d times, want 0", got)
		}
		again, err := NewScanner(mem, Options{}).Scan()
		if err != nil {
			t.Fatalf("rescan failed: %v", err)
		}
		if res.Root != again.Root {
			t.Fatal("committed root does not match the image after removal")
		}
	})

	t.Run("page added", func(t *testing.T) {
		mem := newThreePageMemory()
		src := NewPausableSource(mem)
		gate := NewGate()
		src.SetHook(func(op ReadOp, addr uint64) {
			if op == OpReadPage && addr == pageC {
				gate.Hold()
			}
		})
		gate.Pause()
		out := scanAsync(NewScanner(src, Options{RepairBudget: 8}))
		gate.WaitEntered()
		mem.Map(pageD, fill(0xDD, 4096))
		gate.Resume()

		res := mustCommitted(t, awaitScan(t, out))
		if res.Pages != 4 {
			t.Fatalf("Pages = %d, want 4", res.Pages)
		}
		if res.PagesReRead != 1 {
			t.Fatalf("PagesReRead = %d, want 1 (only the new page)", res.PagesReRead)
		}
		again, err := NewScanner(mem, Options{}).Scan()
		if err != nil {
			t.Fatalf("rescan failed: %v", err)
		}
		if res.Root != again.Root {
			t.Fatal("committed root does not match the image after addition")
		}
	})
}

func TestRemapSameContentStillReRead(t *testing.T) {
	mem := newThreePageMemory()
	src := NewPausableSource(mem)
	gate := NewGate()
	src.SetHook(func(op ReadOp, addr uint64) {
		if op == OpReadPage && addr == pageC {
			gate.Hold()
		}
	})
	gate.Pause()
	out := scanAsync(NewScanner(src, Options{RepairBudget: 8}))
	gate.WaitEntered()
	genBefore := mem.CommitGen()
	// Remap page B with byte-identical content: the content hash would not
	// change, but the mapping gets a fresh write generation.
	mem.Unmap(pageB)
	mem.Map(pageB, fill(0xBB, 4096))
	gate.Resume()

	res := mustCommitted(t, awaitScan(t, out))
	if res.CommitGen == genBefore {
		t.Fatal("committed a stale generation after remap")
	}
	if res.CommitGen != mem.CommitGen() {
		t.Fatalf("commit gen %d, want %d", res.CommitGen, mem.CommitGen())
	}
	// Same content must not be trusted across generations: the remapped
	// page has to be re-read even though its bytes are identical.
	if got := src.ReadCount(pageB); got != 2 {
		t.Fatalf("remapped page read %d times, want 2", got)
	}
	if res.PagesReRead != 1 {
		t.Fatalf("PagesReRead = %d, want 1", res.PagesReRead)
	}
	again, err := NewScanner(mem, Options{}).Scan()
	if err != nil {
		t.Fatalf("rescan failed: %v", err)
	}
	if res.Root != again.Root {
		t.Fatal("committed root does not match the final image")
	}
}

func TestRepairBudgetExhausted(t *testing.T) {
	mem := newThreePageMemory()
	src := NewPausableSource(mem)
	var n uint64
	// Every access dirties page A, so no quiet window ever exists.
	src.SetHook(func(op ReadOp, addr uint64) {
		n++
		mem.Write(pageA, fill(byte(n), 16))
	})
	res, err := NewScanner(src, Options{RepairBudget: 3, MaxRounds: 8}).Scan()
	if !errors.Is(err, ErrRepairBudgetExhausted) {
		t.Fatalf("err = %v, want ErrRepairBudgetExhausted", err)
	}
	if res.Committed {
		t.Fatal("must not commit a digest tree for an inconsistent image")
	}
	if res.PagesReRead > 3 {
		t.Fatalf("PagesReRead = %d exceeds budget 3", res.PagesReRead)
	}
}

func TestConcurrentWriterEventuallyCommits(t *testing.T) {
	mem := newFakeMemory()
	addrs := make([]uint64, 0, 8)
	for i := 0; i < 8; i++ {
		addr := uint64(0x1000 * (i + 1))
		mem.Map(addr, fill(byte(i), 1024))
		addrs = append(addrs, addr)
	}
	mem.history = make(map[uint64][sha256.Size]byte)

	// The writer dirties pages in a tight loop so the scanner cannot find a
	// quiet generation window until the writer stops.
	data := fill(0x77, 1024)
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(20 * time.Millisecond)
		for i := 0; time.Now().Before(deadline); i++ {
			mem.Write(addrs[i%len(addrs)], data)
			if i == 0 {
				close(started)
			}
		}
	}()
	<-started

	res, err := NewScanner(mem, Options{RepairBudget: 1 << 20, MaxRounds: 1 << 20}).Scan()
	<-done
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if !res.Committed {
		t.Fatal("expected eventual commit once the writer stops")
	}
	// The committed tree must be exactly the image at its commit
	// generation, whether the scanner committed mid-stream in a lucky
	// quiet window or after the writer settled.
	want, ok := mem.history[res.CommitGen]
	if !ok {
		t.Fatalf("commit gen %d not found in image history", res.CommitGen)
	}
	if res.Root != want {
		t.Fatalf("committed root does not match the image at gen %d", res.CommitGen)
	}
}
