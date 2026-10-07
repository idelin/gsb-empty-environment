package encobj

import (
	"errors"
	"sync/atomic"
	"testing"
)

func upgradeToK2(t *testing.T, st *Store) {
	t.Helper()
	if err := st.RegisterKey("k2", testKey2[:]); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActiveKey("k2"); err != nil {
		t.Fatal(err)
	}
}

func TestRotateBasic(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	defer st.Close()
	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{
		"a": []byte("A"), "b": []byte("B"),
	}})
	upgradeToK2(t, st)
	n, err := st.RotateAll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rotated = %d, want 1", n)
	}
	// Version unchanged by rotation; content intact; everything on k2.
	sn := mustSnapshot(t, st, "o")
	if sn.Version() != 1 {
		t.Fatalf("version = %d, want 1", sn.Version())
	}
	if got, _ := sn.Get("a"); string(got) != "A" {
		t.Fatalf("a = %q", got)
	}
	sn.Close()
	o := st.obj("o")
	o.mu.Lock()
	manifestKey := o.m.KeyID
	fieldKeys := map[string]string{}
	for _, f := range o.m.Fields {
		fieldKeys[f.Name] = f.KeyID
	}
	o.mu.Unlock()
	if manifestKey != "k2" {
		t.Fatalf("manifest key = %s", manifestKey)
	}
	for name, k := range fieldKeys {
		if k != "k2" {
			t.Fatalf("field %s key = %s", name, k)
		}
	}
	// Second pass is a no-op.
	if n, _ := st.RotateAll(); n != 0 {
		t.Fatalf("second RotateAll = %d, want 0", n)
	}
}

// Acceptance: rotation reads version 3, foreground commits version 4, the
// stale rotation candidate must not overwrite version 4.
func TestRotateLosesToForegroundCommit(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	defer st.Close()
	for i, v := range []string{"one", "two", "three"} {
		mustCommit(t, st, "o", Operation{
			OpID: "op" + v,
			Set:  map[string][]byte{"data": []byte(v), "idx": []byte{byte('1' + i)}},
		})
	}
	upgradeToK2(t, st)

	// Fire exactly once, after the v3 candidate fields are staged and
	// before the candidate manifest is written (no object lock held at
	// this point): commit version 4 in the foreground.
	var committed atomic.Bool
	st.hookPersist = func(p string) error {
		if p == "rotate.manifest.write" && committed.CompareAndSwap(false, true) {
			st.hookPersist = nil // disarm before re-entering Commit
			if _, err := st.Commit("o", Operation{
				OpID: "op4",
				Set:  map[string][]byte{"data": []byte("four")},
			}); err != nil {
				t.Errorf("foreground commit: %v", err)
			}
		}
		return nil
	}
	if _, err := st.RotateObject("o"); err != nil {
		t.Fatalf("RotateObject: %v", err)
	}
	if !committed.Load() {
		t.Fatal("foreground commit never injected")
	}

	// Version 4 with its own content must survive; the v3 candidate is
	// discarded. The retry inside RotateObject re-rotates v4 onto k2.
	sn := mustSnapshot(t, st, "o")
	defer sn.Close()
	if sn.Version() != 4 {
		t.Fatalf("version = %d, want 4 (stale candidate overwrote!)", sn.Version())
	}
	if got, _ := sn.Get("data"); string(got) != "four" {
		t.Fatalf("data = %q, want %q", got, "four")
	}
	o := st.obj("o")
	o.mu.Lock()
	manifestKey := o.m.KeyID
	var stale []string
	for _, f := range o.m.Fields {
		if f.KeyID != "k2" {
			stale = append(stale, f.Name)
		}
	}
	o.mu.Unlock()
	if manifestKey != "k2" {
		t.Fatalf("manifest key = %s, want k2 after retry", manifestKey)
	}
	if len(stale) > 0 {
		t.Fatalf("fields still on k1: %v", stale)
	}
}

// Acceptance: while an old snapshot is open, its key cannot be retired.
func TestOldSnapshotPinsKey(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	defer st.Close()
	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{"a": []byte("A")}})

	sn := mustSnapshot(t, st, "o") // leases k1
	upgradeToK2(t, st)
	if _, err := st.RotateAll(); err != nil {
		t.Fatal(err)
	}
	if err := st.RetireKey("k1"); !errors.Is(err, ErrKeyInUse) {
		t.Fatalf("retire with live snapshot = %v, want ErrKeyInUse", err)
	}
	// The old snapshot is still fully readable.
	if got, _ := sn.Get("a"); string(got) != "A" {
		t.Fatalf("snapshot a = %q", got)
	}
	sn.Close()
	if err := st.RetireKey("k1"); err != nil {
		t.Fatalf("retire after close: %v", err)
	}
	// Retired keys can no longer decrypt; current data is on k2 and reads
	// fine.
	sn2 := mustSnapshot(t, st, "o")
	if got, _ := sn2.Get("a"); string(got) != "A" {
		t.Fatalf("a = %q", got)
	}
	sn2.Close()
	if err := st.SetActiveKey("k1"); !errors.Is(err, ErrKeyRetired) {
		t.Fatalf("reactivate retired key = %v", err)
	}
}

// A snapshot taken before rotation keeps the old manifest alive; files are
// reclaimed only after it closes.
func TestSnapshotAcrossRotationKeepsFiles(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	defer st.Close()
	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{"a": []byte("A")}})
	sn := mustSnapshot(t, st, "o")
	upgradeToK2(t, st)
	if _, err := st.RotateAll(); err != nil {
		t.Fatal(err)
	}
	// Old manifest file must still exist while pinned.
	names, _ := st.st.ReadFileNames()
	mfCount := 0
	for _, n := range names {
		if len(n) > 0 && n[len(n)-3:] == ".mf" {
			mfCount++
		}
	}
	if mfCount != 2 {
		t.Fatalf("manifest files = %d, want 2 (old pinned + new)", mfCount)
	}
	sn.Close()
	names, _ = st.st.ReadFileNames()
	mfCount = 0
	for _, n := range names {
		if len(n) > 3 && n[len(n)-3:] == ".mf" {
			mfCount++
		}
	}
	if mfCount != 1 {
		t.Fatalf("manifest files after close = %d, want 1", mfCount)
	}
}

// Rotation never resurrects a deleted object.
func TestRotateDeletedObject(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir, nil)
	defer st.Close()
	mustCommit(t, st, "o", Operation{OpID: "op1", Set: map[string][]byte{"a": []byte("A")}})
	if err := st.Delete("o"); err != nil {
		t.Fatal(err)
	}
	upgradeToK2(t, st)
	n, err := st.RotateAll()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("rotated %d objects, want 0", n)
	}
	if _, err := st.Snapshot("o"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("snapshot after delete+rotate: %v", err)
	}
}
