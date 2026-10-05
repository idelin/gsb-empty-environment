package segvm

import (
	"errors"
	"fmt"
	"testing"
)

var (
	segA = []byte("aaa")
	segB = []byte("bbb")
	segC = []byte("ccc")
	segD = []byte("ddd")
)

// setupV1 creates a store at version 1 with segments a and b.
func setupV1(t *testing.T, dir string) {
	t.Helper()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, m, []Change{{Name: "a", Data: segA}, {Name: "b", Data: segB}}, nil)
}

// crashingPublish runs publish v2 (add c,d; remove a) under ff and returns
// whether ErrCrash was panicked.
func crashingPublish(ff *FaultFS, dir string) (crashed bool, err error) {
	m, err := Open(ff, dir)
	if err != nil {
		return false, err
	}
	defer func() {
		if r := recover(); r != nil {
			if errors.Is(r.(error), ErrCrash) {
				crashed = true
				return
			}
			panic(r)
		}
	}()
	_, err = m.Publish(
		[]Change{{Name: "c", Data: segC}, {Name: "d", Data: segD}},
		[]string{"a"},
	)
	return false, err
}

// checkConsistent verifies the store is exactly version 1 or 2, fully
// readable, with no half-applied state.
func checkConsistent(t *testing.T, m *Manager) {
	t.Helper()
	switch m.Version() {
	case 1:
		segs := m.Segments()
		if len(segs) != 2 {
			t.Fatalf("v1 should have 2 segments, got %d", len(segs))
		}
		for name, want := range map[string]string{"a": "aaa", "b": "bbb"} {
			got, err := m.Read(name)
			if err != nil || string(got) != want {
				t.Fatalf("v1 read %s = %q, %v", name, got, err)
			}
		}
	case 2:
		segs := m.Segments()
		if len(segs) != 3 {
			t.Fatalf("v2 should have 3 segments, got %d", len(segs))
		}
		for name, want := range map[string]string{"b": "bbb", "c": "ccc", "d": "ddd"} {
			got, err := m.Read(name)
			if err != nil || string(got) != want {
				t.Fatalf("v2 read %s = %q, %v", name, got, err)
			}
		}
		if _, err := m.Read("a"); err == nil {
			t.Fatal("v2 must not contain a")
		}
	default:
		t.Fatalf("unexpected version %d", m.Version())
	}
	if got := uint64(len(m.Versions())); got != m.Version() {
		t.Fatalf("log has %d records but version is %d", got, m.Version())
	}
}

// TestCrashMatrix enumerates every process-exit point of a publish: the
// publish performs T mutating storage ops; for each k in 1..T we let k
// ops complete, kill the "process", reopen from disk, and verify
// atomicity, old-snapshot survival and shared-segment safety.
func TestCrashMatrix(t *testing.T) {
	// Count the total number of mutating ops in one publish.
	probeDir := t.TempDir()
	setupV1(t, probeDir)
	probe := &FaultFS{Base: OSFS{}}
	if _, err := crashingPublish(probe, probeDir); err != nil {
		t.Fatalf("probe publish: %v", err)
	}
	total := len(probe.Ops)
	t.Logf("publish performs %d mutating ops: %v", total, probe.Ops)

	for k := 1; k <= total; k++ {
		t.Run(fmt.Sprintf("exit_after_op_%02d_%s", k, probe.Ops[k-1]), func(t *testing.T) {
			dir := t.TempDir()
			setupV1(t, dir)

			// An independent reader and an in-process snapshot, both
			// pinned at v1, simulating active readers.
			reader, err := Open(OSFS{}, dir)
			if err != nil {
				t.Fatal(err)
			}
			snap := reader.Snapshot()

			ff := &FaultFS{Base: OSFS{}, CrashAfter: k}
			crashed, err := crashingPublish(ff, dir)
			if !crashed {
				t.Fatalf("expected crash after op %d, got err=%v", k, err)
			}

			// "Restart": reopen purely from disk state.
			m, err := Open(OSFS{}, dir)
			if err != nil {
				t.Fatalf("reopen after crash: %v", err)
			}
			checkConsistent(t, m)

			// The old snapshot and reader must still be fully readable:
			// publish never deletes segment files.
			for name, want := range map[string]string{"a": "aaa", "b": "bbb"} {
				if got, err := snap.Read(name); err != nil || string(got) != want {
					t.Fatalf("snapshot read %s = %q, %v", name, got, err)
				}
				if got, err := reader.Read(name); err != nil || string(got) != want {
					t.Fatalf("reader read %s = %q, %v", name, got, err)
				}
			}

			// GC on the reopened manager must not touch the current
			// version's files; a fresh snapshot stays readable after GC.
			cur := m.Snapshot()
			res, err := m.GC()
			if err != nil {
				t.Fatal(err)
			}
			for name := range m.Segments() {
				if _, err := cur.Read(name); err != nil {
					t.Fatalf("post-GC snapshot read %s: %v", name, err)
				}
			}
			// Shared segment b survives GC in both outcomes.
			if m.Version() == 2 {
				if got, err := m.Read("b"); err != nil || string(got) != "bbb" {
					t.Fatalf("shared segment lost after GC: %q, %v", got, err)
				}
				for _, removed := range res.Removed {
					if removed == digestOf(segB)+".seg" ||
						removed == digestOf(segC)+".seg" ||
						removed == digestOf(segD)+".seg" {
						t.Fatalf("GC wrongly removed live segment %s", removed)
					}
				}
			}
			cur.Close()
			snap.Close()
		})
	}
}

// TestFaultInjectionPerOpKind fails each distinguished operation class and
// verifies the store stays on the old version (or, for a commit-point
// sync error, on either consistent side).
func TestFaultInjectionPerOpKind(t *testing.T) {
	cases := []struct {
		name     string
		kind     OpKind
		nth      int
		wantVers []uint64
	}{
		{"create_tmp", OpCreate, 1, []uint64{1}},
		{"write_tmp", OpWrite, 1, []uint64{1}},
		{"sync_tmp", OpSync, 1, []uint64{1}},
		{"rename_tmp", OpRename, 1, []uint64{1}},
		{"dirsync_segments", OpSyncDir, 1, []uint64{1}},
		{"dirsync_segments_second", OpSyncDir, 2, []uint64{1}},
		// sync #3 is the commit-point fsync of manifest.log: the record
		// bytes were already written, so after "restart" either the old
		// or the fully-applied new version is visible.
		{"sync_log_commit_point", OpSync, 3, []uint64{1, 2}},
		// dirsync #3 is the root-dir sync after the commit point: the
		// version is already durable, so the new version must be visible.
		{"dirsync_root", OpSyncDir, 3, []uint64{2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			setupV1(t, dir)
			ff := &FaultFS{Base: OSFS{}, FailKind: tc.kind, FailNth: tc.nth}
			crashed, err := crashingPublish(ff, dir)
			if crashed {
				t.Fatal("unexpected crash")
			}
			if err == nil {
				t.Fatal("expected injected error")
			}
			m, rerr := Open(OSFS{}, dir)
			if rerr != nil {
				t.Fatalf("reopen: %v", rerr)
			}
			ok := false
			for _, v := range tc.wantVers {
				if m.Version() == v {
					ok = true
				}
			}
			if !ok {
				t.Fatalf("version = %d, want one of %v", m.Version(), tc.wantVers)
			}
			checkConsistent(t, m)
		})
	}
}

// TestTornWriteCrash kills the process in the middle of the log-append
// write: the incomplete trailing record must be tolerated and truncated.
func TestTornWriteCrash(t *testing.T) {
	dir := t.TempDir()
	setupV1(t, dir)

	// Find the op index of the log-append write (the last write).
	probe := &FaultFS{Base: OSFS{}}
	if _, err := crashingPublish(probe, dir); err != nil {
		t.Fatalf("probe: %v", err)
	}
	lastWrite := 0
	for i, op := range probe.Ops {
		if op == OpWrite {
			lastWrite = i + 1
		}
	}

	dir2 := t.TempDir()
	setupV1(t, dir2)
	ff := &FaultFS{Base: OSFS{}, TornAt: lastWrite}
	crashed, err := crashingPublish(ff, dir2)
	if !crashed {
		t.Fatalf("expected torn-write crash, got err=%v", err)
	}
	m, err := Open(OSFS{}, dir2)
	if err != nil {
		t.Fatalf("reopen after torn write: %v", err)
	}
	if m.Version() != 1 {
		t.Fatalf("torn commit record must be discarded, version = %d", m.Version())
	}
	checkConsistent(t, m)
	// Store keeps working after tail repair.
	publish(t, m, []Change{{Name: "c", Data: segC}}, nil)
	if m.Version() != 2 {
		t.Fatalf("version = %d after republish", m.Version())
	}
}

// TestTornSegmentWriteCrash kills the process mid-write of a segment tmp
// file: the partial file must never become visible and GC reclaims it.
func TestTornSegmentWriteCrash(t *testing.T) {
	dir := t.TempDir()
	setupV1(t, dir)
	ff := &FaultFS{Base: OSFS{}, TornAt: 2} // first tmp write
	crashed, err := crashingPublish(ff, dir)
	if !crashed {
		t.Fatalf("expected crash, got err=%v", err)
	}
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if m.Version() != 1 {
		t.Fatalf("version = %d, want 1", m.Version())
	}
	res, err := m.GC()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tmp) != 1 {
		t.Fatalf("expected 1 stale tmp reclaimed, got %v", res.Tmp)
	}
	checkConsistent(t, m)
}
