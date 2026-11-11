package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSequenceReuseGuardsStaleRecords deletes an object, then reuses its
// reference number under a new sequence. Records carrying the old sequence
// must never mutate the new occupant, and stale rename halves cannot hijack
// the new object's directory entry.
func TestSequenceReuseGuardsStaleRecords(t *testing.T) {
	p := openTestProcessor(t)
	oldID, newID := id(5, 1), id(5, 2)
	key := lk(1, "slot")

	if _, err := p.ApplyBatch([]Record{rec(100, ReasonCreate, oldID, 1, "slot")}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ApplyBatch([]Record{rec(101, ReasonDelete, oldID, 1, "slot")}); err != nil {
		t.Fatal(err)
	}

	// Stale records from the dead sequence-1 object.
	if _, err := p.ApplyBatch([]Record{
		rec(102, ReasonLinkRemove, oldID, 1, "slot"),
		rec(103, ReasonLinkAdd, oldID, 1, "ghost"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Snapshot().LookupEntry(lk(1, "ghost")); ok {
		t.Fatal("stale link-add from a deleted object was applied")
	}

	// Same reference number, bumped sequence: a genuinely new object.
	if _, err := p.ApplyBatch([]Record{rec(104, ReasonCreate, newID, 1, "slot")}); err != nil {
		t.Fatal(err)
	}
	snap := p.Snapshot()
	owner, ok := snap.LookupEntry(key)
	if !ok || owner != newID {
		t.Fatalf("slot owner = %v ok=%v, want new identity %v", owner, ok, newID)
	}
	if _, oldAlive := snap.LookupObject(oldID); oldAlive {
		t.Fatal("deleted old identity still alive")
	}
	// Delete moves the identity to the graveyard; the two records above then
	// hit the graveyard and are counted as stale.
	if st := p.Stats(); st.StaleSkipped != 2 {
		t.Fatalf("stale skips = %d, want 2", st.StaleSkipped)
	}

	// Rename halves addressed to the dead seq-1 identity cannot pair onto seq-2.
	oldHalf, newHalf := renamePair(105, 106, oldID, key, lk(1, "hijack"))
	if _, err := p.ApplyBatch([]Record{oldHalf, newHalf}); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Snapshot().LookupEntry(lk(1, "hijack")); ok {
		t.Fatal("stale rename moved the entry of the new object")
	}
	if owner, _ := p.Snapshot().LookupEntry(key); owner != newID {
		t.Fatal("new object's entry was disturbed by stale rename records")
	}
}

// TestHardLinkSet exercises multi-link objects: links accumulate, a rename
// moves only one directory entry while the others remain, link removal prunes
// one entry, and delete tears the whole hard-link set down.
func TestHardLinkSet(t *testing.T) {
	p := openTestProcessor(t)
	fid := id(11, 3)
	k1, k2, k3 := lk(1, "one"), lk(1, "two"), lk(2, "three")

	if _, err := p.ApplyBatch([]Record{
		rec(100, ReasonCreate, fid, 1, "one"),
		rec(101, ReasonLinkAdd, fid, 1, "two"),
		rec(102, ReasonLinkAdd, fid, 2, "three"),
	}); err != nil {
		t.Fatal(err)
	}
	links, _ := p.Snapshot().Links(fid)
	if len(links) != 3 {
		t.Fatalf("link set = %v, want 3 entries", links)
	}

	oldHalf, newHalf := renamePair(103, 104, fid, k2, lk(1, "two-moved"))
	if _, err := p.ApplyBatch([]Record{newHalf, oldHalf}); err != nil {
		t.Fatal(err)
	}
	snap := p.Snapshot()
	if _, ok := snap.LookupEntry(k2); ok {
		t.Fatal("renamed link still present under old name")
	}
	for _, key := range []LinkKey{k1, k3, lk(1, "two-moved")} {
		if owner, ok := snap.LookupEntry(key); !ok || owner != fid {
			t.Fatalf("entry %v owner = %v ok=%v, want %v", key, owner, ok, fid)
		}
	}
	if links, _ = snap.Links(fid); len(links) != 3 {
		t.Fatalf("link count after rename = %d, want 3", len(links))
	}

	if _, err := p.ApplyBatch([]Record{rec(105, ReasonLinkRemove, fid, 1, "one")}); err != nil {
		t.Fatal(err)
	}
	if links, _ = p.Snapshot().Links(fid); len(links) != 2 {
		t.Fatalf("link count after remove = %d, want 2", len(links))
	}
	if _, ok := p.Snapshot().LookupEntry(k1); ok {
		t.Fatal("removed entry still resolves")
	}

	if _, err := p.ApplyBatch([]Record{rec(106, ReasonDelete, fid, 0, "")}); err != nil {
		t.Fatal(err)
	}
	snap = p.Snapshot()
	if snap.ObjectCount() != 0 || snap.EntryCount() != 0 {
		t.Fatalf("after delete objects=%d entries=%d, want 0/0", snap.ObjectCount(), snap.EntryCount())
	}
}

// TestCheckpointAtomicAndRestartable checks that checkpoint writes leave no
// temp files behind and that every persisted state (including unpaired rename
// halves and the graveyard) round-trips through JSON.
func TestCheckpointAtomicAndRestartable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")
	p, err := OpenProcessor(path, testJournalID, 100)
	if err != nil {
		t.Fatal(err)
	}
	fid := id(20, 1)
	if _, err := p.ApplyBatch([]Record{rec(100, ReasonCreate, fid, 1, "keep")}); err != nil {
		t.Fatal(err)
	}
	oldHalf, _ := renamePair(101, 102, fid, lk(1, "keep"), lk(1, "moved"))
	if _, err := p.ApplyBatch([]Record{oldHalf}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(raw, &cp); err != nil {
		t.Fatalf("checkpoint not valid json: %v", err)
	}
	if cp.NextUSN != 102 || len(cp.Index.PendingOld) != 1 {
		t.Fatalf("checkpoint next=%d pendingOld=%d", cp.NextUSN, len(cp.Index.PendingOld))
	}
	if _, ok := cp.Index.Entries[lk(1, "keep")]; !ok {
		t.Fatal("checkpoint lost the directory entry (map key text encoding broken)")
	}

	p2, err := OpenProcessor(path, testJournalID, 100)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if p2.NextUSN() != 102 || p2.PendingRenames() != 1 {
		t.Fatalf("restored next=%d pending=%d", p2.NextUSN(), p2.PendingRenames())
	}
	if owner, ok := p2.Snapshot().LookupEntry(lk(1, "keep")); !ok || owner != fid {
		t.Fatal("restored index lost the entry")
	}

	// Re-delivering already-applied USNs must be a no-op, not an error.
	if n, err := p2.ApplyBatch([]Record{oldHalf, rec(100, ReasonCreate, fid, 1, "keep")}); err != nil || n != 0 {
		t.Fatalf("replay n=%d err=%v, want 0/nil", n, err)
	}
}

// TestTruncatedFreshOpen documents the error returned when an existing
// checkpoint's resume USN is already outside the retained log range.
func TestTruncatedFreshOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cp.json")
	p, err := OpenProcessor(path, testJournalID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ApplyBatch([]Record{rec(100, ReasonCreate, id(1, 1), 1, "a")}); err != nil {
		t.Fatal(err)
	}
	// Checkpoint resumes at USN 101; once the retained log starts at 102 that
	// record is gone and continuation is impossible.
	if _, err := OpenProcessor(path, testJournalID, 102); err == nil {
		t.Fatal("expected ErrGap when log was truncated past checkpoint")
	}
}
