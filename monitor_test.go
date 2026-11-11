package netsec

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// stream simulates the kernel notification queue's sequence space.
type stream struct{ seq uint64 }

func (s *stream) next() uint64 { s.seq++; return s.seq }

func writeFile(t *testing.T, root, rel, data string) uint64 {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return objectIDOf(info)
}

// Overflow without any scan in flight must immediately revoke the clean
// verdict; a completed recovery scan restores it.
func TestOverflowRevokesCleanVerdict(t *testing.T) {
	m := NewMonitor()
	var st stream

	barrier := m.BeginScan()
	if !m.CompleteScan(barrier, []Entry{{Path: "a", ObjectID: 1}}) {
		t.Fatal("initial scan rejected")
	}
	if !m.Clean() {
		t.Fatal("expected clean after initial scan")
	}

	m.HandleOverflow(st.next())
	if !m.Lost() {
		t.Fatal("overflow must set lost")
	}
	if m.Clean() {
		t.Fatal("overflow must revoke the clean verdict")
	}

	barrier = m.BeginScan()
	if !m.CompleteScan(barrier, []Entry{{Path: "a", ObjectID: 1}}) {
		t.Fatal("recovery scan rejected")
	}
	if m.Lost() || !m.Clean() {
		t.Fatal("recovery scan must restore the clean verdict")
	}
}

// Two overflows in a row, each landing mid-scan via the pausable
// scanner, must reject both scans; the third, quiet scan succeeds.
func TestConsecutiveOverflows(t *testing.T) {
	root := t.TempDir()
	id := writeFile(t, root, "a", "v1")

	m := NewMonitor()
	var st stream

	// Attempts 1 and 2 overflow while the walk is paused on "a".
	for attempt := 1; attempt <= 2; attempt++ {
		barrier := m.BeginScan()
		var once sync.Once
		sc := &Scanner{Root: root}
		sc.BeforeEntry = func(rel string) {
			once.Do(func() { m.HandleOverflow(st.next()) })
		}
		entries, err := sc.Scan()
		if err != nil {
			t.Fatal(err)
		}
		if m.CompleteScan(barrier, entries) {
			t.Fatalf("attempt %d: scan accepted despite mid-scan overflow", attempt)
		}
		if !m.Lost() {
			t.Fatalf("attempt %d: lost must survive the rejected scan", attempt)
		}
	}

	// Attempt 3 runs to completion without overflow.
	barrier := m.BeginScan()
	entries, err := (&Scanner{Root: root}).Scan()
	if err != nil {
		t.Fatal(err)
	}
	if !m.CompleteScan(barrier, entries) {
		t.Fatal("quiet scan must be accepted")
	}
	if m.Lost() || !m.Clean() {
		t.Fatal("quiet scan must clear lost and restore clean")
	}
	if got, ok := m.State("a"); !ok || got.ObjectID != id {
		t.Fatalf("snapshot not installed: %+v ok=%v want id=%d", got, ok, id)
	}
}

// An event delivered in the scan-end window (after the last entry,
// before completion) must be merged onto the snapshot, not dropped.
func TestScanEndRace(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a", "v1")
	idB := writeFile(t, root, "b", "v1")

	m := NewMonitor()
	var st stream
	barrier := m.BeginScan()

	sc := &Scanner{Root: root}
	sc.BeforeFinish = func() {
		// Racer: b changes right as the scan finishes.
		writeFile(t, root, "b", "v2")
		m.HandleEvent(Event{Seq: st.next(), Op: OpWrite, Path: "b", ObjectID: idB})
	}
	entries, err := sc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if !m.CompleteScan(barrier, entries) {
		t.Fatal("scan without overflow must be accepted")
	}
	if m.Lost() {
		t.Fatal("no overflow happened; lost must be clear")
	}
	if !m.Dirty("b") {
		t.Fatal("scan-end event on b was lost")
	}
	if m.Dirty("a") {
		t.Fatal("a was untouched and must stay clean")
	}
	if m.Clean() {
		t.Fatal("dirty b must prevent the clean verdict")
	}
}

// A rename observed mid-scan must be applied by object ID: the source
// path, reused by a brand-new object before the scan completes, must
// not be removed by the stale rename delta.
func TestRenameDuringScanBindsObjectID(t *testing.T) {
	root := t.TempDir()
	idOld := writeFile(t, root, "old", "payload")

	m := NewMonitor()
	var st stream

	// Baseline: only "old" exists.
	barrier := m.BeginScan()
	entries, err := (&Scanner{Root: root}).Scan()
	if err != nil {
		t.Fatal(err)
	}
	if !m.CompleteScan(barrier, entries) {
		t.Fatal("baseline scan rejected")
	}

	// Recovery scan; while paused, rename old->new and recreate "old"
	// as a different object (path reuse).
	barrier = m.BeginScan()
	var idReused uint64
	var once sync.Once
	sc := &Scanner{Root: root}
	sc.BeforeEntry = func(rel string) {
		once.Do(func() {
			if err := os.Rename(filepath.Join(root, "old"), filepath.Join(root, "new")); err != nil {
				t.Fatal(err)
			}
			m.HandleEvent(Event{Seq: st.next(), Op: OpRename, OldPath: "old", Path: "new", ObjectID: idOld})
			idReused = writeFile(t, root, "old", "different object")
			m.HandleEvent(Event{Seq: st.next(), Op: OpCreate, Path: "old", ObjectID: idReused})
		})
	}
	entries, err = sc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if !m.CompleteScan(barrier, entries) {
		t.Fatal("scan rejected")
	}

	stOld, ok := m.State("old")
	if !ok {
		t.Fatal("reused path old must survive the rename delta")
	}
	if stOld.ObjectID != idReused {
		t.Fatalf("old bound to object %d, want reused object %d", stOld.ObjectID, idReused)
	}
	stNew, ok := m.State("new")
	if !ok || stNew.ObjectID != idOld {
		t.Fatalf("new = %+v ok=%v, want object %d", stNew, ok, idOld)
	}
	if got := m.Paths(idOld); len(got) != 1 || got[0] != "new" {
		t.Fatalf("object %d paths = %v, want [new]", idOld, got)
	}
	if got := m.Paths(idReused); len(got) != 1 || got[0] != "old" {
		t.Fatalf("object %d paths = %v, want [old]", idReused, got)
	}
}

// A write through one hardlink must dirty every name of the object.
func TestHardlinkChange(t *testing.T) {
	root := t.TempDir()
	id := writeFile(t, root, "a", "shared")
	if err := os.Link(filepath.Join(root, "a"), filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}

	m := NewMonitor()
	var st stream
	barrier := m.BeginScan()
	entries, err := (&Scanner{Root: root}).Scan()
	if err != nil {
		t.Fatal(err)
	}
	if !m.CompleteScan(barrier, entries) {
		t.Fatal("initial scan rejected")
	}
	if !m.Clean() {
		t.Fatal("expected clean after initial scan")
	}

	// Mutate via name a; the kernel reports the object's ID.
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.HandleEvent(Event{Seq: st.next(), Op: OpWrite, Path: "a", ObjectID: id})

	if !m.Dirty("a") {
		t.Fatal("a must be dirty")
	}
	if !m.Dirty("b") {
		t.Fatal("hardlink b shares the object and must be dirty too")
	}
	if m.Clean() {
		t.Fatal("dirty object must prevent the clean verdict")
	}
}
