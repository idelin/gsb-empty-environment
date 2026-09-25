package segmentfsm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// attempt runs fn and converts a SimulatedCrash panic into a flag, the
// same way a supervisor would observe a crashed process.
func attempt(fn func() error) (crashed bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(SimulatedCrash); ok {
				crashed = true
				err = nil
				return
			}
			panic(r)
		}
	}()
	err = fn()
	return crashed, err
}

func crashOnceAt(p FaultPoint) Injector {
	fired := false
	return func(pt FaultPoint) error {
		if pt == p && !fired {
			fired = true
			panic(SimulatedCrash{Point: pt})
		}
		return nil
	}
}

func failOnceAt(p FaultPoint) Injector {
	fired := false
	return func(pt FaultPoint) error {
		if pt == p && !fired {
			fired = true
			return fmt.Errorf("transient injected fault at %s", pt)
		}
		return nil
	}
}

var (
	batch1 = []string{"a1", "a2"}
	batch2 = []string{"b1", "b2"}
)

// seedGen1 commits generation 1 without any fault injection, so the
// faulted operation (generation 2) exercises every step including
// reclaiming a real old segment.
func seedGen1(t *testing.T, dir string) {
	t.Helper()
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Ingest(batch1); err != nil {
		t.Fatal(err)
	}
}

// drive opens the store (recovering any interrupted operation first) and
// starts the generation-2 ingest if it has not committed yet.
func drive(dir string, inj Injector) error {
	f, err := Open(dir, WithInjector(inj))
	if err != nil {
		return err
	}
	if f.Generation() == 1 {
		return f.Ingest(batch2)
	}
	return nil
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func verifyFinalState(t *testing.T, dir string) {
	t.Helper()
	f, err := Open(dir)
	if err != nil {
		t.Fatalf("final open: %v", err)
	}
	if f.Generation() != 2 {
		t.Fatalf("generation = %d, want 2", f.Generation())
	}
	got, err := f.ReadAll()
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	wantRecords := append(append([]string(nil), batch1...), batch2...)
	if !reflect.DeepEqual(got, wantRecords) {
		t.Fatalf("records = %v, want %v", got, wantRecords)
	}
	wantFiles := []string{"manifest.json", "seg-000002.dat"}
	if files := listDir(t, dir); !reflect.DeepEqual(files, wantFiles) {
		t.Fatalf("dir = %v, want exactly %v (no journal/tmp/orphans)", files, wantFiles)
	}
}

func TestHappyPath(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Ingest([]string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Ingest([]string{"c"}); err != nil {
		t.Fatal(err)
	}
	if f.Generation() != 2 {
		t.Fatalf("generation = %d, want 2", f.Generation())
	}
	got, err := f.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %v, want %v", got, want)
	}
	// The generation-1 segment must have been reclaimed after gen 2 published.
	wantFiles := []string{"manifest.json", "seg-000002.dat"}
	if files := listDir(t, dir); !reflect.DeepEqual(files, wantFiles) {
		t.Fatalf("dir = %v, want %v", files, wantFiles)
	}
}

// TestCrashRecoveryAtEveryPoint injects a hard crash (panic that skips all
// defers) at each fault point and verifies the store always recovers to
// the same correct final state.
func TestCrashRecoveryAtEveryPoint(t *testing.T) {
	for _, point := range AllFaultPoints {
		t.Run(string(point), func(t *testing.T) {
			dir := t.TempDir()
			seedGen1(t, dir)
			inj := crashOnceAt(point)
			done := false
			for i := 0; i < 20 && !done; i++ {
				crashed, err := attempt(func() error { return drive(dir, inj) })
				if err != nil {
					t.Fatalf("attempt %d: %v", i, err)
				}
				done = !crashed
			}
			if !done {
				t.Fatal("did not converge after 20 attempts")
			}
			verifyFinalState(t, dir)
		})
	}
}

// TestTransientFaultRetry injects a retryable error at each fault point and
// verifies that simply calling again (idempotent retry) completes cleanly.
func TestTransientFaultRetry(t *testing.T) {
	for _, point := range AllFaultPoints {
		t.Run(string(point), func(t *testing.T) {
			dir := t.TempDir()
			seedGen1(t, dir)
			inj := failOnceAt(point)
			var firstErr error
			for i := 0; i < 20; i++ {
				err := drive(dir, inj)
				if i == 0 {
					firstErr = err
				}
				if err == nil {
					if firstErr == nil {
						t.Fatal("expected the first attempt to fail")
					}
					verifyFinalState(t, dir)
					return
				}
			}
			t.Fatal("did not converge after 20 attempts")
		})
	}
}

// TestCrashLoopUntilSuccess crashes at EVERY journal write of the first N
// attempts, proving progress is made across an arbitrary crash sequence.
func TestCrashLoopUntilSuccess(t *testing.T) {
	dir := t.TempDir()
	seedGen1(t, dir)
	crashesLeft := 5 // crash at the next 5 fault points, whenever they fire
	inj := Injector(func(pt FaultPoint) error {
		if crashesLeft > 0 {
			crashesLeft--
			panic(SimulatedCrash{Point: pt})
		}
		return nil
	})
	done := false
	for i := 0; i < 20 && !done; i++ {
		crashed, err := attempt(func() error { return drive(dir, inj) })
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		done = !crashed
	}
	if !done {
		t.Fatal("did not converge")
	}
	verifyFinalState(t, dir)
}

func TestMissingReferencedSegmentIsCorruption(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Ingest([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "seg-000001.dat")); err != nil {
		t.Fatal(err)
	}
	_, err = Open(dir)
	if !errors.Is(err, ErrCorruption) {
		t.Fatalf("Open err = %v, want ErrCorruption", err)
	}
}

func TestBitRotSegmentIsCorruption(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Ingest([]string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	seg := filepath.Join(dir, "seg-000001.dat")
	data, _ := os.ReadFile(seg)
	data[0] ^= 0xff // flip bits, keep size
	if err := os.WriteFile(seg, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); !errors.Is(err, ErrCorruption) {
		t.Fatalf("Open err = %v, want ErrCorruption", err)
	}
}

func TestMissingManifestNeverFallsBack(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Ingest([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	// Segments exist but the manifest is gone: falling back to an empty
	// state would silently drop acknowledged data. Must be corruption.
	if _, err := Open(dir); !errors.Is(err, ErrCorruption) {
		t.Fatalf("Open err = %v, want ErrCorruption", err)
	}
}

func TestTornManifestIsCorruption(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Ingest([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"payload":garbage`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); !errors.Is(err, ErrCorruption) {
		t.Fatalf("Open err = %v, want ErrCorruption", err)
	}
}

func TestOrphanReclamation(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Ingest([]string{"keep"}); err != nil {
		t.Fatal(err)
	}
	// Plant orphans: a segment never referenced by any manifest, and
	// leftover staging files from a "crashed" step.
	for _, n := range []string{"seg-000777.dat", "seg-000002.dat.tmp", "manifest.json.tmp", "journal.json.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	want := []string{"manifest.json", "seg-000001.dat"}
	if files := listDir(t, dir); !reflect.DeepEqual(files, want) {
		t.Fatalf("dir = %v, want %v", files, want)
	}
}

// TestFsyncOrdering asserts the durability contract: every rename is
// preceded by an fsync of the file content and followed by an fsync of
// the directory. This is the boundary that makes renames power-cut safe.
func TestFsyncOrdering(t *testing.T) {
	dir := t.TempDir()
	type event struct{ op, path string }
	var events []event
	f, err := Open(dir, WithObserver(func(op, path string) {
		events = append(events, event{op, path})
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Ingest([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	index := func(op, pathSuffix string) int {
		for i, e := range events {
			if e.op == op && strings.HasSuffix(e.path, pathSuffix) {
				return i
			}
		}
		return -1
	}
	check := func(tmp string) {
		t.Helper()
		syncFile := index("sync", tmp)
		rename := -1
		for i, e := range events {
			if e.op == "rename" && strings.Contains(e.path, tmp) {
				rename = i
			}
		}
		if syncFile < 0 || rename < 0 {
			t.Fatalf("missing sync/rename events for %s: %v", tmp, events)
		}
		if syncFile > rename {
			t.Fatalf("file %s renamed before its content was fsynced: %v", tmp, events)
		}
		// A directory fsync must follow the rename.
		dirSyncAfter := false
		for _, e := range events[rename:] {
			if e.op == "sync" && e.path == dir {
				dirSyncAfter = true
			}
		}
		if !dirSyncAfter {
			t.Fatalf("no directory fsync after renaming %s: %v", tmp, events)
		}
	}
	check("seg-000001.dat.tmp")
	check("manifest.json.tmp")
	check("journal.json.tmp")
}

func TestIngestRejectsNewlines(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Ingest([]string{"ok", "bad\nrecord"}); err == nil {
		t.Fatal("expected error for record containing newline")
	}
}
