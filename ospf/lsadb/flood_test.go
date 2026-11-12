package lsadb

import "testing"

// A previously unknown foreign LSA is installed, flooded and acknowledged.
func TestLegalFloodAcceptsNewLSA(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	lsa := buildLSA(t, neighborID, 1, testLSID, InitSequenceNumber, 10, nil)
	d := db.Admit(neighborID, 1, lsa)

	if d.Outcome != OutcomeAccept || !d.Replace {
		t.Fatalf("outcome = %s replace = %v, want accept", d.Outcome, d.Replace)
	}
	if len(d.Flood) != 1 || d.Flood[0] != lsa {
		t.Fatalf("flood = %v, want the new LSA", d.Flood)
	}
	if len(d.Ack) != 1 || d.Ack[0] != lsa {
		t.Fatalf("ack = %v, want the new LSA acknowledged", d.Ack)
	}
	stored, ok := db.Lookup(lsa.Key())
	if !ok || stored != lsa {
		t.Fatal("accepted LSA must be installed")
	}
	if db.Stats().Accepted.Load() != 1 {
		t.Fatalf("accepted = %d, want 1", db.Stats().Accepted.Load())
	}
}

// A higher-sequence foreign LSA replaces the stored instance.
func TestLegalFloodReplacesOlder(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	old := buildLSA(t, neighborID, 1, testLSID, 50, 10, nil)
	db.Put(old)

	newer := buildLSA(t, neighborID, 1, testLSID, 51, 2, nil)
	d := db.Admit(neighborID, 2, newer)
	if d.Outcome != OutcomeAccept || !d.Replace || d.Instance != newer {
		t.Fatalf("outcome = %s, want accept/replace", d.Outcome)
	}
	stored, ok := db.Lookup(old.Key())
	if !ok || stored != newer {
		t.Fatal("newer LSA must replace the old instance")
	}
}

// The same instance is acknowledged but not flooded again.
func TestLegalFloodDuplicate(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	lsa := buildLSA(t, neighborID, 1, testLSID, 50, 10, nil)
	db.Put(lsa)

	dup := buildLSA(t, neighborID, 1, testLSID, 50, 10, nil)
	d := db.Admit(neighborID, 3, dup)
	if d.Outcome != OutcomeDuplicate {
		t.Fatalf("outcome = %s, want duplicate", d.Outcome)
	}
	if len(d.Flood) != 0 {
		t.Fatalf("flood = %v, duplicates must not be re-flooded", d.Flood)
	}
	if len(d.Ack) != 1 {
		t.Fatalf("ack = %v, duplicates must be acknowledged", d.Ack)
	}
	if db.Stats().Duplicates.Load() != 1 {
		t.Fatalf("duplicates = %d, want 1", db.Stats().Duplicates.Load())
	}
}

// An older foreign LSA is answered with the stored newer instance.
func TestLegalFloodOlderRepliesWithCurrent(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	current := buildLSA(t, neighborID, 1, testLSID, 80, 10, nil)
	db.Put(current)

	stale := buildLSA(t, neighborID, 1, testLSID, 79, 30, nil)
	d := db.Admit(neighborID, 4, stale)
	if d.Outcome != OutcomeOlder {
		t.Fatalf("outcome = %s, want older", d.Outcome)
	}
	if len(d.Flood) != 1 || d.Flood[0] != current {
		t.Fatalf("flood = %v, want the current instance sent back", d.Flood)
	}
	if len(d.Ack) != 0 {
		t.Fatalf("ack = %v, older LSAs are not acknowledged", d.Ack)
	}
	if db.Stats().Older.Load() != 1 {
		t.Fatalf("older = %d, want 1", db.Stats().Older.Load())
	}
}

func TestAdmitRejectsInvalidLSA(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	valid := buildLSA(t, neighborID, 1, testLSID, 10, 10, nil)
	corrupt := make([]byte, len(valid.raw))
	copy(corrupt, valid.raw)
	corrupt[HeaderLength] ^= 0xff

	parsed, err := ParseLSA(corrupt)
	if err == nil {
		t.Fatal("expected ParseLSA to reject the corrupted LSA")
	}
	// Build the invalid LSA directly to exercise validateLSA as well.
	invalid := buildLSA(t, neighborID, 1, testLSID, 10, 10, nil)
	invalid.raw[HeaderLength] ^= 0xff

	d := db.Admit(neighborID, 9, invalid)
	if d.Outcome != OutcomeInvalid || !errorIs(d.Err, ErrChecksum) {
		t.Fatalf("outcome = %s err = %v, want invalid/ErrChecksum", d.Outcome, d.Err)
	}
	if db.Len() != 0 {
		t.Fatalf("database length = %d, invalid LSA must not be installed", db.Len())
	}

	// Keep the earlier parse result referenced to document the wire path.
	_ = parsed
}
