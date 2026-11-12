package lsadb

import "testing"

// A MaxAge LSA with no matching database instance is acknowledged and
// discarded; the database must never create an entry for it.
func TestOrphanMaxAgeAckedAndDiscarded(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	orphan := buildLSA(t, neighborID, 1, testLSID, 7, MaxAge, nil)
	d := db.Admit(neighborID, 1, orphan)

	if d.Outcome != OutcomeOrphanMaxAge {
		t.Fatalf("outcome = %s, want orphan-maxage", d.Outcome)
	}
	if d.Replace || d.Remove {
		t.Fatal("orphan MaxAge must not install or remove anything")
	}
	if len(d.Ack) != 1 || d.Ack[0] != orphan {
		t.Fatalf("ack = %v, want the MaxAge LSA acknowledged", d.Ack)
	}
	if len(d.Flood) != 0 {
		t.Fatalf("flood = %v, orphan MaxAge must not be flooded", d.Flood)
	}
	if db.Len() != 0 {
		t.Fatalf("database length = %d, orphan MaxAge must not create an entry", db.Len())
	}
	if _, ok := db.Lookup(orphan.Key()); ok {
		t.Fatal("orphan MaxAge created a database entry")
	}
	if got := db.Stats().OrphanMaxAge.Load(); got != 1 {
		t.Fatalf("orphan_maxage = %d, want 1", got)
	}
}

// A MaxAge LSA matching an existing instance flushes that instance.
func TestMaxAgeFlushesExistingInstance(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	existing := buildLSA(t, neighborID, 1, testLSID, 9, 10, nil)
	db.Put(existing)

	flush := buildLSA(t, neighborID, 1, testLSID, 9, MaxAge, nil)
	d := db.Admit(neighborID, 2, flush)

	if d.Outcome != OutcomeFlush || !d.Remove {
		t.Fatalf("outcome = %s remove = %v, want flush", d.Outcome, d.Remove)
	}
	if _, ok := db.Lookup(existing.Key()); ok {
		t.Fatal("flushed instance must be removed from the database")
	}
	if len(d.Flood) != 1 || d.Flood[0] != flush {
		t.Fatalf("flood = %v, want the MaxAge LSA flooded", d.Flood)
	}
	if got := db.Stats().Flushed.Load(); got != 1 {
		t.Fatalf("flushed = %d, want 1", got)
	}
}
