package snapshot

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

func TestFullSnapshotCapture(t *testing.T) {
	s := NewStore()
	for i := 0; i < 100; i++ {
		s.Set(fmt.Sprintf("k%03d", i), uint64(i))
	}
	snap := NewSnapshotter(s).Full()
	if snap.Kind != KindFull {
		t.Fatalf("kind = %v, want full", snap.Kind)
	}
	if len(snap.Entries) != 100 {
		t.Fatalf("entries = %d, want 100", len(snap.Entries))
	}
	if snap.Version != 100 {
		t.Fatalf("version = %d, want 100", snap.Version)
	}
	if snap.Seq != 1 {
		t.Fatalf("seq = %d, want 1", snap.Seq)
	}
}

func TestIncrementalOnlyCarriesDelta(t *testing.T) {
	s := NewStore()
	sn := NewSnapshotter(s)
	for i := 0; i < 1000; i++ {
		s.Set(fmt.Sprintf("k%04d", i), uint64(i))
	}
	full := sn.Full()
	if len(full.Entries) != 1000 {
		t.Fatalf("full entries = %d, want 1000", len(full.Entries))
	}

	// Touch only 3 keys out of 1000: the incremental snapshot must be tiny.
	s.Set("k0001", 111)
	s.Set("k0002", 222)
	s.Delete("k0003")
	inc := sn.Incremental()
	if inc.Kind != KindIncremental {
		t.Fatalf("kind = %v, want incremental", inc.Kind)
	}
	if len(inc.Entries) != 3 {
		t.Fatalf("incremental entries = %d, want 3", len(inc.Entries))
	}
	byKey := map[string]Entry{}
	for _, e := range inc.Entries {
		byKey[e.Key] = e
	}
	if e := byKey["k0001"]; e.Value != 111 || e.Deleted {
		t.Fatalf("k0001 entry = %+v", e)
	}
	if e := byKey["k0003"]; !e.Deleted {
		t.Fatalf("k0003 should be a tombstone: %+v", e)
	}

	// No mutations since: the next incremental is empty but still valid.
	empty := sn.Incremental()
	if len(empty.Entries) != 0 {
		t.Fatalf("empty incremental has %d entries", len(empty.Entries))
	}
	if empty.Version != inc.Version {
		t.Fatalf("version moved without mutations: %d -> %d", inc.Version, empty.Version)
	}
}

func TestSnapshotEncodeDecodeRoundTrip(t *testing.T) {
	s := NewStore()
	sn := NewSnapshotter(s)
	s.Set("a", 1)
	s.Set("b", 2)
	s.Delete("a")
	snap := sn.Full()

	data, err := snap.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	if back.Seq != snap.Seq || back.Version != snap.Version || back.Kind != snap.Kind {
		t.Fatalf("header mismatch: %+v vs %+v", back, snap)
	}
	if len(back.Entries) != 1 || back.Entries[0].Key != "b" || back.Entries[0].Value != 2 {
		t.Fatalf("entries mismatch: %+v", back.Entries)
	}
}

func TestDecodeSnapshotRejectsGarbage(t *testing.T) {
	if _, err := DecodeSnapshot([]byte{0xff, 0xff, 0xff}); err == nil {
		t.Fatalf("garbage accepted")
	}
	if _, err := DecodeSnapshot(nil); err != nil {
		t.Fatalf("empty snapshot should decode: %v", err)
	}
}

func TestArchiveRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore()
	sn := NewSnapshotter(s)
	arc, err := OpenArchive(dir)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 500; i++ {
		s.Set(fmt.Sprintf("user:%04d", i), uint64(i*7))
	}
	if _, err := arc.Append(sn.Full()); err != nil {
		t.Fatal(err)
	}
	// Two incremental checkpoints with mutations and deletions.
	s.Set("user:0001", 999)
	s.Delete("user:0002")
	if _, err := arc.Append(sn.Incremental()); err != nil {
		t.Fatal(err)
	}
	s.Set("user:0500", 123456)
	if _, err := arc.Append(sn.Incremental()); err != nil {
		t.Fatal(err)
	}

	infos, err := arc.Checkpoints()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 3 {
		t.Fatalf("checkpoints = %d, want 3", len(infos))
	}
	if infos[0].Kind != KindFull || infos[1].Kind != KindIncremental || infos[2].Kind != KindIncremental {
		t.Fatalf("checkpoint kinds: %+v", infos)
	}

	restored := NewStore()
	if err := arc.Restore(restored); err != nil {
		t.Fatal(err)
	}
	if restored.Len() != 500 { // 500 - 1 deleted + 1 added
		t.Fatalf("restored len = %d, want 500", restored.Len())
	}
	if v, ok := restored.Get("user:0001"); !ok || v != 999 {
		t.Fatalf("user:0001 = %d, %v", v, ok)
	}
	if _, ok := restored.Get("user:0002"); ok {
		t.Fatalf("user:0002 should be deleted")
	}
	if v, ok := restored.Get("user:0499"); !ok || v != 499*7 {
		t.Fatalf("user:0499 = %d, %v", v, ok)
	}
	if v, ok := restored.Get("user:0500"); !ok || v != 123456 {
		t.Fatalf("user:0500 = %d, %v", v, ok)
	}
	if restored.Version() != s.Version() {
		t.Fatalf("restored version = %d, want %d", restored.Version(), s.Version())
	}
}

func TestRestoreFullResetsState(t *testing.T) {
	dir := t.TempDir()
	s := NewStore()
	sn := NewSnapshotter(s)
	arc, _ := OpenArchive(dir)

	s.Set("old", 1)
	if _, err := arc.Append(sn.Full()); err != nil {
		t.Fatal(err)
	}
	// Restore into a store that has unrelated stale keys: a full snapshot
	// must wipe them.
	stale := NewStore()
	stale.Set("stale-key", 42)
	if err := arc.Restore(stale); err != nil {
		t.Fatal(err)
	}
	if _, ok := stale.Get("stale-key"); ok {
		t.Fatalf("stale key survived full restore")
	}
	if v, ok := stale.Get("old"); !ok || v != 1 {
		t.Fatalf("old = %d, %v", v, ok)
	}
}

func TestRestoreEmptyArchive(t *testing.T) {
	arc, err := OpenArchive(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore()
	if err := arc.Restore(s); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 0 {
		t.Fatalf("len = %d, want 0", s.Len())
	}
}

// TestConcurrentSnapshotAndArchive hammers the store with writers while
// snapshotting and archiving concurrently. Run with -race.
func TestConcurrentSnapshotAndArchive(t *testing.T) {
	dir := t.TempDir()
	s := NewStore()
	sn := NewSnapshotter(s)
	arc, err := OpenArchive(dir)
	if err != nil {
		t.Fatal(err)
	}

	const writers = 8
	const opsPerWriter = 2000
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerWriter; i++ {
				key := fmt.Sprintf("w%02d-k%04d", id, i%256)
				s.Set(key, uint64(i))
				if i%97 == 0 {
					s.Delete(key)
				}
			}
		}(w)
	}

	// Concurrent snapshotter + archiver.
	var snapWg sync.WaitGroup
	for g := 0; g < 2; g++ {
		snapWg.Add(1)
		go func(full bool) {
			defer snapWg.Done()
			for i := 0; i < 50; i++ {
				var snap *Snapshot
				if full {
					snap = sn.Full()
				} else {
					snap = sn.Incremental()
				}
				data, err := snap.Encode()
				if err != nil {
					t.Errorf("encode: %v", err)
					return
				}
				if _, err := DecodeSnapshot(data); err != nil {
					t.Errorf("decode: %v", err)
					return
				}
				if _, err := arc.Append(snap); err != nil {
					t.Errorf("archive: %v", err)
					return
				}
			}
		}(g == 0)
	}

	// Concurrent readers.
	for g := 0; g < 4; g++ {
		snapWg.Add(1)
		go func() {
			defer snapWg.Done()
			for i := 0; i < 500; i++ {
				s.Get("w00-k0000")
				s.Len()
				s.Version()
			}
		}()
	}

	wg.Wait()
	snapWg.Wait()

	// Final consistency: one last full snapshot, archive it, restore, and
	// the restored state must equal the live state exactly.
	final := sn.Full()
	if _, err := arc.Append(final); err != nil {
		t.Fatal(err)
	}
	restored := NewStore()
	finalArc, _ := OpenArchive(t.TempDir())
	if _, err := finalArc.Append(final); err != nil {
		t.Fatal(err)
	}
	if err := finalArc.Restore(restored); err != nil {
		t.Fatal(err)
	}
	if restored.Len() != s.Len() {
		t.Fatalf("restored len = %d, live = %d", restored.Len(), s.Len())
	}
	for _, e := range final.Entries {
		if v, ok := restored.Get(e.Key); !ok || v != e.Value {
			t.Fatalf("restored[%s] = %d, %v; want %d", e.Key, v, ok, e.Value)
		}
	}
}

// TestNoGoroutineLeak asserts the package spawns no background goroutines:
// the goroutine count must return to baseline after all operations.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	dir := t.TempDir()
	s := NewStore()
	sn := NewSnapshotter(s)
	arc, _ := OpenArchive(dir)
	for i := 0; i < 100; i++ {
		s.Set(fmt.Sprintf("k%d", i), uint64(i))
	}
	if _, err := arc.Append(sn.Full()); err != nil {
		t.Fatal(err)
	}
	s.Set("k0", 999)
	if _, err := arc.Append(sn.Incremental()); err != nil {
		t.Fatal(err)
	}
	if err := arc.Restore(NewStore()); err != nil {
		t.Fatal(err)
	}

	// Allow the scheduler a moment, then compare.
	for i := 0; i < 100; i++ {
		runtime.Gosched()
	}
	after := runtime.NumGoroutine()
	if after > before {
		t.Fatalf("goroutine leak: before = %d, after = %d", before, after)
	}
}

// TestSnapshotMemoryIsolation ensures mutating the store after a snapshot
// does not retroactively change the captured snapshot.
func TestSnapshotMemoryIsolation(t *testing.T) {
	s := NewStore()
	sn := NewSnapshotter(s)
	s.Set("x", 1)
	snap := sn.Full()
	s.Set("x", 2)
	s.Delete("x")
	for _, e := range snap.Entries {
		if e.Key == "x" && e.Value != 1 {
			t.Fatalf("snapshot mutated retroactively: %+v", e)
		}
	}
}
