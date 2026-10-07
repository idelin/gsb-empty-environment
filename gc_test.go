package gcstore

import (
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempStore(t *testing.T, maxBytes int64) (*Store, string) {
	dir, err := ioutil.TempDir("", "gcstore-test")
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir, maxBytes)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return st, dir
}

func mustPut(t *testing.T, st *Store, id string, links []string, data string) {
	t.Helper()
	if err := st.Put(Block{ID: id, Links: links, Data: []byte(data)}); err != nil {
		t.Fatalf("Put(%s): %v", id, err)
	}
}

func mustGet(t *testing.T, st *Store, id string) Block {
	t.Helper()
	b, err := st.Get(id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	return b
}

// countSegmentBlocks decodes every record in every seg-* file.
func countSegmentBlocks(t *testing.T, dir string) int {
	t.Helper()
	entries, err := ioutil.ReadDir(filepath.Join(dir, "segments"))
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "seg-") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, "segments", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for {
			_, err := decodeRecord(f)
			if err == io.EOF {
				break
			}
			if err != nil {
				f.Close()
				t.Fatalf("decode %s: %v", e.Name(), err)
			}
			total++
		}
		f.Close()
	}
	return total
}

func listFiles(t *testing.T, dir, prefix string) []string {
	t.Helper()
	entries, err := ioutil.ReadDir(filepath.Join(dir, "segments"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestGCReachabilityMarksLiveBlocks(t *testing.T) {
	st, dir := tempStore(t, 0)
	defer os.RemoveAll(dir)

	mustPut(t, st, "A", []string{"B"}, "a")
	mustPut(t, st, "B", []string{"C"}, "b")
	mustPut(t, st, "C", nil, "c")
	mustPut(t, st, "D", nil, "dead")
	mustPut(t, st, "E", nil, "e")

	if err := st.Snapshot("s1", []string{"A"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Snapshot("s2", []string{"E"}); err != nil {
		t.Fatal(err)
	}

	if err := st.GC(); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"A", "B", "C", "E"} {
		mustGet(t, st, id)
	}
	if _, err := st.Get("D"); err != ErrNotFound {
		t.Fatalf("unreachable block D should be collected, got err=%v", err)
	}
}

func TestDuplicateBlocksCopiedOnce(t *testing.T) {
	st, dir := tempStore(t, 0)
	defer os.RemoveAll(dir)

	// S is reachable from both snapshots and via two links from R1.
	mustPut(t, st, "S", nil, "shared")
	mustPut(t, st, "X", nil, "x")
	mustPut(t, st, "R1", []string{"S", "S", "X"}, "r1")
	mustPut(t, st, "R2", []string{"S"}, "r2")

	if err := st.Snapshot("s1", []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Snapshot("s2", []string{"R2"}); err != nil {
		t.Fatal(err)
	}
	if err := st.GC(); err != nil {
		t.Fatal(err)
	}

	// 4 unique live blocks must occupy exactly 4 records after GC.
	if n := countSegmentBlocks(t, dir); n != 4 {
		t.Fatalf("expected 4 copied blocks, found %d", n)
	}
	if got := mustGet(t, st, "S"); string(got.Data) != "shared" {
		t.Fatalf("shared block corrupted: %q", got.Data)
	}
}

func TestOldSnapshotReadsAfterGC(t *testing.T) {
	st, dir := tempStore(t, 0)
	defer os.RemoveAll(dir)

	mustPut(t, st, "root", []string{"child"}, "root-data")
	mustPut(t, st, "child", []string{"leaf"}, "child-data")
	mustPut(t, st, "leaf", nil, "leaf-data")
	mustPut(t, st, "garbage", nil, "junk")
	if err := st.Snapshot("old", []string{"root"}); err != nil {
		t.Fatal(err)
	}
	if err := st.GC(); err != nil {
		t.Fatal(err)
	}

	// Walk the chain from the old snapshot root, before and after reopen.
	for round := 0; round < 2; round++ {
		if round == 1 {
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			var err error
			st, err = Open(dir, 0)
			if err != nil {
				t.Fatal(err)
			}
		}
		roots := st.Roots("old")
		if len(roots) != 1 || roots[0] != "root" {
			t.Fatalf("round %d: bad roots %v", round, roots)
		}
		b := mustGet(t, st, roots[0])
		if string(b.Data) != "root-data" || len(b.Links) != 1 {
			t.Fatalf("round %d: bad root %+v", round, b)
		}
		b = mustGet(t, st, b.Links[0])
		if string(b.Data) != "child-data" || len(b.Links) != 1 {
			t.Fatalf("round %d: bad child %+v", round, b)
		}
		b = mustGet(t, st, b.Links[0])
		if string(b.Data) != "leaf-data" {
			t.Fatalf("round %d: bad leaf %+v", round, b)
		}
	}
	st.Close()
}

func TestMappingRecovery(t *testing.T) {
	st, dir := tempStore(t, 0)
	defer os.RemoveAll(dir)

	mustPut(t, st, "one", nil, "1")
	mustPut(t, st, "two", []string{"one"}, "2")
	if err := st.Snapshot("snap", []string{"two"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()

	if got := mustGet(t, st2, "one"); string(got.Data) != "1" {
		t.Fatalf("recovered block mismatch: %q", got.Data)
	}
	if got := mustGet(t, st2, "two"); len(got.Links) != 1 || got.Links[0] != "one" {
		t.Fatalf("recovered links mismatch: %v", got.Links)
	}
	if roots := st2.Roots("snap"); len(roots) != 1 || roots[0] != "two" {
		t.Fatalf("snapshot roots not recovered: %v", roots)
	}
}

func TestGCOutOfSpaceAbort(t *testing.T) {
	// 3 blocks of 512 bytes: ~525 bytes/record, ~1575 total. A limit of
	// 2600 lets the writes in but cannot fit a second copy of the live
	// set, so GC must abort partway through the copy.
	st, dir := tempStore(t, 2600)
	defer os.RemoveAll(dir)

	mustPut(t, st, "A", []string{"B"}, string(make([]byte, 512)))
	mustPut(t, st, "B", []string{"C"}, string(make([]byte, 512)))
	mustPut(t, st, "C", nil, string(make([]byte, 512)))
	if err := st.Snapshot("s", []string{"A"}); err != nil {
		t.Fatal(err)
	}
	segsBefore := listFiles(t, dir, "seg-")

	if err := st.GC(); err != ErrNoSpace {
		t.Fatalf("expected ErrNoSpace, got %v", err)
	}

	// Old data intact, old segments untouched, partial copy removed.
	for _, id := range []string{"A", "B", "C"} {
		mustGet(t, st, id)
	}
	if got := listFiles(t, dir, "gc-"); len(got) != 0 {
		t.Fatalf("aborted GC left partial segments: %v", got)
	}
	if got := listFiles(t, dir, "seg-"); strings.Join(got, ",") != strings.Join(segsBefore, ",") {
		t.Fatalf("old segments changed by aborted GC: %v -> %v", segsBefore, got)
	}

	// The persisted manifest still describes the old mapping.
	st2, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	for _, id := range []string{"A", "B", "C"} {
		mustGet(t, st2, id)
	}
}

func TestCutpointMappingPersistedBeforeSweep(t *testing.T) {
	st, dir := tempStore(t, 0)
	defer os.RemoveAll(dir)

	mustPut(t, st, "A", []string{"B"}, "a")
	mustPut(t, st, "B", nil, "b")
	mustPut(t, st, "dead", nil, "junk")
	if err := st.Snapshot("s", []string{"A"}); err != nil {
		t.Fatal(err)
	}

	called := false
	st.afterMappingPersist = func() error {
		called = true
		// At the cutpoint the durable manifest must already reference
		// existing segment files for all live blocks.
		data, err := ioutil.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			t.Errorf("cutpoint: manifest not persisted: %v", err)
			return nil
		}
		var m manifest
		if err := json.Unmarshal(data, &m); err != nil {
			t.Errorf("cutpoint: %v", err)
			return nil
		}
		if len(m.Mapping) != 2 {
			t.Errorf("cutpoint: expected 2 live mappings, got %d", len(m.Mapping))
		}
		for id, ref := range m.Mapping {
			if _, err := os.Stat(filepath.Join(dir, "segments", ref.Segment)); err != nil {
				t.Errorf("cutpoint: mapping for %s references missing segment: %v", id, err)
			}
		}
		return nil
	}

	if err := st.GC(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("cutpoint hook not invoked")
	}
	// After the sweep, only the new data segment and the fresh (empty)
	// active segment remain.
	if got := listFiles(t, dir, "seg-"); len(got) != 2 {
		t.Fatalf("expected 2 segments after sweep, got %v", got)
	}
	if _, err := st.Get("dead"); err != ErrNotFound {
		t.Fatalf("dead block should be gone, got %v", err)
	}
}

var errCrash = errors.New("simulated crash")

func TestCrashAfterCutpointRecovers(t *testing.T) {
	st, dir := tempStore(t, 0)
	defer os.RemoveAll(dir)

	mustPut(t, st, "A", []string{"B"}, "a")
	mustPut(t, st, "B", nil, "b")
	if err := st.Snapshot("s", []string{"A"}); err != nil {
		t.Fatal(err)
	}

	// Crash right after the cutpoint, before old segments are deleted.
	st.afterMappingPersist = func() error { return errCrash }
	if err := st.GC(); err != errCrash {
		t.Fatalf("expected crash error, got %v", err)
	}

	// Recovery: the persisted mapping already points at the new segment.
	st2, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if got := mustGet(t, st2, "A"); string(got.Data) != "a" {
		t.Fatalf("bad data after crash recovery: %q", got.Data)
	}
	mustGet(t, st2, "B")
}

func TestCrashBeforeCutpointRecovers(t *testing.T) {
	st, dir := tempStore(t, 0)
	defer os.RemoveAll(dir)

	mustPut(t, st, "A", nil, "a")
	if err := st.Snapshot("s", []string{"A"}); err != nil {
		t.Fatal(err)
	}

	// Crash after the copy segment was written but before the new
	// mapping was persisted.
	st.beforeMappingPersist = func() error { return errCrash }
	if err := st.GC(); err != errCrash {
		t.Fatalf("expected crash error, got %v", err)
	}
	if got := listFiles(t, dir, "gc-"); len(got) != 1 {
		t.Fatalf("expected leftover gc segment, got %v", got)
	}

	// Recovery: old mapping still in effect, partial GC segment removed.
	st2, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if got := mustGet(t, st2, "A"); string(got.Data) != "a" {
		t.Fatalf("bad data after crash recovery: %q", got.Data)
	}
	if got := listFiles(t, dir, "gc-"); len(got) != 0 {
		t.Fatalf("recovery did not remove partial gc segment: %v", got)
	}
}

func TestDanglingLinksTolerated(t *testing.T) {
	st, dir := tempStore(t, 0)
	defer os.RemoveAll(dir)

	mustPut(t, st, "A", []string{"missing"}, "a")
	if err := st.Snapshot("s", []string{"A"}); err != nil {
		t.Fatal(err)
	}
	if err := st.GC(); err != nil {
		t.Fatal(err)
	}
	mustGet(t, st, "A")
	st.Close()
}
