package unwind

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// faultReader is an io.ReaderAt that serves only the pages present in
// pages, fails everything else, counts underlying reads per page, and can
// be gated to coordinate concurrent tests.
type faultReader struct {
	pageSize int64
	pages    map[int64][]byte
	gate     <-chan struct{} // if non-nil, every ReadAt waits until it closes

	mu    sync.Mutex
	calls map[int64]int
}

func newFaultReader(pageSize int64) *faultReader {
	return &faultReader{
		pageSize: pageSize,
		pages:    make(map[int64][]byte),
		calls:    make(map[int64]int),
	}
}

func (f *faultReader) setWord(addr uint64, v uint64) {
	idx := int64(addr / uint64(f.pageSize))
	off := int(addr % uint64(f.pageSize))
	p, ok := f.pages[idx]
	if !ok {
		p = make([]byte, f.pageSize)
		f.pages[idx] = p
	}
	putle64(p[off:], v)
}

func (f *faultReader) ReadAt(p []byte, off int64) (int, error) {
	idx := off / f.pageSize
	f.mu.Lock()
	f.calls[idx]++
	f.mu.Unlock()
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	page, ok := f.pages[idx]
	f.mu.Unlock()
	if !ok {
		return 0, errors.New("page unmapped")
	}
	return copy(p, page), nil
}

func (f *faultReader) pageCalls(idx int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[idx]
}

func TestConcurrentSamePageReadsAreCoalesced(t *testing.T) {
	fr := newFaultReader(64)
	fr.setWord(0, 0xdeadbeef)
	gate := make(chan struct{})
	fr.gate = gate
	mem := NewMemory(fr, 64)

	const readers = 8
	var wg sync.WaitGroup
	vals := make([]uint64, readers)
	errs := make([]error, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vals[i], errs[i] = mem.Uint64(context.Background(), 0)
		}(i)
	}
	// Let every goroutine reach the fetch, then release the single read.
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()

	for i := 0; i < readers; i++ {
		if errs[i] != nil {
			t.Fatalf("reader %d: %v", i, errs[i])
		}
		if vals[i] != 0xdeadbeef {
			t.Fatalf("reader %d: got %#x", i, vals[i])
		}
	}
	if got := fr.pageCalls(0); got != 1 {
		t.Fatalf("underlying ReadAt called %d times, want 1", got)
	}
}

func TestCancelOneConsumerLeavesOthersIntact(t *testing.T) {
	fr := newFaultReader(64)
	fr.setWord(0, 0x1234)
	gate := make(chan struct{})
	fr.gate = gate
	mem := NewMemory(fr, 64)

	ctx, cancel := context.WithCancel(context.Background())
	cancelledErr := make(chan error, 1)
	go func() {
		_, err := mem.Uint64(ctx, 0)
		cancelledErr <- err
	}()

	survivorDone := make(chan struct{})
	var survivorVal uint64
	var survivorErr error
	go func() {
		defer close(survivorDone)
		survivorVal, survivorErr = mem.Uint64(context.Background(), 0)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-cancelledErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled consumer: got %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled consumer did not return")
	}
	select {
	case <-survivorDone:
		t.Fatal("surviving consumer returned before the shared fetch completed")
	default:
	}

	close(gate)
	<-survivorDone
	if survivorErr != nil {
		t.Fatalf("surviving consumer: %v", survivorErr)
	}
	if survivorVal != 0x1234 {
		t.Fatalf("surviving consumer got %#x", survivorVal)
	}
	if got := fr.pageCalls(0); got != 1 {
		t.Fatalf("underlying ReadAt called %d times, want 1", got)
	}
}

func TestReadSpansPages(t *testing.T) {
	fr := newFaultReader(8)
	fr.setWord(0, 0x1122334455667788)
	fr.setWord(8, 0x99aabbccddeeff00)
	mem := NewMemory(fr, 8)

	b, err := mem.Read(context.Background(), 4, 8)
	if err != nil {
		t.Fatal(err)
	}
	if got := le64(b); got != 0xddeeff0011223344 {
		t.Fatalf("cross-page read = %#x", got)
	}
}

// frame-pointer chain rules: FP' = *(FP), PC' = *(FP+8), SP' = FP+16.
// Register layout: 0=PC, 1=SP, 2=FP.
const (
	regPC = 0
	regSP = 1
	regFP = 2
)

func fpChainFrame() Frame {
	return Frame{
		regPC: Deref{Reg: regFP, Off: 8},
		regSP: AddrOf{Reg: regFP, Off: 16},
		regFP: Deref{Reg: regFP, Off: 0},
	}
}

func TestUnwindFramePointerChain(t *testing.T) {
	fr := newFaultReader(0x1000)
	// frame 1 at FP=0x4000 -> caller FP=0x5000, PC=0xaaaa
	fr.setWord(0x4000, 0x5000)
	fr.setWord(0x4008, 0xaaaa)
	// frame 2 at FP=0x5000 -> caller FP=0x6000, PC=0xbbbb
	fr.setWord(0x5000, 0x6000)
	fr.setWord(0x5008, 0xbbbb)
	mem := NewMemory(fr, 0x1000)

	start := Regs{0x9999, 0x3000, 0x4000}
	steps := []Frame{fpChainFrame(), fpChainFrame()}
	res := NewUnwinder(mem).Unwind(context.Background(), start, steps)
	if res.Err != nil {
		t.Fatalf("unwind: %v", res.Err)
	}
	want := []Regs{
		{0x9999, 0x3000, 0x4000},
		{0xaaaa, 0x4010, 0x5000},
		{0xbbbb, 0x5010, 0x6000},
	}
	if len(res.Frames) != len(want) {
		t.Fatalf("got %d frames, want %d", len(res.Frames), len(want))
	}
	for i := range want {
		if !res.Frames[i].Equal(want[i]) {
			t.Fatalf("frame %d = %v, want %v", i, res.Frames[i], want[i])
		}
	}
}

func TestUnwindMissingPageKeepsFramesAndExactGap(t *testing.T) {
	fr := newFaultReader(0x1000)
	// frame 1 at FP=0x4000 -> caller FP=0x9000 (page 0x9000 unmapped)
	fr.setWord(0x4000, 0x9000)
	fr.setWord(0x4008, 0xaaaa)
	mem := NewMemory(fr, 0x1000)

	start := Regs{0x9999, 0x3000, 0x4000}
	steps := []Frame{fpChainFrame(), fpChainFrame()}
	res := NewUnwinder(mem).Unwind(context.Background(), start, steps)

	var ge *GapError
	if !errors.As(res.Err, &ge) {
		t.Fatalf("Err = %v, want *GapError", res.Err)
	}
	if res.Gap == nil {
		t.Fatal("Gap is nil")
	}
	if res.Gap.Addr != 0x9000 || res.Gap.Len != 0x1000 {
		t.Fatalf("Gap = %+v, want {0x9000 0x1000}", *res.Gap)
	}
	want := []Regs{
		{0x9999, 0x3000, 0x4000},
		{0xaaaa, 0x4010, 0x9000},
	}
	if len(res.Frames) != len(want) {
		t.Fatalf("got %d frames, want %d", len(res.Frames), len(want))
	}
	for i := range want {
		if !res.Frames[i].Equal(want[i]) {
			t.Fatalf("frame %d = %v, want %v", i, res.Frames[i], want[i])
		}
	}
}

func TestUnwindNoProgressSelfCycle(t *testing.T) {
	fr := newFaultReader(0x1000)
	// FP points at itself and PC reloads itself: next state == current.
	fr.setWord(0x4000, 0x4000)
	fr.setWord(0x4008, 0x9999)
	mem := NewMemory(fr, 0x1000)

	start := Regs{0x9999, 0x4010, 0x4000}
	res := NewUnwinder(mem).Unwind(context.Background(), start, []Frame{fpChainFrame(), fpChainFrame()})
	if !errors.Is(res.Err, ErrNoProgress) {
		t.Fatalf("Err = %v, want ErrNoProgress", res.Err)
	}
	if len(res.Frames) != 1 || !res.Frames[0].Equal(start) {
		t.Fatalf("Frames = %v, want only the start frame", res.Frames)
	}
}

func TestRulesEvaluateAgainstOldRegisterSet(t *testing.T) {
	// Swap rule: new[0] = old[1], new[1] = old[0]. With simultaneous
	// evaluation the values exchange; sequential evaluation would corrupt
	// the swap. Applying it twice returns to the start state, which the
	// unwinder must flag as a no-progress cycle.
	mem := NewMemory(newFaultReader(64), 64)
	swap := Frame{Reg(1), Reg(0)}

	start := Regs{0x1111, 0x2222}
	res := NewUnwinder(mem).Unwind(context.Background(), start, []Frame{swap, swap})
	if !errors.Is(res.Err, ErrNoProgress) {
		t.Fatalf("Err = %v, want ErrNoProgress", res.Err)
	}
	if len(res.Frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(res.Frames))
	}
	if !res.Frames[1].Equal(Regs{0x2222, 0x1111}) {
		t.Fatalf("swapped frame = %v, want [0x2222 0x1111]", res.Frames[1])
	}
}

func TestUnwindCancelledContext(t *testing.T) {
	fr := newFaultReader(0x1000)
	fr.setWord(0x4000, 0x5000)
	fr.setWord(0x4008, 0xaaaa)
	mem := NewMemory(fr, 0x1000)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := NewUnwinder(mem).Unwind(ctx, Regs{0x9999, 0x3000, 0x4000}, []Frame{fpChainFrame()})
	if !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("Err = %v, want context.Canceled", res.Err)
	}
	if len(res.Frames) != 1 {
		t.Fatalf("Frames = %v, want only the start frame", res.Frames)
	}
}
