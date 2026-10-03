package lscheckpoint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func writeKeys(sm *StateMachine, start, end int) {
	for i := start; i < end; i++ {
		sm.Put(fmt.Sprintf("key-%08d", i), []byte(fmt.Sprintf("value-%08d", i)))
	}
}

func verifyKeys(t *testing.T, sm *StateMachine, start, end int) {
	t.Helper()
	for i := start; i < end; i++ {
		v, ok := sm.Get(fmt.Sprintf("key-%08d", i))
		if !ok {
			t.Fatalf("missing key-%08d", i)
		}
		if string(v) != fmt.Sprintf("value-%08d", i) {
			t.Fatalf("key-%08d: got %q", i, v)
		}
	}
}

// TestFullCheckpointRestore: snapshot -> checkpoint -> restore -> data equal.
func TestFullCheckpointRestore(t *testing.T) {
	dir := t.TempDir()
	sm := NewStateMachine()
	writeKeys(sm, 0, 20000)

	m, err := NewCheckpointer(dir).Checkpoint(sm.Snapshot(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Chunks) != NumPages/PagesPerChunk {
		t.Fatalf("expected %d chunks, got %d", NumPages/PagesPerChunk, len(m.Chunks))
	}

	restored, err := Restore(filepath.Join(dir, fmt.Sprintf("ckpt-%06d", m.Seq)))
	if err != nil {
		t.Fatal(err)
	}
	if restored.Len() != sm.Len() {
		t.Fatalf("len mismatch: %d vs %d", restored.Len(), sm.Len())
	}
	verifyKeys(t, restored, 0, 20000)
}

// TestIncrementalCheckpoint: diff package only contains changed pages and
// restores the full state when layered on its base.
func TestIncrementalCheckpoint(t *testing.T) {
	dir := t.TempDir()
	sm := NewStateMachine()
	writeKeys(sm, 0, 20000)
	cp := NewCheckpointer(dir)

	full, err := cp.Checkpoint(sm.Snapshot(), false)
	if err != nil {
		t.Fatal(err)
	}

	// Touch only a few keys -> only their pages should be re-written.
	for i := 0; i < 50; i++ {
		sm.Put(fmt.Sprintf("key-%08d", i*100), []byte("updated"))
	}
	incr, err := cp.Checkpoint(sm.Snapshot(), true)
	if err != nil {
		t.Fatal(err)
	}
	if incr.Base == "" {
		t.Fatal("incremental checkpoint must reference a base")
	}
	changedPages := 0
	for _, c := range incr.Chunks {
		changedPages += len(c.Pages)
	}
	if changedPages == 0 || changedPages >= NumPages {
		t.Fatalf("incremental diff should rewrite only some pages, got %d", changedPages)
	}
	t.Logf("incremental diff rewrote %d/%d pages", changedPages, NumPages)

	restored, err := Restore(filepath.Join(dir, fmt.Sprintf("ckpt-%06d", incr.Seq)))
	if err != nil {
		t.Fatal(err)
	}
	if restored.Len() != 20000 {
		t.Fatalf("restored len = %d", restored.Len())
	}
	for i := 0; i < 50; i++ {
		v, ok := restored.Get(fmt.Sprintf("key-%08d", i*100))
		if !ok || string(v) != "updated" {
			t.Fatalf("updated key-%08d missing: %q", i*100, v)
		}
	}
	// Untouched keys must come from the base checkpoint.
	verifyKeys(t, restored, 5000, 6000)
	_ = full
}

// TestCheckpointUnderWriteLoad: trigger a checkpoint while 100k KV pairs
// are being written continuously; write latency jitter must stay bounded.
func TestCheckpointUnderWriteLoad(t *testing.T) {
	const total = 100000
	const checkpointAt = total / 2

	dir := t.TempDir()
	sm := NewStateMachine()
	cp := NewCheckpointer(dir)

	lat := make([]int64, total) // per-write latency in ns
	var written atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			start := time.Now()
			sm.Put(fmt.Sprintf("key-%08d", i), []byte(fmt.Sprintf("value-%08d", i)))
			lat[i] = time.Since(start).Nanoseconds()
			written.Store(int64(i + 1))
		}
	}()

	// Wait until half the writes are in, then checkpoint a live snapshot.
	for written.Load() < checkpointAt {
		time.Sleep(time.Millisecond)
	}
	snap := sm.Snapshot()
	m, err := cp.Checkpoint(snap, false)
	if err != nil {
		t.Fatal(err)
	}
	ckptStart := int64(m.Seq)
	ckptEnd := written.Load()
	wg.Wait()

	if m.Seq < checkpointAt {
		t.Fatalf("snapshot seq = %d, want >= %d", m.Seq, checkpointAt)
	}

	stats := func(lo, hi int64) (avg, max time.Duration) {
		var sum, mx int64
		for i := lo; i < hi; i++ {
			sum += lat[i]
			if lat[i] > mx {
				mx = lat[i]
			}
		}
		return time.Duration(sum / (hi - lo)), time.Duration(mx)
	}
	percentile := func(lo, hi int64, q float64) time.Duration {
		w := make([]int64, hi-lo)
		copy(w, lat[lo:hi])
		sort.Slice(w, func(i, j int) bool { return w[i] < w[j] })
		return time.Duration(w[int(q*float64(len(w)-1))])
	}
	baseAvg, _ := stats(1000, 40000)            // steady state before checkpoint
	durAvg, durMax := stats(ckptStart, ckptEnd) // writes during checkpoint
	p999 := percentile(ckptStart, ckptEnd, 0.999)
	t.Logf("baseline avg=%v, during-checkpoint avg=%v p99.9=%v max=%v (window=%d writes)",
		baseAvg, durAvg, p999, durMax, ckptEnd-ckptStart)

	if durAvg > 10*baseAvg {
		t.Fatalf("latency jitter out of control: during avg %v > 10x baseline %v", durAvg, baseAvg)
	}
	if p999 > 50*time.Millisecond {
		t.Fatalf("latency jitter out of control: p99.9 %v > 50ms", p999)
	}

	// The checkpoint itself must be restorable and consistent.
	restored, err := Restore(filepath.Join(dir, fmt.Sprintf("ckpt-%06d", m.Seq)))
	if err != nil {
		t.Fatal(err)
	}
	verifyKeys(t, restored, 0, int(m.Seq))
}

// TestCorruptThirdChunk: corrupt the 3rd chunk file of a checkpoint;
// restore must pinpoint chunk index 2 and reject the data.
func TestCorruptThirdChunk(t *testing.T) {
	dir := t.TempDir()
	sm := NewStateMachine()
	writeKeys(sm, 0, 20000)
	m, err := NewCheckpointer(dir).Checkpoint(sm.Snapshot(), false)
	if err != nil {
		t.Fatal(err)
	}
	ckptDir := filepath.Join(dir, fmt.Sprintf("ckpt-%06d", m.Seq))

	const victim = 2 // the 3rd chunk (0-based index 2)
	path := filepath.Join(ckptDir, fmt.Sprintf("chunk-%06d.bin", victim))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body[len(body)/2] ^= 0xFF // flip bits in the middle of the chunk
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = Restore(ckptDir)
	if err == nil {
		t.Fatal("restore must reject corrupted checkpoint")
	}
	var cerr *CorruptionError
	if !errors.As(err, &cerr) {
		t.Fatalf("expected CorruptionError, got %T: %v", err, err)
	}
	if cerr.ChunkIndex != victim {
		t.Fatalf("corruption located at chunk %d, want %d", cerr.ChunkIndex, victim)
	}
	if cerr.Expected == cerr.Actual {
		t.Fatal("expected/actual hashes must differ")
	}
	t.Logf("corruption precisely located: %v", err)

	// Error message must name the bad chunk for operators.
	if !strings.Contains(err.Error(), "chunk 2") {
		t.Fatalf("error should name chunk index: %v", err)
	}
}
