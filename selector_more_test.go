package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func TestIdempotentRetransmission(t *testing.T) {
	s, _ := NewSelector(testKey, 1, 1)
	r := rec("e1", "u1", "gA", 1, "payload")

	for i := 0; i < 3; i++ {
		if err := s.Ingest(r); err != nil {
			t.Fatalf("retransmission %d: %v", i, err)
		}
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}

	res := s.Trim()
	assertIDs(t, "kept", res.Kept, []string{"e1"})
	if len(res.Dropped) != 0 {
		t.Fatalf("unexpected drops: %v", res.Dropped)
	}
}

func TestConflictRejected(t *testing.T) {
	s, _ := NewSelector(testKey, 2, 2)
	original := rec("e1", "u1", "gA", 1, "original")

	if err := s.Ingest(original); err != nil {
		t.Fatalf("Ingest original: %v", err)
	}
	if err := s.Ingest(rec("e1", "u1", "gA", 1, "tampered")); !errors.Is(err, ErrConflict) {
		t.Fatalf("payload conflict: got %v, want ErrConflict", err)
	}

	variants := []Record{
		rec("e1", "u2", "gA", 1, "original"),
		rec("e1", "u1", "gB", 1, "original"),
		rec("e1", "u1", "gA", 2, "original"),
	}
	for i, v := range variants {
		if err := s.Ingest(v); !errors.Is(err, ErrConflict) {
			t.Fatalf("variant %d: got %v, want ErrConflict", i, err)
		}
	}

	res := s.Trim()
	assertIDs(t, "kept after conflict", res.Kept, []string{"e1"})
	if !bytes.Equal(res.Kept[0].Payload, original.Payload) {
		t.Fatalf("original record mutated by conflicting write")
	}

	if err := s.Ingest(Record{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty EventID: got %v", err)
	}
}

func TestWithdraw(t *testing.T) {
	s, _ := NewSelector(testKey, 2, 2)
	r1 := rec("e1", "u1", "gA", 1, "p1")
	r2 := rec("e2", "u1", "gA", 1, "p2")

	if err := s.Ingest(r1); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(r2); err != nil {
		t.Fatal(err)
	}

	if !s.Withdraw("e1") {
		t.Fatal("first withdraw should report true")
	}
	if s.Withdraw("e1") {
		t.Fatal("duplicate withdraw should report false")
	}
	if s.Withdraw("missing") {
		t.Fatal("withdraw of unknown event should report false")
	}
	if !s.IsWithdrawn("e1") {
		t.Fatal("e1 should be withdrawn")
	}

	if err := s.Ingest(r1); err != nil {
		t.Fatalf("retransmission after withdraw: %v", err)
	}

	res := s.Trim()
	assertIDs(t, "kept", res.Kept, []string{"e2"})
	assertIDs(t, "withdrawn", res.Withdrawn, []string{"e1"})
	if len(res.Dropped) != 0 {
		t.Fatalf("withdrawn event must not appear as dropped: %v", res.Dropped)
	}
}

func TestWithdrawFreesSlot(t *testing.T) {
	s, _ := NewSelector(testKey, 1, 1)
	records := []Record{
		rec("e1", "u1", "gA", 1, "p1"),
		rec("e2", "u1", "gA", 1, "p2"),
	}
	for _, r := range records {
		if err := s.Ingest(r); err != nil {
			t.Fatal(err)
		}
	}

	before := s.Trim()
	if len(before.Kept) != 1 || len(before.Dropped) != 1 {
		t.Fatalf("before withdraw: kept=%d dropped=%d", len(before.Kept), len(before.Dropped))
	}

	s.Withdraw(before.Kept[0].EventID)
	after := s.Trim()
	if len(after.Kept) != 1 || after.Kept[0].EventID != before.Dropped[0].Record.EventID {
		t.Fatalf("slot not freed: kept=%v dropped=%v", idsOf(after.Kept), after.Dropped)
	}
	if len(after.Withdrawn) != 1 || after.Withdrawn[0].EventID != before.Kept[0].EventID {
		t.Fatalf("tombstone missing: %v", idsOf(after.Withdrawn))
	}
}

func TestDigestExposure(t *testing.T) {
	s, _ := NewSelector(testKey, 1, 1)
	r := rec("e1", "u1", "gA", 1, "p1")
	if err := s.Ingest(r); err != nil {
		t.Fatal(err)
	}

	got, ok := s.Digest("e1")
	if !ok {
		t.Fatal("digest lookup failed")
	}
	if want := KeyedDigest(testKey, r); got != want {
		t.Fatalf("digest mismatch: got %x want %x", got, want)
	}
	if _, ok := s.Digest("missing"); ok {
		t.Fatal("unknown event must report false")
	}
}

func TestTrimRecordsStateless(t *testing.T) {
	r1 := rec("e1", "u1", "gA", 1, "p1")
	r2 := rec("e2", "u1", "gA", 1, "p2")

	res, err := TrimRecords(testKey, []Record{r1, r2, r1}, 1, 1)
	if err != nil {
		t.Fatalf("TrimRecords: %v", err)
	}
	if len(res.Kept) != 1 || len(res.Dropped) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}

	if _, err := TrimRecords(testKey, []Record{r1, rec("e1", "u9", "gZ", 9, "x")}, 1, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict: got %v", err)
	}
	if _, err := TrimRecords(nil, []Record{r1}, 1, 1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("bad key: got %v", err)
	}
}

func TestMultipleUserWindowsIsolated(t *testing.T) {
	s, _ := NewSelector(testKey, 1, 2)
	records := []Record{
		rec("u1w1a", "u1", "gA", 1, "p"),
		rec("u1w1b", "u1", "gB", 1, "p"),
		rec("u1w2a", "u1", "gA", 2, "p"),
		rec("u2w1a", "u2", "gA", 1, "p"),
		rec("u2w1b", "u2", "gB", 1, "p"),
	}
	for _, r := range records {
		if err := s.Ingest(r); err != nil {
			t.Fatal(err)
		}
	}

	res := s.Trim()
	if len(res.Kept) != len(records) {
		t.Fatalf("each (user, window) has at most 2 records within caps: kept=%d of %d", len(res.Kept), len(records))
	}
}
