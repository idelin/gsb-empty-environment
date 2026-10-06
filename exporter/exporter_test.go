package exporter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func seedSource(n int) *MemSource {
	src := NewMemSource()
	for i := 0; i < n; i++ {
		src.Put(fmt.Sprintf("key-%04d", i), fmt.Sprintf("value-%04d", i))
	}
	return src
}

func tempDir(t *testing.T) (string, func()) {
	dir, err := ioutil.TempDir("", "export-test")
	if err != nil {
		t.Fatal(err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

// wantRecords mirrors the source's current snapshot contents.
func wantRecords(src *MemSource, n int) []Record {
	page, err := src.ReadPage(src.SnapshotID(), "", 0)
	if err != nil {
		panic(err)
	}
	return page.Records
}

func runToCompletion(t *testing.T, e *Exporter) *Manifest {
	t.Helper()
	var m *Manifest
	for i := 0; i < 1000; i++ {
		var err error
		m, err = e.Export()
		if err != nil {
			t.Fatalf("export run %d: %v", i, err)
		}
		if m.Done {
			return m
		}
	}
	t.Fatal("export did not converge")
	return nil
}

func assertComplete(t *testing.T, e *Exporter, m *Manifest, want []Record) {
	t.Helper()
	if !m.Done {
		t.Fatal("manifest not marked done")
	}
	got, err := e.Collect(m)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("final set mismatch:\n got %d records, first=%v\n want %d records, first=%v",
			len(got), got[0], len(want), want[0])
	}
}

// TestFullExportCoversSnapshot: one-shot export of the whole snapshot.
func TestFullExportCoversSnapshot(t *testing.T) {
	src := seedSource(25)
	dir, cleanup := tempDir(t)
	defer cleanup()
	e := &Exporter{Source: src, Dir: dir, PageSize: 10}
	m := runToCompletion(t, e)
	if len(m.Pages) != 3 {
		t.Fatalf("want 3 pages, got %d", len(m.Pages))
	}
	if m.SnapshotID != src.SnapshotID() {
		t.Fatalf("manifest snapshot %q != source %q", m.SnapshotID, src.SnapshotID())
	}
	// Page ranges must tile the key space without gaps or overlaps.
	for i := 1; i < len(m.Pages); i++ {
		if m.Pages[i].StartKey <= m.Pages[i-1].EndKey {
			t.Fatalf("page ranges overlap: %+v then %+v", m.Pages[i-1], m.Pages[i])
		}
	}
	assertComplete(t, e, m, wantRecords(src, 25))
}

// TestResumeAfterInterruption: crash mid-export, resume reuses valid pages
// and does not re-read them from the source.
func TestResumeAfterInterruption(t *testing.T) {
	src := seedSource(25)
	dir, cleanup := tempDir(t)
	defer cleanup()
	e := &Exporter{Source: src, Dir: dir, PageSize: 10, MaxPagesPerRun: 1}

	m, err := e.Export() // exports page 0, then "crashes"
	if err != nil {
		t.Fatal(err)
	}
	if m.Done || len(m.Pages) != 1 {
		t.Fatalf("after run 1: done=%v pages=%d", m.Done, len(m.Pages))
	}
	readsBefore := src.ReadCount()

	e2 := &Exporter{Source: src, Dir: dir, PageSize: 10}
	m = runToCompletion(t, e2)
	if got := src.ReadCount() - readsBefore; got != 2 {
		t.Fatalf("resume re-read %d pages, want 2 (only the missing ones)", got)
	}
	assertComplete(t, e2, m, wantRecords(src, 25))
}

// TestSourceUpdateInvalidatesManifest: a source mutation rotates the
// snapshot ID, so resume must NOT reuse any old page.
func TestSourceUpdateInvalidatesManifest(t *testing.T) {
	src := seedSource(25)
	dir, cleanup := tempDir(t)
	defer cleanup()
	e := &Exporter{Source: src, Dir: dir, PageSize: 10, MaxPagesPerRun: 2}
	if _, err := e.Export(); err != nil {
		t.Fatal(err)
	}

	// Source changes between runs: update + delete + insert.
	src.Put("key-0003", "updated")
	src.Delete("key-0007")
	src.Put("key-9999", "new")
	readsBefore := src.ReadCount()

	e2 := &Exporter{Source: src, Dir: dir, PageSize: 10}
	m := runToCompletion(t, e2)
	if m.SnapshotID != src.SnapshotID() {
		t.Fatalf("manifest still on stale snapshot %q", m.SnapshotID)
	}
	// Stale pages must not be reused: the whole new snapshot is re-read.
	if got := src.ReadCount() - readsBefore; got != len(m.Pages) {
		t.Fatalf("reused stale pages: read %d pages for %d-page manifest", got, len(m.Pages))
	}
	assertComplete(t, e2, m, wantRecords(src, 25))
}

// TestCorruptPageIsReExported: a damaged page file must be re-read from the
// source; its bytes on disk (and its offset) must not be trusted.
func TestCorruptPageIsReExported(t *testing.T) {
	src := seedSource(25)
	dir, cleanup := tempDir(t)
	defer cleanup()
	e := &Exporter{Source: src, Dir: dir, PageSize: 10}
	m := runToCompletion(t, e)

	// Corrupt the middle page file: flip bytes but keep a plausible offset.
	victim := m.Pages[1]
	path := filepath.Join(dir, victim.File)
	if err := ioutil.WriteFile(path, []byte(`[{"key":"key-0010","value":"FORGED"}]`), 0644); err != nil {
		t.Fatal(err)
	}
	readsBefore := src.ReadCount()

	e2 := &Exporter{Source: src, Dir: dir, PageSize: 10}
	m2 := runToCompletion(t, e2)
	if got := src.ReadCount() - readsBefore; got != 2 {
		t.Fatalf("corruption recovery read %d pages, want 2 (page 1 re-read, page 2 truncated+re-read)", got)
	}
	// The forged bytes must be gone; the page is re-read from the source,
	// so its digest returns to the digest of the true snapshot content.
	forged := sha256Hex(`[{"key":"key-0010","value":"FORGED"}]`)
	if m2.Pages[1].SHA256 == forged {
		t.Fatal("corrupt page content survived recovery")
	}
	data, err := ioutil.ReadFile(filepath.Join(dir, m2.Pages[1].File))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "FORGED") {
		t.Fatal("forged record still on disk after recovery")
	}
	assertComplete(t, e2, m2, wantRecords(src, 25))
}

// TestDeletedPageIsReExported: a missing page file is also re-exported.
func TestDeletedPageIsReExported(t *testing.T) {
	src := seedSource(25)
	dir, cleanup := tempDir(t)
	defer cleanup()
	e := &Exporter{Source: src, Dir: dir, PageSize: 10}
	m := runToCompletion(t, e)
	if err := os.Remove(filepath.Join(dir, m.Pages[0].File)); err != nil {
		t.Fatal(err)
	}
	e2 := &Exporter{Source: src, Dir: dir, PageSize: 10}
	m2 := runToCompletion(t, e2)
	assertComplete(t, e2, m2, wantRecords(src, 25))
}

// TestRepeatedResumeIsIdempotent: resuming a finished export is a no-op and
// repeated resumes converge on the same complete set.
func TestRepeatedResumeIsIdempotent(t *testing.T) {
	src := seedSource(25)
	dir, cleanup := tempDir(t)
	defer cleanup()
	e := &Exporter{Source: src, Dir: dir, PageSize: 10}
	m1 := runToCompletion(t, e)
	want := wantRecords(src, 25)
	readsBefore := src.ReadCount()

	for i := 0; i < 3; i++ {
		eN := &Exporter{Source: src, Dir: dir, PageSize: 10}
		mN := runToCompletion(t, eN)
		if !reflect.DeepEqual(m1, mN) {
			t.Fatalf("resume %d changed the manifest", i)
		}
		assertComplete(t, eN, mN, want)
	}
	if got := src.ReadCount() - readsBefore; got != 0 {
		t.Fatalf("idempotent resumes read %d pages from source, want 0", got)
	}
}

// TestChaosRecovery: interruption + source update + corruption across
// repeated resumes still converges to the exact final snapshot.
func TestChaosRecovery(t *testing.T) {
	src := seedSource(30)
	dir, cleanup := tempDir(t)
	defer cleanup()

	// Run 1: partial export.
	e := &Exporter{Source: src, Dir: dir, PageSize: 7, MaxPagesPerRun: 2}
	if _, err := e.Export(); err != nil {
		t.Fatal(err)
	}
	// Source updates before resume.
	src.Put("key-0001", "v2")
	src.Delete("key-0029")
	// Run 2: partial export of the new snapshot.
	e2 := &Exporter{Source: src, Dir: dir, PageSize: 7, MaxPagesPerRun: 1}
	if _, err := e2.Export(); err != nil {
		t.Fatal(err)
	}
	// Corrupt whatever page 0 now is.
	if err := ioutil.WriteFile(filepath.Join(dir, pageFileName(0)), []byte("garbage"), 0644); err != nil {
		t.Fatal(err)
	}
	// Run 3..n: resume to completion.
	e3 := &Exporter{Source: src, Dir: dir, PageSize: 7}
	m := runToCompletion(t, e3)
	assertComplete(t, e3, m, wantRecords(src, 30))
}
