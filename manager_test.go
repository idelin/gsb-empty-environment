package manifestmgr

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

var (
	blobA = []byte("alpha-content-shared")
	blobB = []byte("beta-content-removed-in-v2")
	blobC = []byte("gamma-content-added-in-v2")
)

func publishV1(t *testing.T, m *Manager) {
	t.Helper()
	v, err := m.Publish(map[string][]byte{"a": blobA, "b": blobB}, nil)
	if err != nil || v != 1 {
		t.Fatalf("publish v1: v=%d err=%v", v, err)
	}
}

// publishV2 removes B, adds C, keeps shared A.
func publishV2(m *Manager) (uint64, error) {
	return m.Publish(map[string][]byte{"c": blobC}, []string{SegmentID(blobB)})
}

// checkConsistent verifies the recovered store: current version fully
// readable via a snapshot, and GC does not remove anything still referenced.
func checkConsistent(t *testing.T, m *Manager) {
	t.Helper()
	cur := m.Current()
	if cur == nil {
		t.Fatal("no current version after recovery")
	}
	snap := m.AcquireSnapshot()
	defer snap.Close()
	if snap.Version != cur.Version {
		t.Fatalf("snapshot version %d != current %d", snap.Version, cur.Version)
	}
	for _, s := range cur.Segments {
		data, err := snap.Read(s.ID)
		if err != nil {
			t.Fatalf("read %s of v%d: %v", s.ID, cur.Version, err)
		}
		if DigestOf(data) != s.Digest {
			t.Fatalf("digest mismatch for %s", s.ID)
		}
	}
	rep, err := m.GC()
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	for _, d := range rep.Deleted {
		id := strings.TrimSuffix(d, ".seg")
		for _, s := range cur.Segments {
			if s.ID == id {
				t.Fatalf("GC deleted live segment %s", id)
			}
		}
	}
	// Re-open after GC: store must still validate.
	if _, err := Open(m.fs); err != nil {
		t.Fatalf("open after GC: %v", err)
	}
}

// TestCrashAtEveryPublishStep enumerates every exit point of the publish
// protocol (write, sync, rename, dir-sync, log append, log sync, root
// dir-sync), crashes there, restarts from the on-disk state, and verifies:
// no half-committed version, no lost old data, no deleted shared segments.
func TestCrashAtEveryPublishStep(t *testing.T) {
	// Measure how many mutating ops a v2 publish performs.
	opsInPublish := func() int {
		ff := NewFaultFS(NewMemFS())
		m, err := Create(ff)
		if err != nil {
			t.Fatal(err)
		}
		publishV1(t, m)
		before := ff.Total
		if _, err := publishV2(m); err != nil {
			t.Fatal(err)
		}
		return ff.Total - before
	}()
	t.Logf("publish performs %d mutating ops; enumerating %d exit points", opsInPublish, opsInPublish+1)

	for crashAt := 0; crashAt <= opsInPublish; crashAt++ {
		t.Run(fmt.Sprintf("exitPoint=%02d", crashAt), func(t *testing.T) {
			ff := NewFaultFS(NewMemFS())
			m, err := Create(ff)
			if err != nil {
				t.Fatal(err)
			}
			publishV1(t, m)

			ff.Hook = CrashAfter(crashAt) // arm the crash
			v2, perr := publishV2(m)

			// Restart: a new manager recovers from the on-disk state.
			ff.Hook = nil
			m2, err := Open(ff)
			if err != nil {
				t.Fatalf("recovery failed: %v", err)
			}
			switch m2.Version() {
			case 1:
				// Old version fully intact; nothing of v2 visible.
				if perr == nil {
					t.Fatalf("publish reported success (v=%d) but v1 is current", v2)
				}
				cur := m2.Current()
				if len(cur.Segments) != 2 {
					t.Fatalf("v1 should have 2 segments, has %d", len(cur.Segments))
				}
			case 2:
				// Whole new version visible, atomically.
				cur := m2.Current()
				if len(cur.Segments) != 2 {
					t.Fatalf("v2 should have 2 segments (A,C), has %d", len(cur.Segments))
				}
				ids := map[string]bool{}
				for _, s := range cur.Segments {
					ids[s.ID] = true
				}
				if !ids[SegmentID(blobA)] || !ids[SegmentID(blobC)] || ids[SegmentID(blobB)] {
					t.Fatalf("v2 segment set wrong: %v", ids)
				}
			default:
				t.Fatalf("half-committed state: version %d", m2.Version())
			}
			checkConsistent(t, m2)
		})
	}
}

// TestTornCommitAppend simulates a crash in the middle of the commit-record
// append: only a byte prefix reaches disk.
func TestTornCommitAppend(t *testing.T) {
	ff := NewFaultFS(NewMemFS())
	m, err := Create(ff)
	if err != nil {
		t.Fatal(err)
	}
	publishV1(t, m)

	ff.TornAppendOn = ff.Counts[OpAppend] + 1 // the very next append (v2 commit)
	ff.TornBytes = 9                          // only 9 of the frame's bytes land
	if _, err := publishV2(m); err == nil {
		t.Fatal("expected torn append to fail the publish")
	}
	ff.TornAppendOn = 0

	m2, err := Open(ff)
	if err != nil {
		t.Fatalf("recovery from torn append: %v", err)
	}
	if m2.Version() != 1 {
		t.Fatalf("version = %d, want 1", m2.Version())
	}
	// The store must accept new commits after truncation.
	if _, err := publishV2(m2); err != nil {
		t.Fatalf("publish after torn-tail recovery: %v", err)
	}
	if m2.Version() != 2 {
		t.Fatalf("version = %d, want 2", m2.Version())
	}
	checkConsistent(t, m2)
}

// TestSnapshotPinsProtectFromGC: a pinned old snapshot keeps its segments
// alive, including ones removed in the current version.
func TestSnapshotPinsProtectFromGC(t *testing.T) {
	ff := NewFaultFS(NewMemFS())
	m, err := Create(ff)
	if err != nil {
		t.Fatal(err)
	}
	publishV1(t, m)
	snap1 := m.AcquireSnapshot() // pins v1
	defer snap1.Close()

	if _, err := publishV2(m); err != nil {
		t.Fatal(err)
	}
	rep, err := m.GC()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Deleted) != 0 {
		t.Fatalf("GC deleted %v while v1 pinned", rep.Deleted)
	}
	// The removed-in-v2 segment is still readable through the old snapshot.
	data, err := snap1.Read(SegmentID(blobB))
	if err != nil {
		t.Fatalf("old snapshot lost segment: %v", err)
	}
	if !bytes.Equal(data, blobB) {
		t.Fatal("old snapshot returned wrong content")
	}

	// After the pin is released, GC collects the unreferenced segment.
	snap1.Close()
	rep, err = m.GC()
	if err != nil {
		t.Fatal(err)
	}
	want := SegmentID(blobB) + ".seg"
	found := false
	for _, d := range rep.Deleted {
		if d == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("GC did not collect unreferenced %s: deleted=%v", want, rep.Deleted)
	}
	// Shared segment A and current segment C survive.
	for _, id := range []string{SegmentID(blobA), SegmentID(blobC)} {
		if !ff.Inner.Exists("segments/" + id + ".seg") {
			t.Fatalf("shared/current segment %s wrongly deleted", id)
		}
	}
	checkConsistent(t, m)
}

// TestSharedSegmentNeverDeleted: segment A is referenced by both v1 and v2;
// no crash point or GC run may remove it while referenced.
func TestSharedSegmentNeverDeleted(t *testing.T) {
	ff := NewFaultFS(NewMemFS())
	m, err := Create(ff)
	if err != nil {
		t.Fatal(err)
	}
	publishV1(t, m)
	if _, err := publishV2(m); err != nil {
		t.Fatal(err)
	}
	rep, err := m.GC()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range rep.Deleted {
		if d == SegmentID(blobA)+".seg" {
			t.Fatal("shared segment deleted")
		}
	}
	if !ff.Inner.Exists("segments/" + SegmentID(blobA) + ".seg") {
		t.Fatal("shared segment missing after GC")
	}
}

// TestOrphanIdentification: unknown files are reported as orphans, never
// deleted, and never mistaken for valid segments.
func TestOrphanIdentification(t *testing.T) {
	ff := NewFaultFS(NewMemFS())
	m, err := Create(ff)
	if err != nil {
		t.Fatal(err)
	}
	publishV1(t, m)

	// Plant an unknown file and a stray temp file in the segments dir.
	if err := ff.Inner.WriteFile("segments/README.txt", []byte("not a segment")); err != nil {
		t.Fatal(err)
	}
	if err := ff.Inner.WriteFile("segments/"+tmpPrefix+"leftover.seg", []byte("partial")); err != nil {
		t.Fatal(err)
	}

	rep, err := m.GC()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Orphans) != 1 || rep.Orphans[0] != "README.txt" {
		t.Fatalf("orphans = %v, want [README.txt]", rep.Orphans)
	}
	if !ff.Inner.Exists("segments/README.txt") {
		t.Fatal("orphan file was deleted")
	}
	// Stray temp file is collected.
	if ff.Inner.Exists("segments/" + tmpPrefix + "leftover.seg") {
		t.Fatal("temp leftover not collected")
	}
	// The orphan is not mistaken for a segment of any version.
	for _, r := range m.Versions() {
		for _, s := range r.Segments {
			if strings.Contains(s.ID, "README") {
				t.Fatal("orphan mistaken for segment")
			}
		}
	}
}

// TestSegmentCorruptionDetected: a tampered or missing segment file fails
// validation on open.
func TestSegmentCorruptionDetected(t *testing.T) {
	ff := NewFaultFS(NewMemFS())
	m, err := Create(ff)
	if err != nil {
		t.Fatal(err)
	}
	publishV1(t, m)
	p := "segments/" + SegmentID(blobA) + ".seg"
	if err := ff.Inner.WriteFile(p, []byte("tampered")); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ff); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("want digest mismatch, got %v", err)
	}
}

func TestSegmentMissingDetected(t *testing.T) {
	ff := NewFaultFS(NewMemFS())
	m, err := Create(ff)
	if err != nil {
		t.Fatal(err)
	}
	publishV1(t, m)
	if err := ff.Inner.Remove("segments/" + SegmentID(blobB) + ".seg"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ff); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("want missing segment error, got %v", err)
	}
}

// TestReopenPersistence: versions survive a plain restart.
func TestReopenPersistence(t *testing.T) {
	ff := NewFaultFS(NewMemFS())
	m, err := Create(ff)
	if err != nil {
		t.Fatal(err)
	}
	publishV1(t, m)
	if _, err := publishV2(m); err != nil {
		t.Fatal(err)
	}
	m2, err := Open(ff)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Version() != 2 || len(m2.Versions()) != 2 {
		t.Fatalf("reopen: version=%d history=%d", m2.Version(), len(m2.Versions()))
	}
	checkConsistent(t, m2)
}

// TestRemoveUnknownSegmentRejected.
func TestRemoveUnknownSegmentRejected(t *testing.T) {
	ff := NewFaultFS(NewMemFS())
	m, err := Create(ff)
	if err != nil {
		t.Fatal(err)
	}
	publishV1(t, m)
	if _, err := m.Publish(nil, []string{"seg-does-not-exist"}); err == nil {
		t.Fatal("removing unknown segment should fail")
	}
	if m.Version() != 1 {
		t.Fatal("failed publish bumped version")
	}
}
