package checkpoint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

func writeKeys(s *Store, start, end int) {
	for i := start; i < end; i++ {
		s.Put(fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
	}
}

func recoverDir(t *testing.T, dir string) *RecoveredState {
	t.Helper()
	state, err := RecoverDir(dir)
	if err != nil {
		t.Fatalf("RecoverDir: %v", err)
	}
	return state
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore()
	cp, err := NewCheckpointer(store, dir)
	if err != nil {
		t.Fatal(err)
	}
	writeKeys(store, 0, 10_000)
	meta, err := cp.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if meta.NumPages != NumPages {
		t.Fatalf("first checkpoint should cover all %d pages, got %d", NumPages, meta.NumPages)
	}
	state := recoverDir(t, dir)
	for i := 0; i < 10_000; i++ {
		k := fmt.Sprintf("key-%d", i)
		if state.KV[k] != fmt.Sprintf("value-%d", i) {
			t.Fatalf("key %s: got %q", k, state.KV[k])
		}
	}
	if state.Version != store.Version() {
		t.Fatalf("version mismatch: recovered %d, store %d", state.Version, store.Version())
	}
}

func TestIncrementalDelta(t *testing.T) {
	dir := t.TempDir()
	store := NewStore()
	cp, _ := NewCheckpointer(store, dir)
	writeKeys(store, 0, 10_000)
	full, err := cp.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}

	// Modify only a handful of keys; the delta must be small.
	writeKeys(store, 0, 10)
	writeKeys(store, 10_000, 10_005)
	delta, err := cp.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if delta.NumPages >= full.NumPages {
		t.Fatalf("delta pages %d should be < full pages %d", delta.NumPages, full.NumPages)
	}
	if delta.NumPages > 16 {
		t.Fatalf("delta too large: %d pages", delta.NumPages)
	}
	if delta.PrevChain != full.ChainHash {
		t.Fatal("delta not linked to base checkpoint chain")
	}

	state := recoverDir(t, dir)
	if len(state.KV) != 10_005 {
		t.Fatalf("recovered %d keys, want 10005", len(state.KV))
	}
	for i := 0; i < 10_005; i++ {
		k := fmt.Sprintf("key-%d", i)
		if state.KV[k] != fmt.Sprintf("value-%d", i) {
			t.Fatalf("key %s: got %q", k, state.KV[k])
		}
	}
}

func TestCorruptThirdChunk(t *testing.T) {
	dir := t.TempDir()
	store := NewStore()
	cp, _ := NewCheckpointer(store, dir)
	writeKeys(store, 0, 20_000)
	meta, err := cp.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if meta.NumChunks < 3 {
		t.Fatalf("need at least 3 chunks, got %d", meta.NumChunks)
	}

	raw, err := os.ReadFile(meta.Path)
	if err != nil {
		t.Fatal(err)
	}
	const victim = 2 // the 3rd chunk, 0-based
	off, length, err := LocateChunk(raw, victim)
	if err != nil {
		t.Fatal(err)
	}
	raw[off+length/2] ^= 0xFF // flip a byte in the middle of the chunk data

	badDir := t.TempDir()
	badPath := filepath.Join(badDir, filepath.Base(meta.Path))
	if err := os.WriteFile(badPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = Recover([]string{badPath})
	if err == nil {
		t.Fatal("expected corruption to be detected")
	}
	var cerr *CorruptionError
	if !errors.As(err, &cerr) {
		t.Fatalf("expected CorruptionError, got %T: %v", err, err)
	}
	if cerr.ChunkIndex != victim {
		t.Fatalf("reported chunk %d, want %d", cerr.ChunkIndex, victim)
	}
	if len(cerr.PageIDs) == 0 {
		t.Fatal("no affected pages reported")
	}
	for _, id := range cerr.PageIDs {
		if int(id) >= NumPages {
			t.Fatalf("bogus page id %d", id)
		}
	}
	t.Logf("corruption located: %v", cerr)
}

func TestBrokenChainLinkage(t *testing.T) {
	dir := t.TempDir()
	store := NewStore()
	cp, _ := NewCheckpointer(store, dir)
	writeKeys(store, 0, 5_000)
	m1, _ := cp.Checkpoint()
	writeKeys(store, 5_000, 8_000)
	m2, _ := cp.Checkpoint()

	// Applying the delta without its base must break the chain linkage.
	if _, err := Recover([]string{m2.Path}); err == nil {
		t.Fatal("expected chain linkage failure")
	}
	var cerr *CorruptionError
	if _, err := Recover([]string{m2.Path, m1.Path}); !errors.As(err, &cerr) {
		t.Fatalf("expected CorruptionError for swapped order, got %v", err)
	}
	if _, err := Recover([]string{m1.Path, m2.Path}); err != nil {
		t.Fatalf("valid chain rejected: %v", err)
	}
}

func TestTamperedFinalChain(t *testing.T) {
	dir := t.TempDir()
	store := NewStore()
	cp, _ := NewCheckpointer(store, dir)
	writeKeys(store, 0, 1_000)
	meta, _ := cp.Checkpoint()

	raw, _ := os.ReadFile(meta.Path)
	raw[len(raw)-1] ^= 0x01 // corrupt the final chain hash in the trailer
	badPath := filepath.Join(t.TempDir(), "tampered.ckpt")
	os.WriteFile(badPath, raw, 0o644)

	_, err := Recover([]string{badPath})
	var cerr *CorruptionError
	if !errors.As(err, &cerr) || cerr.ChunkIndex != -1 {
		t.Fatalf("expected chain-level CorruptionError, got %v", err)
	}
}

// TestWriteLatencyDuringCheckpoint writes 100k KV pairs while checkpoints
// run concurrently, and asserts write latency jitter stays bounded.
func TestWriteLatencyDuringCheckpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping latency test in short mode")
	}
	dir := t.TempDir()
	store := NewStore()
	cp, _ := NewCheckpointer(store, dir)

	const total = 100_000
	latencies := make([]time.Duration, 0, total)
	var mu sync.Mutex

	stop := make(chan struct{})
	var metas []*Meta
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		next := uint64(20_000)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if store.Version() >= next {
				m, err := cp.Checkpoint()
				if err != nil {
					t.Errorf("checkpoint: %v", err)
					return
				}
				mu.Lock()
				metas = append(metas, m)
				mu.Unlock()
				next += 20_000
			} else {
				time.Sleep(time.Millisecond)
			}
		}
	}()

	for i := 0; i < total; i++ {
		start := time.Now()
		store.Put(fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
		d := time.Since(start)
		mu.Lock()
		latencies = append(latencies, d)
		mu.Unlock()
	}
	close(stop)
	wg.Wait()

	final, err := cp.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	metas = append(metas, final)

	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p50 := sorted[len(sorted)*50/100]
	p99 := sorted[len(sorted)*99/100]
	max := sorted[len(sorted)-1]
	t.Logf("writes=%d checkpoints=%d p50=%v p99=%v max=%v", total, len(metas), p50, p99, max)

	if p99 > 50*time.Millisecond {
		t.Fatalf("p99 write latency %v exceeds 50ms bound", p99)
	}
	if max > 500*time.Millisecond {
		t.Fatalf("max write latency %v exceeds 500ms bound", max)
	}

	// The checkpoint chain must rebuild the exact final state.
	var paths []string
	for _, m := range metas {
		paths = append(paths, m.Path)
	}
	state, err := Recover(paths)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if len(state.KV) != total {
		t.Fatalf("recovered %d keys, want %d", len(state.KV), total)
	}
	for i := 0; i < total; i += 997 { // spot check
		k := fmt.Sprintf("key-%d", i)
		if state.KV[k] != fmt.Sprintf("value-%d", i) {
			t.Fatalf("key %s mismatch", k)
		}
	}
}
