package netsec

import (
	"errors"
	"testing"
)

func TestMergeWithTombstones(t *testing.T) {
	s1, _ := NewSelector(testKey, 2, 2)
	s2, _ := NewSelector(testKey, 2, 2)

	r1 := rec("e1", "u1", "gA", 1, "p1")
	r2 := rec("e2", "u1", "gA", 1, "p2")
	if err := s1.Ingest(r1); err != nil {
		t.Fatal(err)
	}
	if err := s2.Ingest(r2); err != nil {
		t.Fatal(err)
	}
	s2.Withdraw("e2")

	merged, err := Merge(s1, s2)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !merged.IsWithdrawn("e2") {
		t.Fatal("withdrawal tombstone must survive Merge")
	}
	res := merged.Trim()
	assertIDs(t, "kept", res.Kept, []string{"e1"})
	assertIDs(t, "withdrawn", res.Withdrawn, []string{"e2"})

	// Clone over a selector that contains tombstones.
	cloned := merged.Clone()
	if !cloned.IsWithdrawn("e2") {
		t.Fatal("clone must preserve tombstones")
	}
}

func TestTrimRecordsEmptyEventID(t *testing.T) {
	bad := Record{UserID: "u1", Group: "gA", Payload: []byte("p")}
	if _, err := TrimRecords(testKey, []Record{bad}, 1, 1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty EventID: got %v, want ErrInvalidConfig", err)
	}
}

// TestOrphanTombstoneAcrossShards routes a withdrawal to a shard that never
// sees the event while another shard holds the event. The merged state must
// retract the event exactly as a single host would.
func TestOrphanTombstoneAcrossShards(t *testing.T) {
	holder, _ := NewSelector(testKey, 2, 2)
	retractor, _ := NewSelector(testKey, 2, 2)

	r := rec("e1", "u1", "gA", 1, "p1")
	if err := holder.Ingest(r); err != nil {
		t.Fatal(err)
	}
	if retractor.Withdraw("e1") {
		t.Fatal("withdraw on a shard without the event must report false")
	}
	if !retractor.IsWithdrawn("e1") {
		t.Fatal("retractor should retain an orphan tombstone")
	}

	merged, err := Merge(holder, retractor)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !merged.IsWithdrawn("e1") {
		t.Fatal("orphan tombstone must be promoted during Merge")
	}

	res := merged.Trim()
	if len(res.Kept) != 0 || len(res.Withdrawn) != 1 || res.Withdrawn[0].EventID != "e1" {
		t.Fatalf("event should be retracted after merge: %+v", res)
	}

	// Single-host analogue: orphan tombstone then local arrival.
	single, _ := NewSelector(testKey, 2, 2)
	single.Withdraw("e1")
	if err := single.Ingest(r); err != nil {
		t.Fatal(err)
	}
	if !sameResult(single.Trim(), res) {
		t.Fatalf("merged and single-host results differ:\n %+v\n %+v", single.Trim(), res)
	}
}

// TestDigestTieBreaker forces the (digest, EventID) tie path by exercising
// ordering against a zero digest and distinct EventIDs.
func TestDigestTieBreaker(t *testing.T) {
	e1 := entry{record: rec("aaa", "u1", "gA", 1, "p")}
	e2 := entry{record: rec("zzz", "u1", "gA", 1, "p")}

	entries := []entry{e1, e2}
	sortEntriesInPlace(entries)
	if entries[0].record.EventID != "aaa" || entries[1].record.EventID != "zzz" {
		t.Fatalf("EventID tie-break failed: %v", idsOf([]Record{entries[0].record, entries[1].record}))
	}

	sortEntriesInPlace([]entry{{record: rec("only", "u1", "gA", 1, "p")}})
}
