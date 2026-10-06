package publisher

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"testing"
)

func mustOpen(t *testing.T, fs FS, dir string) *Publisher {
	t.Helper()
	p, err := Open(fs, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return p
}

func mustPublish(t *testing.T, p *Publisher, updates map[string][]byte) {
	t.Helper()
	if err := p.Publish(updates); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func readAll(t *testing.T, s *Snapshot, names ...string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, name := range names {
		data, err := s.Read(name)
		if err != nil {
			t.Fatalf("Read(%s): %v", name, err)
		}
		out[name] = string(data)
	}
	return out
}

func TestPublishAndSnapshotIsolation(t *testing.T) {
	fs := NewFaultFS()
	p := mustOpen(t, fs, "/pub")

	mustPublish(t, p, map[string][]byte{"a": []byte("a1"), "b": []byte("b1")})
	snap1 := p.Snapshot()
	if got := readAll(t, snap1, "a", "b"); !reflect.DeepEqual(got, map[string]string{"a": "a1", "b": "b1"}) {
		t.Fatalf("snap1 = %v", got)
	}

	mustPublish(t, p, map[string][]byte{"a": []byte("a2")})
	snap2 := p.Snapshot()
	if got := readAll(t, snap2, "a", "b"); !reflect.DeepEqual(got, map[string]string{"a": "a2", "b": "b1"}) {
		t.Fatalf("snap2 = %v", got)
	}
	// The pinned snapshot still reads the old version.
	if got := readAll(t, snap1, "a", "b"); !reflect.DeepEqual(got, map[string]string{"a": "a1", "b": "b1"}) {
		t.Fatalf("snap1 after publish = %v", got)
	}
	snap1.Close()
	snap2.Close()

	if _, err := p.Snapshot().Read("missing"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestReopenReadsManifest(t *testing.T) {
	fs := NewFaultFS()
	p := mustOpen(t, fs, "/pub")
	mustPublish(t, p, map[string][]byte{"a": []byte("a1"), "b": []byte("b1")})

	p2 := mustOpen(t, fs, "/pub")
	snap := p2.Snapshot()
	defer snap.Close()
	if got := readAll(t, snap, "a", "b"); !reflect.DeepEqual(got, map[string]string{"a": "a1", "b": "b1"}) {
		t.Fatalf("after reopen = %v", got)
	}
}

// TestCrashAtEveryCutPoint enumerates every mutating cut point of a
// publish (journal writes and syncs, version-file writes and syncs,
// directory syncs, the manifest rename), crashes before each one, and
// verifies recovery leaves either the whole old set or the whole new set.
func TestCrashAtEveryCutPoint(t *testing.T) {
	v1 := map[string][]byte{"a": []byte("a1"), "b": []byte("b1"), "c": []byte("c1")}
	v2 := map[string][]byte{"a": []byte("a2"), "b": []byte("b2"), "c": []byte("c2")}
	wantV1 := map[string]string{"a": "a1", "b": "b1", "c": "c1"}
	wantV2 := map[string]string{"a": "a2", "b": "b2", "c": "c2"}

	// Measure the cut points of the second publish.
	measure := NewFaultFS()
	mp := mustOpen(t, measure, "/pub")
	mustPublish(t, mp, v1)
	measure.ResetOps()
	mustPublish(t, mp, v2)
	ops := measure.Ops()
	t.Logf("publish cut points (%d):", len(ops))
	for i, op := range ops {
		t.Logf("  [%d] %s", i, op)
	}

	for cut := 0; cut <= len(ops); cut++ {
		t.Run(fmt.Sprintf("cut_%03d", cut), func(t *testing.T) {
			fs := NewFaultFS()
			p := mustOpen(t, fs, "/pub")
			mustPublish(t, p, v1)

			fs.ResetOps()
			fs.Arm(cut)
			p.Publish(v2) // may fail midway; error is expected

			fs.Reboot()
			recovered := mustOpen(t, fs, "/pub")
			snap := recovered.Snapshot()
			defer snap.Close()
			got := readAll(t, snap, "a", "b", "c")
			if !reflect.DeepEqual(got, wantV1) && !reflect.DeepEqual(got, wantV2) {
				cutOp := "no crash (clean run)"
				if cut < len(ops) {
					cutOp = ops[cut].String()
				}
				t.Fatalf("mixed versions after crash at cut %d (%s): %v", cut, cutOp, got)
			}

			// The publisher must stay usable after recovery.
			v3 := map[string][]byte{"a": []byte("a3")}
			mustPublish(t, recovered, v3)
			snap3 := recovered.Snapshot()
			defer snap3.Close()
			got3 := readAll(t, snap3, "a", "b", "c")
			if got3["a"] != "a3" {
				t.Fatalf("publish after recovery broken: %v", got3)
			}
			if got3["b"] != got["b"] || got3["c"] != got["c"] {
				t.Fatalf("unrelated files changed: %v (was %v)", got3, got)
			}
		})
	}
}

// TestCrashDuringRecovery crashes while recovery itself is completing a
// committed transaction; a second recovery must finish the job.
func TestCrashDuringRecovery(t *testing.T) {
	v1 := map[string][]byte{"a": []byte("a1"), "b": []byte("b1")}
	v2 := map[string][]byte{"a": []byte("a2"), "b": []byte("b2")}

	fs := NewFaultFS()
	p := mustOpen(t, fs, "/pub")
	mustPublish(t, p, v1)

	// Crash right after the commit record is synced: count ops of a
	// publish up to and including the commit record's sync.
	fs.ResetOps()
	mustPublish(t, p, v2)
	ops := fs.Ops()
	commitIdx := -1
	journalSyncs := 0
	for i, op := range ops {
		if op.Kind == OpSync && op.Path == "/pub/journal" {
			journalSyncs++
			if journalSyncs == 2 { // begin sync, then commit sync
				commitIdx = i
			}
		}
	}
	if commitIdx < 0 {
		t.Fatal("commit sync not found")
	}

	fs2 := NewFaultFS()
	p2 := mustOpen(t, fs2, "/pub")
	mustPublish(t, p2, v1)
	fs2.ResetOps()
	fs2.Arm(commitIdx + 1) // everything through the commit sync lands
	p2.Publish(v2)

	// First recovery attempt crashes partway through the redo.
	fs2.Reboot()
	fs2.ResetOps()
	fs2.Arm(2)
	Open(fs2, "/pub")

	fs2.Reboot()
	recovered := mustOpen(t, fs2, "/pub")
	snap := recovered.Snapshot()
	defer snap.Close()
	want := map[string]string{"a": "a2", "b": "b2"}
	if got := readAll(t, snap, "a", "b"); !reflect.DeepEqual(got, want) {
		t.Fatalf("recovery did not complete committed txn: %v", got)
	}
}

func TestRollbackRemovesStagedFiles(t *testing.T) {
	v1 := map[string][]byte{"a": []byte("a1")}
	v2 := map[string][]byte{"a": []byte("a2"), "b": []byte("b2")}

	fs := NewFaultFS()
	p := mustOpen(t, fs, "/pub")
	mustPublish(t, p, v1)

	// Crash before the commit record: the begin record and possibly some
	// version files are durable, but the txn must roll back.
	fs.ResetOps()
	fs.Arm(3) // begin write+sync, then part of the version files
	p.Publish(v2)

	fs.Reboot()
	recovered := mustOpen(t, fs, "/pub")
	snap := recovered.Snapshot()
	defer snap.Close()
	if got := readAll(t, snap, "a"); !reflect.DeepEqual(got, map[string]string{"a": "a1"}) {
		t.Fatalf("rollback visible state = %v", got)
	}
	if _, err := snap.Read("b"); err != ErrNotFound {
		t.Fatalf("rolled-back file visible: %v", err)
	}
}

func TestOSFSRoundTrip(t *testing.T) {
	dir, err := ioutil.TempDir("", "publisher")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	p := mustOpen(t, OSFS{}, dir)
	mustPublish(t, p, map[string][]byte{"a": []byte("hello"), "b": []byte("world")})
	mustPublish(t, p, map[string][]byte{"a": []byte("hi")})

	p2 := mustOpen(t, OSFS{}, dir)
	snap := p2.Snapshot()
	defer snap.Close()
	a, _ := snap.Read("a")
	b, _ := snap.Read("b")
	if !bytes.Equal(a, []byte("hi")) || !bytes.Equal(b, []byte("world")) {
		t.Fatalf("OSFS round trip: a=%q b=%q", a, b)
	}
}
