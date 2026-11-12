package lsadb

import "testing"

func sameKey(a, b *LSA) bool { return a.Key() == b.Key() }

// A forged self-originated LSA must never enter the database; instead the
// router counter-originates a strictly newer instance.
func TestForgedSelfLSA(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	own := buildLSA(t, localID, 1, testLSID, 100, 5, nil)
	db.Put(own)

	forged := buildLSA(t, localID, 1, testLSID, 200, 1, nil)
	d := db.Admit(attackerID, 1, forged)

	if d.Outcome != OutcomeSelfDefense {
		t.Fatalf("outcome = %s, want self-defense", d.Outcome)
	}
	if d.Reoriginated == nil || d.Reoriginated.SeqNum != 201 {
		t.Fatalf("re-originated sequence = %v, want 201", d.Reoriginated)
	}
	if CompareSequence(d.Reoriginated.SeqNum, forged.SeqNum) <= 0 {
		t.Fatal("re-originated LSA must be strictly newer than the forgery")
	}
	stored, ok := db.Lookup(forged.Key())
	if !ok || stored != d.Reoriginated {
		t.Fatal("database must hold the re-originated instance, not the forgery")
	}
	if len(d.Flood) != 1 || d.Flood[0] != d.Reoriginated {
		t.Fatalf("flood = %v, want only the re-originated LSA", d.Flood)
	}
	if len(d.Ack) != 1 || d.Ack[0] != forged {
		t.Fatalf("ack = %v, want the forged LSA acknowledged", d.Ack)
	}
	if d.Reoriginated.AdvertisingRouter != localID || d.Reoriginated.LSAGE != 0 {
		t.Fatal("re-originated LSA must be a fresh local instance")
	}
	if db.Stats().SelfReoriginated.Load() != 1 {
		t.Fatalf("self_reoriginated = %d, want 1", db.Stats().SelfReoriginated.Load())
	}
}

// An equal-sequence self LSA is still a forgery ("not lower than") and must
// trigger counter-origination as well.
func TestForgedSelfEqualSequence(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	own := buildLSA(t, localID, 1, testLSID, 100, 5, nil)
	db.Put(own)

	forged := buildLSA(t, localID, 1, testLSID, 100, 1, nil)
	d := db.Admit(attackerID, 1, forged)
	if d.Outcome != OutcomeSelfDefense || d.Reoriginated == nil ||
		d.Reoriginated.SeqNum != 101 {
		t.Fatalf("outcome = %s reorig = %v, want self-defense at seq 101",
			d.Outcome, d.Reoriginated)
	}
}

// A self LSA older than the local instance is harmless: send the local
// instance back and do not re-originate.
func TestForgedSelfOlder(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	own := buildLSA(t, localID, 1, testLSID, 300, 5, nil)
	db.Put(own)

	stale := buildLSA(t, localID, 1, testLSID, 299, 1, nil)
	d := db.Admit(attackerID, 1, stale)
	if d.Outcome != OutcomeSelfOlder {
		t.Fatalf("outcome = %s, want self-older", d.Outcome)
	}
	if len(d.Flood) != 1 || d.Flood[0] != own {
		t.Fatalf("flood = %v, want the local instance", d.Flood)
	}
	if db.Stats().SelfReoriginated.Load() != 0 {
		t.Fatal("older self LSA must not re-originate")
	}
	stored, _ := db.Lookup(own.Key())
	if stored != own {
		t.Fatal("database instance must remain unchanged")
	}
}

// Counter-origination is rate limited per LSA key.
func TestSelfDefenseThrottle(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 100, clock)

	own := buildLSA(t, localID, 1, testLSID, 100, 5, nil)
	db.Put(own)

	first := db.Admit(attackerID, 1, buildLSA(t, localID, 1, testLSID, 200, 1, nil))
	if first.Outcome != OutcomeSelfDefense {
		t.Fatalf("first outcome = %s, want self-defense", first.Outcome)
	}

	clock.advance(50)
	second := db.Admit(attackerID, 2, buildLSA(t, localID, 1, testLSID, 250, 1, nil))
	if second.Outcome != OutcomeSelfDefenseThrottled || !second.DefenseDelayed {
		t.Fatalf("second outcome = %s, want throttled", second.Outcome)
	}
	if second.RetryAfter <= 0 {
		t.Fatalf("retry after = %v, want positive duration", second.RetryAfter)
	}
	stored, _ := db.Lookup(own.Key())
	if stored != first.Reoriginated {
		t.Fatal("throttled defense must leave the database untouched")
	}

	clock.advance(51)
	third := db.Admit(attackerID, 3, buildLSA(t, localID, 1, testLSID, 250, 1, nil))
	if third.Outcome != OutcomeSelfDefense || third.Reoriginated == nil ||
		third.Reoriginated.SeqNum != 251 {
		t.Fatalf("third outcome = %s reorig = %v, want defense at seq 251",
			third.Outcome, third.Reoriginated)
	}

	if got := db.Stats().SelfThrottled.Load(); got != 1 {
		t.Fatalf("self_throttled = %d, want 1", got)
	}
}

// A forged self-originated LSA for an LSA the router does not currently hold
// is still counter-originated at a higher sequence number.
func TestForgedSelfWithoutLocalInstance(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	db := newTestDB(t, 10, clock)

	forged := buildLSA(t, localID, 1, testLSID, 500, 1, nil)
	d := db.Admit(attackerID, 1, forged)

	if d.Outcome != OutcomeSelfDefense || d.Reoriginated == nil {
		t.Fatalf("outcome = %s reorig = %v, want self-defense", d.Outcome, d.Reoriginated)
	}
	if d.Reoriginated.SeqNum != 501 {
		t.Fatalf("re-originated sequence = %#x, want 501", seqHex(d.Reoriginated.SeqNum))
	}
	if !sameKey(d.Reoriginated, forged) ||
		d.Reoriginated.AdvertisingRouter != localID {
		t.Fatal("re-originated LSA must preserve the forged LSA identity")
	}
	stored, ok := db.Lookup(forged.Key())
	if !ok || stored != d.Reoriginated {
		t.Fatal("counter-originated LSA must be installed, the forgery refused")
	}
}
