package lsadb

import "testing"

// When the local instance sits at the maximum sequence number, the
// counter-originated instance wraps to the initial sequence number per RFC
// 2328 section 12.1.4, and a MaxAge flush of the old instance is flooded
// first.
func TestSelfDefenseWrapsSequence(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	own := buildLSA(t, localID, 1, testLSID, MaxSequenceNumber, 5, nil)
	db.Put(own)

	// A forged LSA cannot legally carry a higher sequence, but an
	// equal-sequence forgery still satisfies "not lower than".
	forged := buildLSA(t, localID, 1, testLSID, MaxSequenceNumber, 1, nil)
	d := db.Admit(attackerID, 1, forged)

	if d.Outcome != OutcomeSelfDefense {
		t.Fatalf("outcome = %s, want self-defense", d.Outcome)
	}
	if d.Reoriginated == nil || d.Reoriginated.SeqNum != InitSequenceNumber {
		t.Fatalf("re-originated sequence = %v, want initial %#x",
			d.Reoriginated, seqHex(InitSequenceNumber))
	}
	if len(d.Flood) != 2 {
		t.Fatalf("flood count = %d, want flush then fresh", len(d.Flood))
	}
	flush, fresh := d.Flood[0], d.Flood[1]
	if !flush.IsMaxAge() || flush.SeqNum != MaxSequenceNumber {
		t.Fatalf("first flooded LSA = seq %#x age %d, want MaxAge flush",
			uint32(flush.SeqNum), flush.LSAGE)
	}
	if fresh != d.Reoriginated || fresh.SeqNum != InitSequenceNumber {
		t.Fatal("second flooded LSA must be the fresh initial instance")
	}
	stored, ok := db.Lookup(own.Key())
	if !ok || stored != fresh {
		t.Fatal("database must retain the wrapped fresh instance")
	}
	// The wrapped initial sequence is circularly newer than the maximum.
	if CompareSequence(stored.SeqNum, MaxSequenceNumber) <= 0 {
		t.Fatal("wrapped initial sequence must compare newer than the maximum")
	}
}

// A foreign LSA that legitimately wraps must be accepted through the same
// circular sequence comparison.
func TestForeignLSASequenceWrapAccept(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	maxed := buildLSA(t, neighborID, 1, testLSID, MaxSequenceNumber, 10, nil)
	db.Put(maxed)

	wrapped := buildLSA(t, neighborID, 1, testLSID, InitSequenceNumber, 2, nil)
	d := db.Admit(neighborID, 1, wrapped)
	if d.Outcome != OutcomeAccept || d.Instance != wrapped {
		t.Fatalf("outcome = %s, want accept of wrapped LSA", d.Outcome)
	}
	stored, ok := db.Lookup(maxed.Key())
	if !ok || stored != wrapped {
		t.Fatal("wrapped LSA must replace the maximum-sequence instance")
	}
}
