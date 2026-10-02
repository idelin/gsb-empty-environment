package xport

import (
	"encoding/binary"
	"errors"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"
)

const (
	acceptRows  = 500_000
	acceptBatch = 5_000 // -> exactly 100 chunks
	acceptMem   = 1 << 20
	peakBudget  = 10 << 20 // 10MB acceptance bound
)

// rowIter lazily generates n changes, simulating a source DB cursor.
func rowIter(n int) ChangeIter {
	i := 0
	return func() (Change, bool) {
		if i >= n {
			return Change{}, false
		}
		val := make([]byte, 32)
		binary.LittleEndian.PutUint64(val, uint64(i)*2654435761)
		ch := Change{Op: OpUpdate, Key: uint64(i), Val: val}
		i++
		return ch, true
	}
}

func heapAlloc() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// Acceptance: a 500k-row transaction is streamed as 100 chunks, integrity
// is verified end to end, and peak heap growth stays under 10MB.
func TestLargeTransactionStreaming(t *testing.T) {
	runtime.GC()
	base := heapAlloc()

	var mu sync.Mutex
	peak := base
	sample := func() {
		if h := heapAlloc(); h > peak {
			peak = h
		}
	}
	stop := make(chan struct{})
	go func() { // background heap sampler
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				mu.Lock()
				sample()
				mu.Unlock()
			}
		}
	}()
	defer close(stop)

	tr := &Transport{
		Threshold: acceptBatch,
		BatchSize: acceptBatch,
		MemLimit:  acceptMem,
		QueueCap:  4,
		SpillDir:  t.TempDir(),
	}

	var received int64
	var lastKey uint64
	st, err := tr.Transfer(42, rowIter(acceptRows), func(c Change) error {
		if c.Key != lastKey {
			t.Fatalf("row order broken: got key %d, want %d", c.Key, lastKey)
		}
		lastKey++
		received++
		if received%1024 == 0 {
			mu.Lock()
			sample()
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	runtime.GC()
	mu.Lock()
	sample()
	mu.Unlock()

	if st.Split.Chunks != 100 {
		t.Fatalf("chunks = %d, want 100", st.Split.Chunks)
	}
	if st.Receive.Rows != acceptRows || received != acceptRows {
		t.Fatalf("rows = %d/%d, want %d", st.Receive.Rows, received, acceptRows)
	}
	if st.Receive.Chunks != 100 {
		t.Fatalf("received chunks = %d, want 100", st.Receive.Chunks)
	}
	if st.SpilledChunks == 0 {
		t.Fatalf("expected disk spill under 1MB budget, got none")
	}
	if st.PeakReserved > acceptMem {
		t.Fatalf("limiter peak %d exceeds budget %d", st.PeakReserved, acceptMem)
	}
	growth := int64(peak) - int64(base)
	t.Logf("chunks=%d spilled=%d(%d bytes) in-mem=%d limiter-peak=%d heap-growth=%.2fMB",
		st.Split.Chunks, st.SpilledChunks, st.SpilledBytes, st.InMemChunks,
		st.PeakReserved, float64(growth)/(1<<20))
	if growth > peakBudget {
		t.Fatalf("peak heap growth %.2fMB exceeds 10MB budget", float64(growth)/(1<<20))
	}
}

func TestSplitterStages(t *testing.T) {
	var stages []Stage
	var last *Chunk
	s := &Splitter{Threshold: 2, BatchSize: 2}
	stats, err := s.Split(7, rowIter(5), func(c *Chunk) error {
		stages = append(stages, c.Stage)
		cp := *c
		last = &cp
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chunks != 3 || stats.Rows != 5 {
		t.Fatalf("stats = %+v, want 3 chunks / 5 rows", stats)
	}
	want := []Stage{StageBegin, StageMiddle, StageEnd}
	for i := range want {
		if stages[i] != want[i] {
			t.Fatalf("stage[%d] = %s, want %s", i, stages[i], want[i])
		}
	}
	if last.TotalChunks != 3 || last.TotalRows != 5 || last.TxChecksum == 0 {
		t.Fatalf("final chunk totals wrong: %+v", last)
	}
}

func TestSplitterSmallTxSingleChunk(t *testing.T) {
	var stages []Stage
	s := &Splitter{Threshold: 100, BatchSize: 100}
	_, err := s.Split(1, rowIter(10), func(c *Chunk) error {
		stages = append(stages, c.Stage)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 1 || stages[0] != StageSingle {
		t.Fatalf("stages = %v, want [SINGLE]", stages)
	}
}

func TestMemLimiterBackpressure(t *testing.T) {
	lim := NewMemLimiter(100)
	lim.Acquire(80)
	if lim.TryAcquire(30) {
		t.Fatal("TryAcquire should fail over budget")
	}
	done := make(chan struct{})
	go func() { lim.Acquire(30); close(done) }()
	select {
	case <-done:
		t.Fatal("Acquire should block while over budget")
	case <-time.After(20 * time.Millisecond):
	}
	lim.Release(50)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Acquire should unblock after Release")
	}
	if lim.Peak() != 80 {
		t.Fatalf("peak = %d, want 80", lim.Peak())
	}
}

func TestSpoolRoundTrip(t *testing.T) {
	sp, err := NewSpool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Remove()
	for i := 0; i < 3; i++ {
		if err := sp.Write(&Chunk{TxID: 9, Seq: i, Stage: StageMiddle, Changes: []Change{{Op: OpInsert, Key: uint64(i), Val: []byte("v")}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sp.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := sp.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := 0; i < 3; i++ {
		c, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		if c.Seq != i || c.TxID != 9 || len(c.Changes) != 1 {
			t.Fatalf("chunk %d corrupted: %+v", i, c)
		}
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF, got %v", err)
	}
}

func TestReassemblerIntegrityFailures(t *testing.T) {
	build := func() []*Chunk {
		var chunks []*Chunk
		s := &Splitter{BatchSize: 2}
		if _, err := s.Split(5, rowIter(4), func(c *Chunk) error { chunks = append(chunks, c); return nil }); err != nil {
			t.Fatal(err)
		}
		return chunks
	}
	sink := func(Change) error { return nil }

	t.Run("tampered checksum", func(t *testing.T) {
		chunks := build()
		chunks[0].Changes[0].Key ^= 0xff // corrupt payload after checksum
		r := &Reassembler{}
		if err := r.Apply(chunks[0], sink); err == nil {
			t.Fatal("want checksum mismatch error")
		}
	})
	t.Run("out of order", func(t *testing.T) {
		chunks := build()
		r := &Reassembler{}
		if err := r.Apply(chunks[1], sink); err == nil {
			t.Fatal("want first-chunk stage error")
		}
	})
	t.Run("truncated", func(t *testing.T) {
		chunks := build()
		r := &Reassembler{}
		if err := r.Apply(chunks[0], sink); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Finish(); err == nil {
			t.Fatal("want truncation error")
		}
	})
	t.Run("tampered totals", func(t *testing.T) {
		chunks := build()
		chunks[len(chunks)-1].TotalRows++
		r := &Reassembler{}
		var err error
		for _, c := range chunks {
			if err = r.Apply(c, sink); err != nil {
				break
			}
		}
		if err == nil {
			t.Fatal("want integrity error on totals")
		}
	})
}

func TestTransportSmallTxNoSpill(t *testing.T) {
	tr := &Transport{Threshold: 1000, BatchSize: 1000, MemLimit: 1 << 20, SpillDir: t.TempDir()}
	var n int
	st, err := tr.Transfer(3, rowIter(500), func(Change) error { n++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if n != 500 || st.Split.Chunks != 1 || st.SpilledChunks != 0 {
		t.Fatalf("stats = %+v rows=%d, want 1 chunk, 0 spills, 500 rows", st, n)
	}
}
