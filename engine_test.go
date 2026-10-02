package stcs

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
)

const testValueSize = 1024

func makeValue(ver uint64) []byte {
	b := make([]byte, testValueSize)
	binary.BigEndian.PutUint64(b, ver)
	for i := 8; i < len(b); i++ {
		b[i] = byte(ver)
	}
	return b
}

func valueVersion(v []byte) uint64 { return binary.BigEndian.Uint64(v[:8]) }

// TestSizeTieredCompactionAcceptance exercises the full acceptance path:
// sustained high-frequency writes flush ~10 MiB SSTables; once 4 of them
// accumulate in one size tier a full multi-way merge produces a single
// ~40 MiB table; repeatedly updated keys keep only their newest version;
// and write amplification stays within the theoretical single-tier bound.
func TestSizeTieredCompactionAcceptance(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir, Options{MemtableLimit: 10 << 20, TierThreshold: 4})
	if err != nil {
		t.Fatal(err)
	}

	const hotKeys = 500
	hotVersions := make(map[string]uint64, hotKeys)
	var events []CompactEvent
	eng.Compactor().OnCompact = func(ev CompactEvent) { events = append(events, ev) }

	// Sustained writes: mostly unique keys, plus repeated updates to a
	// hot key set so duplicate versions pile up across tables.
	i := 0
	for eng.Stats().Compactions == 0 {
		ver := uint64(i + 1)
		key := fmt.Sprintf("user%010d", i)
		if err := eng.Put([]byte(key), makeValue(ver)); err != nil {
			t.Fatal(err)
		}
		if i%20 == 0 {
			hk := fmt.Sprintf("hot%06d", (i/20)%hotKeys)
			if err := eng.Put([]byte(hk), makeValue(ver)); err != nil {
				t.Fatal(err)
			}
			hotVersions[hk] = ver
		}
		i++
	}
	stats := eng.Stats()
	t.Logf("puts=%d flushes=%d user=%.1fMiB flushed=%.1fMiB compacted(in=%.1fMiB out=%.1fMiB)",
		i, stats.FlushCount,
		mib(stats.UserBytes), mib(stats.FlushBytes),
		mib(stats.CompactReadBytes), mib(stats.CompactWriteBytes))

	// Exactly one compaction: 4 x ~10MiB tables merged into 1 x ~40MiB.
	if stats.FlushCount != 4 {
		t.Fatalf("flushes = %d, want 4", stats.FlushCount)
	}
	if stats.Compactions != 1 {
		t.Fatalf("compactions = %d, want 1", stats.Compactions)
	}
	if stats.MaxLiveTables != 4 {
		t.Fatalf("max live tables = %d, want 4", stats.MaxLiveTables)
	}
	if stats.LiveTables != 1 {
		t.Fatalf("live tables = %d, want 1 after merge", stats.LiveTables)
	}
	if len(events) != 1 || len(events[0].Inputs) != 4 {
		t.Fatalf("unexpected compaction events: %+v", events)
	}
	merged := eng.Compactor().Tables()[0]
	if merged.Size < 30<<20 || merged.Size > 42<<20 {
		t.Fatalf("merged size = %.1fMiB, want ~40MiB", mib(merged.Size))
	}
	if stats.DedupedEntries == 0 {
		t.Fatal("expected stale duplicate versions to be dropped")
	}

	// Write amplification: single-tier STCS rewrites every byte at most
	// once, so the theoretical bound is 2.0.
	if stats.WriteAmplification > 2.0 {
		t.Fatalf("write amplification %.3f exceeds theoretical bound 2.0", stats.WriteAmplification)
	}
	if stats.WriteAmplification < 1.0 {
		t.Fatalf("write amplification %.3f below 1.0 is impossible", stats.WriteAmplification)
	}
	// The merge read everything it wrote (plus the dropped duplicates).
	if stats.CompactionReadAmp < 1.0 || stats.CompactionReadAmp > 1.2 {
		t.Fatalf("compaction read amplification %.3f outside [1.0, 1.2]", stats.CompactionReadAmp)
	}

	// The merged table holds each key exactly once, in sorted order, and
	// every hot key carries its newest version.
	it, err := merged.Iterator()
	if err != nil {
		t.Fatal(err)
	}
	var prev []byte
	var seen int
	for it.Next() {
		e := it.Entry()
		if prev != nil && bytes.Compare(prev, e.Key) >= 0 {
			t.Fatalf("duplicate or out-of-order key %q after %q", e.Key, prev)
		}
		prev = append(prev[:0], e.Key...)
		if len(e.Key) >= 3 && string(e.Key[:3]) == "hot" {
			if got := valueVersion(e.Value); got != hotVersions[string(e.Key)] {
				t.Fatalf("hot key %s has version %d, want newest %d",
					e.Key, got, hotVersions[string(e.Key)])
			}
		}
		seen++
	}
	if err := it.Close(); err != nil {
		t.Fatal(err)
	}
	if uint64(seen) != merged.Count {
		t.Fatalf("iterated %d entries, meta says %d", seen, merged.Count)
	}

	// Point lookups agree with the merged state.
	for hk, ver := range hotVersions {
		v, ok := eng.Get([]byte(hk))
		if !ok {
			t.Fatalf("hot key %s missing", hk)
		}
		if got := valueVersion(v); got != ver {
			t.Fatalf("Get(%s) version = %d, want %d", hk, got, ver)
		}
	}
	lastUser := fmt.Sprintf("user%010d", i-1)
	if v, ok := eng.Get([]byte(lastUser)); !ok || valueVersion(v) != uint64(i) {
		t.Fatalf("Get(%s) wrong: ok=%v", lastUser, ok)
	}
}

// TestConcurrentSustainedWrites hammers the engine from several goroutines
// and verifies no version is lost.
func TestConcurrentSustainedWrites(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir, Options{MemtableLimit: 256 << 10, TierThreshold: 4})
	if err != nil {
		t.Fatal(err)
	}
	const writers = 4
	const perWriter = 5000
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				key := fmt.Sprintf("g%d-key%08d", g, i)
				if err := eng.Put([]byte(key), makeValue(uint64(i))); err != nil {
					t.Error(err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if err := eng.Flush(); err != nil {
		t.Fatal(err)
	}
	for g := 0; g < writers; g++ {
		for i := 0; i < perWriter; i += 997 {
			key := fmt.Sprintf("g%d-key%08d", g, i)
			v, ok := eng.Get([]byte(key))
			if !ok {
				t.Fatalf("%s missing", key)
			}
			if got := valueVersion(v); got != uint64(i) {
				t.Fatalf("%s version = %d, want %d", key, got, i)
			}
		}
	}
	stats := eng.Stats()
	if stats.WriteAmplification <= 1.0 {
		t.Fatalf("expected some compaction rewrite, WA = %.3f", stats.WriteAmplification)
	}
	t.Logf("compactions=%d liveTables=%d WA=%.3f", stats.Compactions, stats.LiveTables, stats.WriteAmplification)
}

func mib(b int64) float64 { return float64(b) / (1 << 20) }
