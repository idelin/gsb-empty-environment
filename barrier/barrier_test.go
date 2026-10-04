package barrier

import (
	"sync"
	"testing"
)

func setup(t *testing.T, ids ...string) (*Barrier, uint64) {
	t.Helper()
	b := New()
	for _, id := range ids {
		b.Join(id)
	}
	round, err := b.BeginRound()
	if err != nil {
		t.Fatalf("BeginRound: %v", err)
	}
	return b, round
}

func TestFreezeSnapshotAndJoinWaitsNextRound(t *testing.T) {
	b, round := setup(t, "a", "b")

	// Joining mid-round: effective only from the next round.
	if eff := b.Join("c"); eff != round+1 {
		t.Fatalf("join mid-round effective round = %d, want %d", eff, round+1)
	}
	if _, err := b.Arrive("c", round); err != ErrNotFrozen {
		t.Fatalf("arrive of mid-round joiner: err = %v, want ErrNotFrozen", err)
	}

	if _, err := b.Arrive("a", round); err != nil {
		t.Fatalf("arrive a: %v", err)
	}
	if _, err := b.Exempt("b", round, "left"); err != nil {
		t.Fatalf("exempt b: %v", err)
	}
	cert, ok := b.Certificate(round)
	if !ok {
		t.Fatal("round should be complete")
	}
	if cert.Arrived() != 1 || cert.Exempted() != 1 {
		t.Fatalf("cert tallies = %d/%d, want 1/1", cert.Arrived(), cert.Exempted())
	}

	// Next round freezes the new member.
	if _, err := b.BeginRound(); err != nil {
		t.Fatalf("BeginRound 2: %v", err)
	}
	frozen := b.Frozen()
	if len(frozen) != 3 || frozen[0] != "a" || frozen[1] != "b" || frozen[2] != "c" {
		t.Fatalf("frozen = %v, want [a b c]", frozen)
	}
}

func TestArrivalProofUniqueAndDuplicateIdempotent(t *testing.T) {
	b, round := setup(t, "a", "b", "c")

	p1, err := b.Arrive("a", round)
	if err != nil {
		t.Fatalf("arrive a: %v", err)
	}
	p2, err := b.Arrive("b", round)
	if err != nil {
		t.Fatalf("arrive b: %v", err)
	}
	if p1.Seq != 1 || p2.Seq != 2 || p1.Digest == p2.Digest {
		t.Fatalf("proofs not unique/ordered: %+v %+v", p1, p2)
	}

	// Duplicate arrival returns the original proof and does not advance state.
	dup, err := b.Arrive("a", round)
	if err != nil {
		t.Fatalf("duplicate arrive: %v", err)
	}
	if dup != p1 {
		t.Fatalf("duplicate proof = %+v, want %+v", dup, p1)
	}
	p3, err := b.Arrive("c", round)
	if err != nil {
		t.Fatalf("arrive c: %v", err)
	}
	if p3.Seq != 3 {
		t.Fatalf("seq after duplicate = %d, want 3 (duplicate had no side effect)", p3.Seq)
	}
	if _, ok := b.Certificate(round); !ok {
		t.Fatal("round should be complete after all arrivals")
	}
}

func TestStaleAndFutureRoundMessagesHaveNoSideEffects(t *testing.T) {
	b, round := setup(t, "a", "b")
	if _, err := b.Arrive("a", round); err != nil {
		t.Fatalf("arrive a: %v", err)
	}
	if _, err := b.Exempt("b", round, "gone"); err != nil {
		t.Fatalf("exempt b: %v", err)
	}
	round2, err := b.BeginRound()
	if err != nil {
		t.Fatalf("BeginRound 2: %v", err)
	}

	// Stale round messages are rejected and change nothing.
	if _, err := b.Arrive("b", round); err != ErrStaleRound {
		t.Fatalf("stale arrive: err = %v, want ErrStaleRound", err)
	}
	if _, err := b.Exempt("a", round, "late"); err != ErrStaleRound {
		t.Fatalf("stale exempt: err = %v, want ErrStaleRound", err)
	}
	// Future round messages are rejected too.
	if _, err := b.Arrive("a", round2+1); err != ErrFutureRound {
		t.Fatalf("future arrive: err = %v, want ErrFutureRound", err)
	}

	// Old certificate untouched, current round still fully pending.
	cert1, _ := b.Certificate(round)
	if cert1.Arrived() != 1 || cert1.Exempted() != 1 {
		t.Fatalf("old cert mutated: %+v", cert1)
	}
	if _, ok := b.Certificate(round2); ok {
		t.Fatal("round 2 should still be open")
	}
	if _, err := b.BeginRound(); err != ErrRoundInProgress {
		t.Fatalf("BeginRound during open round: err = %v, want ErrRoundInProgress", err)
	}
	if _, err := b.Arrive("a", round2); err != nil {
		t.Fatalf("arrive a round2: %v", err)
	}
	if p, err := b.Arrive("b", round2); err != nil || p.Seq != 2 {
		t.Fatalf("arrive b round2 = %+v, %v; want seq 2 (stale msgs had no side effect)", p, err)
	}
}

func TestLeaveRequiresExemptionWhenFrozen(t *testing.T) {
	b, round := setup(t, "a", "b")

	if err := b.Leave("a"); err != ErrExemptionRequired {
		t.Fatalf("leave frozen member: err = %v, want ErrExemptionRequired", err)
	}
	if _, err := b.Exempt("a", round, "shutdown"); err != nil {
		t.Fatalf("exempt a: %v", err)
	}
	if err := b.Leave("a"); err != nil {
		t.Fatalf("leave exempted member: %v", err)
	}
	if err := b.Leave("nobody"); err != ErrUnknownMember {
		t.Fatalf("leave unknown: err = %v, want ErrUnknownMember", err)
	}
	// Exempted member stays in the frozen set of this round's certificate.
	if _, err := b.Arrive("b", round); err != nil {
		t.Fatalf("arrive b: %v", err)
	}
	cert, ok := b.Certificate(round)
	if !ok || len(cert.Entries) != 2 {
		t.Fatalf("cert = %+v, ok = %v; want 2 entries", cert, ok)
	}
	// Next round freezes only the remaining member.
	if _, err := b.BeginRound(); err != nil {
		t.Fatalf("BeginRound 2: %v", err)
	}
	if frozen := b.Frozen(); len(frozen) != 1 || frozen[0] != "b" {
		t.Fatalf("frozen = %v, want [b]", frozen)
	}
}

func TestArrivalAndExemptionAreMutuallyExclusive(t *testing.T) {
	b, round := setup(t, "a")
	if _, err := b.Arrive("a", round); err != nil {
		t.Fatalf("arrive: %v", err)
	}
	if _, err := b.Exempt("a", round, "too late"); err != ErrAlreadyArrived {
		t.Fatalf("exempt after arrive: err = %v, want ErrAlreadyArrived", err)
	}

	b2, round2 := setup(t, "x")
	if _, err := b2.Exempt("x", round2, "left"); err != nil {
		t.Fatalf("exempt: %v", err)
	}
	if _, err := b2.Arrive("x", round2); err != ErrAlreadyExempted {
		t.Fatalf("arrive after exempt: err = %v, want ErrAlreadyExempted", err)
	}
	// Duplicate exemption is idempotent and keeps the original reason.
	r1, _ := b2.Exempt("x", round2, "left")
	r2, _ := b2.Exempt("x", round2, "different reason")
	if r1 != r2 || r2.Reason != "left" {
		t.Fatalf("duplicate exemption changed record: %+v vs %+v", r1, r2)
	}
}

// TestLastArrivalVsExemptionRace runs the last frozen member's arrival
// concurrently with its exemption. Exactly one must win, and the completion
// certificate must consistently list every frozen member's disposition.
func TestLastArrivalVsExemptionRace(t *testing.T) {
	const members = 8
	const iterations = 500

	arrivalWins, exemptionWins := 0, 0
	for i := 0; i < iterations; i++ {
		b := New()
		ids := make([]string, 0, members)
		for m := 0; m < members; m++ {
			id := string(rune('a' + m))
			ids = append(ids, id)
			b.Join(id)
		}
		round, err := b.BeginRound()
		if err != nil {
			t.Fatalf("BeginRound: %v", err)
		}
		// Everyone but the last member arrives.
		for _, id := range ids[:members-1] {
			if _, err := b.Arrive(id, round); err != nil {
				t.Fatalf("arrive %s: %v", id, err)
			}
		}

		last := ids[members-1]
		var wg sync.WaitGroup
		start := make(chan struct{})
		var arriveErr, exemptErr error
		race := func(f func() error, errp *error) {
			defer wg.Done()
			<-start
			*errp = f()
		}
		wg.Add(2)
		arrive := func() error { _, err := b.Arrive(last, round); return err }
		exempt := func() error { _, err := b.Exempt(last, round, "left at the finish line"); return err }
		if i%2 == 0 {
			go race(arrive, &arriveErr)
			go race(exempt, &exemptErr)
		} else {
			go race(exempt, &exemptErr)
			go race(arrive, &arriveErr)
		}
		close(start)
		wg.Wait()

		// Exactly one of the two operations wins.
		if (arriveErr == nil) == (exemptErr == nil) {
			t.Fatalf("iter %d: arriveErr=%v exemptErr=%v, want exactly one success",
				i, arriveErr, exemptErr)
		}
		if arriveErr != nil && arriveErr != ErrAlreadyExempted {
			t.Fatalf("iter %d: unexpected arriveErr %v", i, arriveErr)
		}
		if exemptErr != nil && exemptErr != ErrAlreadyArrived {
			t.Fatalf("iter %d: unexpected exemptErr %v", i, exemptErr)
		}

		// The round is complete and the certificate accounts for everyone.
		cert, ok := b.Certificate(round)
		if !ok {
			t.Fatalf("iter %d: round not complete after the race", i)
		}
		if len(cert.Entries) != members {
			t.Fatalf("iter %d: cert has %d entries, want %d", i, len(cert.Entries), members)
		}
		seen := make(map[string]Disposition, members)
		for _, e := range cert.Entries {
			if e.Disposition == Pending {
				t.Fatalf("iter %d: member %s still pending in certificate", i, e.Member)
			}
			seen[e.Member] = e.Disposition
		}
		for _, id := range ids {
			if _, ok := seen[id]; !ok {
				t.Fatalf("iter %d: frozen member %s missing from certificate", i, id)
			}
		}
		switch seen[last] {
		case Arrived:
			arrivalWins++
			if cert.Arrived() != members || cert.Exempted() != 0 {
				t.Fatalf("iter %d: inconsistent cert %+v", i, cert)
			}
		case Exempted:
			exemptionWins++
			if cert.Arrived() != members-1 || cert.Exempted() != 1 {
				t.Fatalf("iter %d: inconsistent cert %+v", i, cert)
			}
		}
	}
	if arrivalWins == 0 || exemptionWins == 0 {
		t.Fatalf("race not exercised both ways: arrival=%d exemption=%d",
			arrivalWins, exemptionWins)
	}
	t.Logf("race outcomes over %d iterations: arrival=%d exemption=%d",
		iterations, arrivalWins, exemptionWins)
}
