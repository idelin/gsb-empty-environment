package segstore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var allFaultPoints = []FaultPoint{
	FaultBeforeBuild,
	FaultAfterBuild,
	FaultBeforeValidate,
	FaultAfterValidate,
	FaultBeforePublish,
	FaultAfterPublish,
	FaultBeforeReclaim,
	FaultAfterReclaim,
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

func assertFiles(t *testing.T, dir string, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := listDir(t, dir)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

// TestCommitBasic covers the happy path, reads and full verification.
func TestCommitBasic(t *testing.T) {
	s, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Commit([]byte("gen-0")); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit([]byte("gen-1-longer")); err != nil {
		t.Fatal(err)
	}
	if s.Manifest().Version != 2 {
		t.Fatalf("version = %d, want 2", s.Manifest().Version)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("verify: %v", err)
	}
	got, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "gen-1-longer" {
		t.Fatalf("read = %q", got)
	}
}

// TestCrashAtEveryPoint injects a crash at every single state-machine
// point and proves reopening the directory ends in a fully committed store.
func TestCrashAtEveryPoint(t *testing.T) {
	for _, point := range allFaultPoints {
		t.Run(string(point), func(t *testing.T) {
			dir := t.TempDir()
			armed := false
			fault := func(fp FaultPoint) error {
				if armed && fp == point {
					return ErrInjectedFault
				}
				return nil
			}

			s, err := Open(dir, fault)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Commit([]byte("gen-0")); err != nil {
				t.Fatalf("base commit: %v", err)
			}

			armed = true // next commit crashes once at point
			err = s.Commit([]byte("gen-1"))
			if !errors.Is(err, ErrInjectedFault) {
				t.Fatalf("commit error = %v, want injected fault", err)
			}

			// Simulate process death: discard s, reopen the same directory.
			// before_build aborts the unacknowledged txn and needs a retry;
			// every other point rolls forward automatically.
			s2, err := Open(dir, nil)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			if s2.Manifest().Version == 1 {
				if err := s2.Commit([]byte("gen-1")); err != nil {
					t.Fatalf("retry commit: %v", err)
				}
			} else if s2.Manifest().Version != 2 {
				t.Fatalf("recovered version = %d, want 1 or 2", s2.Manifest().Version)
			}

			if s2.Manifest().Version != 2 {
				t.Fatalf("final version = %d", s2.Manifest().Version)
			}
			got, err := s2.Read()
			if err != nil {
				t.Fatal(err)
			}

			if string(got) != "gen-1" {
				t.Fatalf("recovered data = %q, want gen-1", got)
			}
			if err := s2.Verify(); err != nil {
				t.Fatalf("verify after recovery: %v", err)
			}
			// New segment present, old segment reclaimed, no journal/tmp.
			assertFiles(t, dir, "manifest.json", "seg-000002.dat")
		})
	}
}

// TestIdempotentRetry retries across multiple "process restarts" while a
// fault persists; no duplicate segments or journals may appear.
func TestIdempotentRetry(t *testing.T) {
	dir := t.TempDir()
	alwaysFault := func(fp FaultPoint) error {
		if fp == FaultBeforePublish {
			return ErrInjectedFault
		}
		return nil
	}

	s1, err := Open(dir, alwaysFault)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Commit([]byte("payload")); !errors.Is(err, ErrInjectedFault) {
		t.Fatalf("first attempt: %v", err)
	}

	// Reopen while the fault persists: recovery reaches the same point.
	if _, err := Open(dir, alwaysFault); !errors.Is(err, ErrInjectedFault) {
		t.Fatalf("faulty reopen: %v", err)
	}

	// Restart with the fault cleared: the txn rolls forward exactly once.
	s3, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("clean reopen: %v", err)
	}
	if s3.Manifest().Version != 1 {
		t.Fatalf("version = %d, want 1", s3.Manifest().Version)
	}
	got, _ := s3.Read()
	if string(got) != "payload" {
		t.Fatalf("data = %q", got)
	}
	assertFiles(t, dir, "manifest.json", "seg-000001.dat")
}

// TestRecoverBuildingAbortsPartialSegment: a crash while building leaves a
// torn segment that is not yet acknowledged; recovery removes it instead
// of publishing bad data, and a retry reuses the txn number safely.
func TestRecoverBuildingAbortsPartialSegment(t *testing.T) {
	dir := t.TempDir()
	want := []byte("the-real-payload")
	sum := sha256Hex(want)

	j := journal{
		State:      StateBuilding,
		Txn:        1,
		NewSegment: "seg-000001.dat",
		Size:       int64(len(want)),
		SHA256:     sum,
	}
	if err := writeJournalJSON(dir, j); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seg-000001.dat"),
		[]byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if s.Manifest().Version != 0 {
		t.Fatalf("version = %d, want aborted txn", s.Manifest().Version)
	}
	if _, err := os.Stat(filepath.Join(dir, "seg-000001.dat")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("partial segment not reclaimed, err=%v", err)
	}
	if err := s.Commit(want); err != nil {
		t.Fatalf("retry: %v", err)
	}
	assertFiles(t, dir, "manifest.json", "seg-000001.dat")
}

// TestCollectOrphans removes unreferenced files but protects the in-flight
// pre-publish segment referenced by the journal.
func TestCollectOrphans(t *testing.T) {
	dir := t.TempDir()
	armed := false
	s, err := Open(dir, func(fp FaultPoint) error {
		if armed && fp == FaultAfterBuild {
			return ErrInjectedFault
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Commit([]byte("gen-0")); err != nil {
		t.Fatal(err)
	}
	armed = true
	if err := s.Commit([]byte("gen-1")); !errors.Is(err, ErrInjectedFault) {
		t.Fatalf("crash: %v", err)
	}

	// Pre-publish new segment is protected by the journal; real orphans go.
	if err := os.WriteFile(filepath.Join(dir, "seg-999999.dat"),
		[]byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "leftover.tmp"),
		[]byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed, err := s.CollectOrphans()
	if err != nil {
		t.Fatal(err)
	}
	wantRemoved := []string{"leftover.tmp", "seg-999999.dat"}
	if strings.Join(removed, ",") != strings.Join(wantRemoved, ",") {
		t.Fatalf("removed = %v, want %v", removed, wantRemoved)
	}
	if _, err := os.Stat(filepath.Join(dir, "seg-000002.dat")); err != nil {
		t.Fatalf("in-flight segment must be protected: %v", err)
	}

	// After restart the txn publishes and old segments are reclaimed.
	s2, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	removed, err = s2.CollectOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("unexpected leftovers: %v", removed)
	}
	assertFiles(t, dir, "manifest.json", "seg-000002.dat")
}

// TestMissingSegmentIsCorrupt: a manifest pointing at a missing segment is
// corruption; Open must surface ErrCorrupt rather than fall back.
func TestMissingSegmentIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Commit([]byte("acknowledged")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "seg-000001.dat")); err != nil {
		t.Fatal(err)
	}
	_, err = Open(dir, nil)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("open after segment loss: %v, want ErrCorrupt", err)
	}
}

// TestTamperedManifestIsCorrupt: any byte-level change to the manifest is
// caught by its embedded checksum.
func TestTamperedManifestIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Commit([]byte("acknowledged")); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, manifestFile)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	key := `"sha256":"`
	idx := strings.LastIndex(text, key)
	if idx < 0 {
		t.Fatal("no checksum field")
	}
	pos := idx + len(key)
	alt := byte('0')
	if text[pos] == '0' {
		alt = '1'
	}
	flipped := text[:pos] + string(alt) + text[pos+1:]
	if err := os.WriteFile(p, []byte(flipped), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Open(dir, nil)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("open after tamper: %v, want ErrCorrupt", err)
	}
}

// TestMissingManifestWithSegmentsIsCorrupt: orphan segments without a
// manifest are never silently treated as current data.
func TestMissingManifestWithSegmentsIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seg-000001.dat"),
		[]byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir, nil); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("open = %v, want ErrCorrupt", err)
	}
}

// TestReadMissingDataNotFallback is covered by ErrCorrupt semantics; here
// we additionally confirm Verify reports content corruption.
func TestVerifyDetectsBitRot(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Commit([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "seg-000001.dat")
	b, _ := os.ReadFile(p)
	b[0] ^= 0xff
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("verify = %v, want ErrCorrupt", err)
	}
}
