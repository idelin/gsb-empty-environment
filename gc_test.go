package gcstore

import (
	"io/ioutil"
	"os"
	"sort"
	"testing"
)

func tempStore(t *testing.T, maxBytes int64) (*Store, string, func()) {
	dir, err := ioutil.TempDir("", "gcstore-test-")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, maxBytes)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return s, dir, func() { s.Close(); os.RemoveAll(dir) }
}

func reopen(t *testing.T, dir string, maxBytes int64) *Store {
	s, err := Open(dir, maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func dirBytes(t *testing.T, dir string) int64 {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, fi := range files {
		if _, ok := parseSegName(fi.Name()); ok {
			total += fi.Size()
		}
	}
	return total
}

func segIDs(t *testing.T, dir string) []uint64 {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uint64
	for _, fi := range files {
		if id, ok := parseSegName(fi.Name()); ok {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// readTree traverses from root and returns the data payloads in
// deterministic (hash-sorted) order.
func readTree(t *testing.T, s *Store, root Hash) []string {
	var out []string
	seen := make(map[Hash]bool)
	stack := []Hash{root}
	for len(stack) > 0 {
		h := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[h] {
			continue
		}
		seen[h] = true
		b, err := s.Get(h)
		if err != nil {
			t.Fatalf("read %s: %v", h.String(), err)
		}
		out = append(out, string(b.Data))
		stack = append(stack, b.Refs...)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustPut(t *testing.T, s *Store, refs []Hash, data string) Hash {
	h, err := s.Put(refs, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func mustSnapshot(t *testing.T, s *Store, name string, root Hash) {
	if err := s.Snapshot(name, root); err != nil {
		t.Fatal(err)
	}
}

func mustRotate(t *testing.T, s *Store) {
	if err := s.Rotate(); err != nil {
		t.Fatal(err)
	}
}

// TestGCReachabilityMultipleRoots verifies that the mark phase keeps
// exactly the blocks reachable from the union of all snapshot roots,
// collects unreachable garbage, and collapses duplicate copies of the
// same content to a single physical copy.
func TestGCReachabilityMultipleRoots(t *testing.T) {
	s, dir, cleanup := tempStore(t, 0)
	defer cleanup()

	shared := mustPut(t, s, nil, "shared-leaf")
	mustRotate(t, s)
	dup := mustPut(t, s, nil, "shared-leaf") // duplicate copy on disk
	if dup != shared {
		t.Fatal("same content must hash equally")
	}
	mustRotate(t, s)

	a1 := mustPut(t, s, nil, "a1")
	rootA := mustPut(t, s, []Hash{shared, a1}, "rootA")
	mustRotate(t, s)
	b1 := mustPut(t, s, nil, "b1")
	rootB := mustPut(t, s, []Hash{shared, b1}, "rootB")
	mustRotate(t, s)
	g1 := mustPut(t, s, nil, "garbage-1")
	g2 := mustPut(t, s, []Hash{g1}, "garbage-2")
	mustRotate(t, s)

	mustSnapshot(t, s, "A", rootA)
	mustSnapshot(t, s, "B", rootB)

	// Sanity: two physical copies of the shared block exist before GC.
	recLen := int64(len(encodeRecord(&Block{Data: []byte("shared-leaf")})))
	before := dirBytes(t, dir)

	if err := s.GC(); err != nil {
		t.Fatal(err)
	}

	// Every block reachable from either root survives.
	for _, h := range []Hash{shared, a1, rootA, b1, rootB} {
		if !s.Has(h) {
			t.Fatalf("live block %s collected", h.String())
		}
	}
	// Unreachable blocks are gone.
	for _, h := range []Hash{g1, g2} {
		if s.Has(h) {
			t.Fatalf("garbage block %s survived", h.String())
		}
		if _, err := s.Get(h); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	}
	// Graph contents are intact through both roots.
	if got := readTree(t, s, rootA); !equalStrings(got, []string{"a1", "rootA", "shared-leaf"}) {
		t.Fatalf("rootA tree: %v", got)
	}
	if got := readTree(t, s, rootB); !equalStrings(got, []string{"b1", "rootB", "shared-leaf"}) {
		t.Fatalf("rootB tree: %v", got)
	}
	// Deduplication: the two physical copies of "shared-leaf" collapsed
	// to one, so exactly one record's worth of bytes was reclaimed
	// beyond the garbage removal.
	live := []*Block{
		{Data: []byte("shared-leaf")},
		{Data: []byte("a1")},
		{Refs: []Hash{shared, a1}, Data: []byte("rootA")},
		{Data: []byte("b1")},
		{Refs: []Hash{shared, b1}, Data: []byte("rootB")},
	}
	var want int64
	for _, b := range live {
		want += int64(len(encodeRecord(b)))
	}
	if got := dirBytes(t, dir); got != want {
		t.Fatalf("bytes after GC = %d, want %d (before %d, dup record %d)", got, want, before, recLen)
	}
}

// TestOldSnapshotReadsDuringAndAfterGC verifies that an old snapshot
// stays readable while GC is mid-flight (after the copy phase, before
// the cut point) and after the full collection.
func TestOldSnapshotReadsDuringAndAfterGC(t *testing.T) {
	s, dir, cleanup := tempStore(t, 0)
	defer cleanup()

	v1 := mustPut(t, s, nil, "data-v1")
	root1 := mustPut(t, s, []Hash{v1}, "root-v1")
	mustSnapshot(t, s, "old", root1)
	mustRotate(t, s)

	v2 := mustPut(t, s, nil, "data-v2")
	root2 := mustPut(t, s, []Hash{v2}, "root-v2")
	mustSnapshot(t, s, "new", root2)
	mustRotate(t, s)

	mustPut(t, s, nil, "garbage")
	mustRotate(t, s)

	oldSegs := segIDs(t, dir)

	st, err := s.GCMark()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.GCCopy(st); err != nil {
		t.Fatal(err)
	}

	// Cut point not reached yet: old segments must still be on disk and
	// the old snapshot must read through its original locations.
	for _, id := range oldSegs {
		if _, err := os.Stat(s.segPath(id)); err != nil {
			t.Fatalf("segment %d deleted before cut point: %v", id, err)
		}
	}
	if got := readTree(t, s, root1); !equalStrings(got, []string{"data-v1", "root-v1"}) {
		t.Fatalf("old snapshot mid-GC: %v", got)
	}

	if err := s.GCPersist(st); err != nil {
		t.Fatal(err)
	}
	if err := s.GCSweep(st); err != nil {
		t.Fatal(err)
	}

	if got := readTree(t, s, root1); !equalStrings(got, []string{"data-v1", "root-v1"}) {
		t.Fatalf("old snapshot after GC: %v", got)
	}
	if got := readTree(t, s, root2); !equalStrings(got, []string{"data-v2", "root-v2"}) {
		t.Fatalf("new snapshot after GC: %v", got)
	}
	for _, id := range oldSegs {
		if _, err := os.Stat(s.segPath(id)); !os.IsNotExist(err) {
			t.Fatalf("segment %d not swept", id)
		}
	}
}

// TestMappingRecoveryCrashBeforePersist simulates a crash after the
// copy phase but before the cut point: the recovered store must have
// the old mapping, intact old segments, and no orphan GC segment.
func TestMappingRecoveryCrashBeforePersist(t *testing.T) {
	s, dir, cleanup := tempStore(t, 0)
	defer cleanup()

	leaf := mustPut(t, s, nil, "leaf")
	root := mustPut(t, s, []Hash{leaf}, "root")
	mustSnapshot(t, s, "snap", root)
	mustRotate(t, s)
	garbage := mustPut(t, s, nil, "garbage")
	mustRotate(t, s)

	st, err := s.GCMark()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.GCCopy(st); err != nil {
		t.Fatal(err)
	}
	if st.NewSeg == 0 {
		t.Fatal("expected blocks to be copied")
	}
	if _, err := os.Stat(s.segPath(st.NewSeg)); err != nil {
		t.Fatalf("GC segment missing before crash: %v", err)
	}
	segsBefore := segIDs(t, dir)

	// "Crash": abandon the store without persisting or sweeping, then
	// reopen the directory.
	s.Close()
	s2 := reopen(t, dir, 0)
	defer s2.Close()

	// Old mapping recovered: live blocks and even the garbage are still
	// mapped, and reads go through the original locations.
	if got := readTree(t, s2, root); !equalStrings(got, []string{"leaf", "root"}) {
		t.Fatalf("snapshot after recovery: %v", got)
	}
	if !s2.Has(garbage) {
		t.Fatal("mapping changed despite crash before cut point")
	}
	// Orphan GC segment removed by recovery; only the pre-GC segments
	// remain.
	got := segIDs(t, dir)
	want := segsBefore[:len(segsBefore)-1] // drop the GC segment
	if len(got) != len(want) {
		t.Fatalf("segments after recovery = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("segments after recovery = %v, want %v", got, want)
		}
	}
	// GC can be rerun to completion on the recovered store.
	if err := s2.GC(); err != nil {
		t.Fatal(err)
	}
	if s2.Has(garbage) {
		t.Fatal("garbage survived rerun GC")
	}
	if got := readTree(t, s2, root); !equalStrings(got, []string{"leaf", "root"}) {
		t.Fatalf("snapshot after rerun GC: %v", got)
	}
}

// TestMappingRecoveryCrashAfterPersist simulates a crash after the cut
// point but before the sweep: recovery must load the new mapping and
// drop the now-unreferenced old segments, with all data readable.
func TestMappingRecoveryCrashAfterPersist(t *testing.T) {
	s, dir, cleanup := tempStore(t, 0)
	defer cleanup()

	leaf := mustPut(t, s, nil, "leaf")
	root := mustPut(t, s, []Hash{leaf}, "root")
	mustSnapshot(t, s, "snap", root)
	mustRotate(t, s)
	garbage := mustPut(t, s, nil, "garbage")
	mustRotate(t, s)

	st, err := s.GCMark()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.GCCopy(st); err != nil {
		t.Fatal(err)
	}
	if err := s.GCPersist(st); err != nil {
		t.Fatal(err)
	}
	oldSegs := segIDs(t, dir)

	// "Crash" after the cut point, before the sweep.
	s.Close()
	s2 := reopen(t, dir, 0)
	defer s2.Close()

	// New mapping recovered: data readable, garbage unmapped.
	if got := readTree(t, s2, root); !equalStrings(got, []string{"leaf", "root"}) {
		t.Fatalf("snapshot after recovery: %v", got)
	}
	if s2.Has(garbage) {
		t.Fatal("garbage still mapped after cut point")
	}
	// Recovery finished the sweep: only the GC output segment remains.
	got := segIDs(t, dir)
	if len(got) != 1 || got[0] != st.NewSeg {
		t.Fatalf("segments after recovery = %v, want [%d] (old %v)", got, st.NewSeg, oldSegs)
	}
}

// TestGCOutOfSpaceAbort verifies that GC aborts cleanly when copying
// the live set would exceed the space budget: it returns ErrNoSpace,
// leaves the mapping and all segments untouched, removes partial
// output, and can be retried once more space is available.
func TestGCOutOfSpaceAbort(t *testing.T) {
	s, dir, cleanup := tempStore(t, 0)
	defer cleanup()

	live := mustPut(t, s, nil, string(make([]byte, 100)))
	root := mustPut(t, s, []Hash{live}, "root")
	mustSnapshot(t, s, "snap", root)
	mustRotate(t, s)
	garbage := mustPut(t, s, nil, string(make([]byte, 200)))
	mustRotate(t, s)

	used := dirBytes(t, dir)
	segsBefore := segIDs(t, dir)
	s.Close()

	// Budget fits the current data plus only part of the copied output:
	// old segments cannot be deleted until the cut point, so the copy
	// must abort.
	budget := used + int64(len(encodeRecord(&Block{Refs: []Hash{live}, Data: []byte("root")})))
	s2 := reopen(t, dir, budget)
	defer s2.Close()

	if err := s2.GC(); err != ErrNoSpace {
		t.Fatalf("GC error = %v, want ErrNoSpace", err)
	}

	// Nothing changed: mapping intact, all segments present, no partial
	// GC output left behind.
	if got := readTree(t, s2, root); len(got) != 2 {
		t.Fatalf("snapshot after abort: %v", got)
	}
	if !s2.Has(garbage) {
		t.Fatal("mapping changed after aborted GC")
	}
	if got := segIDs(t, dir); len(got) != len(segsBefore) {
		t.Fatalf("segments after abort = %v, want %v", got, segsBefore)
	}
	if got := dirBytes(t, dir); got != used {
		t.Fatalf("bytes after abort = %d, want %d", got, used)
	}

	// With the budget lifted, GC succeeds and reclaims the garbage.
	s2.Close()
	s3 := reopen(t, dir, 0)
	defer s3.Close()
	if err := s3.GC(); err != nil {
		t.Fatal(err)
	}
	if s3.Has(garbage) {
		t.Fatal("garbage survived GC after budget lifted")
	}
	if got := readTree(t, s3, root); len(got) != 2 {
		t.Fatalf("snapshot after retry: %v", got)
	}
}
