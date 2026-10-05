package barrier

import (
	"sync"
	"testing"
)

func arriveAll(t *testing.T, b *Barrier, round int, members ...string) {
	t.Helper()
	for _, m := range members {
		if _, created := b.Arrive(round, m); !created {
			t.Fatalf("arrival of %q in round %d was not accepted", m, round)
		}
	}
}

func TestCompletionCertificateListsAllFrozenMembers(t *testing.T) {
	b := NewBarrier("a", "b", "c")
	arriveAll(t, b, 1, "a", "b", "c")

	certs := b.Certificates()
	if len(certs) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(certs))
	}
	cert := certs[0]
	if cert.Round != 1 {
		t.Fatalf("certificate round = %d, want 1", cert.Round)
	}
	for _, m := range []string{"a", "b", "c"} {
		if cert.Dispositions[m] != DispositionArrived {
			t.Fatalf("disposition of %q = %q, want arrived", m, cert.Dispositions[m])
		}
		if _, ok := cert.Arrivals[m]; !ok {
			t.Fatalf("certificate missing arrival proof for %q", m)
		}
	}
	if len(cert.Dispositions) != 3 {
		t.Fatalf("certificate lists %d members, want 3", len(cert.Dispositions))
	}
	if b.Round() != 2 {
		t.Fatalf("round = %d, want 2 after completion", b.Round())
	}
}

func TestArrivalProofsHaveUniqueSequence(t *testing.T) {
	b := NewBarrier("a", "b")
	p1, _ := b.Arrive(1, "a")
	p2, _ := b.Arrive(1, "b")
	if p1.Seq == p2.Seq {
		t.Fatalf("duplicate sequence numbers: %d", p1.Seq)
	}
	if p1.Round != 1 || p2.Round != 1 {
		t.Fatalf("proofs carry wrong round: %d, %d", p1.Round, p2.Round)
	}
}

func TestDuplicateArrivalHasNoSideEffect(t *testing.T) {
	b := NewBarrier("a", "b")
	p1, created := b.Arrive(1, "a")
	if !created {
		t.Fatal("first arrival should be accepted")
	}
	p2, created := b.Arrive(1, "a")
	if created {
		t.Fatal("duplicate arrival must not be accepted")
	}
	if p2 == nil || *p2 != *p1 {
		t.Fatal("duplicate arrival should return the existing proof unchanged")
	}
	if len(b.Certificates()) != 0 {
		t.Fatal("duplicate arrival must not complete the round")
	}
	arriveAll(t, b, 1, "b")
	if len(b.Certificates()) != 1 {
		t.Fatalf("expected exactly 1 certificate, got %d", len(b.Certificates()))
	}
}

func TestStaleAndFutureRoundMessagesHaveNoSideEffect(t *testing.T) {
	b := NewBarrier("a", "b")
	arriveAll(t, b, 1, "a", "b") // completes round 1, now in round 2

	if p, created := b.Arrive(1, "a"); created || p != nil {
		t.Fatal("stale-round arrival must be ignored")
	}
	if e, created := b.Exempt(1, "a", "late"); created || e != nil {
		t.Fatal("stale-round exemption must be ignored")
	}
	if p, created := b.Arrive(3, "a"); created || p != nil {
		t.Fatal("future-round arrival must be ignored")
	}
	if len(b.Certificates()) != 1 {
		t.Fatalf("stale/future messages created side effects: %d certificates", len(b.Certificates()))
	}
	if b.Round() != 2 {
		t.Fatalf("round = %d, want 2", b.Round())
	}
}

func TestArrivalOfNonFrozenMemberHasNoSideEffect(t *testing.T) {
	b := NewBarrier("a")
	if p, created := b.Arrive(1, "ghost"); created || p != nil {
		t.Fatal("arrival of non-frozen member must be ignored")
	}
	if len(b.Certificates()) != 0 {
		t.Fatal("non-frozen arrival must not complete the round")
	}
}

func TestNewMemberWaitsForNextRound(t *testing.T) {
	b := NewBarrier("a")
	effective := b.Join("n")
	if effective != 2 {
		t.Fatalf("join effective round = %d, want 2", effective)
	}
	if b.Frozen("n") {
		t.Fatal("new member must not be frozen into the current round")
	}
	arriveAll(t, b, 1, "a")

	certs := b.Certificates()
	if len(certs) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(certs))
	}
	if _, ok := certs[0].Dispositions["n"]; ok {
		t.Fatal("round-1 certificate must not include the new member")
	}
	if !b.Frozen("n") {
		t.Fatal("new member must be frozen into round 2")
	}
	arriveAll(t, b, 2, "a", "n")
	certs = b.Certificates()
	if len(certs) != 2 {
		t.Fatalf("expected 2 certificates, got %d", len(certs))
	}
	if certs[1].Dispositions["n"] != DispositionArrived {
		t.Fatal("round-2 certificate must list the new member as arrived")
	}
}

func TestExemptedMemberCountsTowardCompletion(t *testing.T) {
	b := NewBarrier("a", "b")
	arriveAll(t, b, 1, "a")
	e, created := b.Exempt(1, "b", "maintenance")
	if !created {
		t.Fatal("exemption should be accepted")
	}
	if e.Reason != "maintenance" {
		t.Fatalf("exemption reason = %q", e.Reason)
	}
	certs := b.Certificates()
	if len(certs) != 1 {
		t.Fatalf("expected completion, got %d certificates", len(certs))
	}
	if certs[0].Dispositions["b"] != DispositionExempted {
		t.Fatal("member b must be listed as exempted")
	}
	if _, ok := certs[0].Exemptions["b"]; !ok {
		t.Fatal("certificate must carry the exemption record for b")
	}
}

func TestLeavingMemberGetsExemptionRecord(t *testing.T) {
	b := NewBarrier("a", "b")
	arriveAll(t, b, 1, "a")
	b.Leave("b") // b exits mid-round without arriving

	certs := b.Certificates()
	if len(certs) != 1 {
		t.Fatalf("expected completion after leave, got %d certificates", len(certs))
	}
	cert := certs[0]
	if cert.Dispositions["b"] != DispositionExempted {
		t.Fatal("exiting member must be listed as exempted")
	}
	if _, ok := cert.Exemptions["b"]; !ok {
		t.Fatal("exiting member must have an exemption record")
	}
	if b.Frozen("b") {
		t.Fatal("exited member must not be frozen into round 2")
	}
	if !b.Frozen("a") {
		t.Fatal("remaining member must stay frozen into round 2")
	}
}

func TestArrivedMemberLeavesWithoutExemption(t *testing.T) {
	b := NewBarrier("a", "b", "c")
	arriveAll(t, b, 1, "a", "b")
	b.Leave("b") // b already arrived this round: no exemption needed
	arriveAll(t, b, 1, "c")

	certs := b.Certificates()
	if len(certs) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(certs))
	}
	if certs[0].Dispositions["b"] != DispositionArrived {
		t.Fatal("arrived member must stay arrived in the certificate")
	}
	if _, ok := certs[0].Exemptions["b"]; ok {
		t.Fatal("arrived member must not get an exemption record")
	}
	if b.Frozen("b") {
		t.Fatal("exited member must not be frozen into round 2")
	}
}

func TestLastArrivalVersusExemptionRace(t *testing.T) {
	for i := 0; i < 300; i++ {
		b := NewBarrier("a", "b", "c")
		arriveAll(t, b, 1, "a", "b")

		var wg sync.WaitGroup
		var proof *ArrivalProof
		var exemption *Exemption
		var arrivedCreated, exemptCreated bool
		wg.Add(2)
		go func() {
			defer wg.Done()
			proof, arrivedCreated = b.Arrive(1, "c")
		}()
		go func() {
			defer wg.Done()
			exemption, exemptCreated = b.Exempt(1, "c", "racing exit")
		}()
		wg.Wait()

		// Exactly one side wins the race.
		if arrivedCreated == exemptCreated {
			t.Fatalf("iter %d: exactly one of arrival/exemption must win (arrived=%v exempted=%v)",
				i, arrivedCreated, exemptCreated)
		}
		if arrivedCreated && proof == nil {
			t.Fatalf("iter %d: winning arrival must return a proof", i)
		}
		if exemptCreated && exemption == nil {
			t.Fatalf("iter %d: winning exemption must return a record", i)
		}

		certs := b.Certificates()
		if len(certs) != 1 {
			t.Fatalf("iter %d: expected exactly 1 certificate, got %d", i, len(certs))
		}
		cert := certs[0]

		// The certificate must account for every frozen member exactly once.
		if len(cert.Dispositions) != 3 {
			t.Fatalf("iter %d: certificate lists %d members, want 3", i, len(cert.Dispositions))
		}
		for _, m := range []string{"a", "b", "c"} {
			if _, ok := cert.Dispositions[m]; !ok {
				t.Fatalf("iter %d: certificate missing frozen member %q", i, m)
			}
		}
		want := DispositionExempted
		if arrivedCreated {
			want = DispositionArrived
		}
		if cert.Dispositions["c"] != want {
			t.Fatalf("iter %d: c disposition = %q, want %q", i, cert.Dispositions["c"], want)
		}
		if len(cert.Arrivals)+len(cert.Exemptions) != 3 {
			t.Fatalf("iter %d: certificate records are inconsistent", i)
		}
	}
}

func TestConcurrentDuplicateArrivalsCompleteOnce(t *testing.T) {
	b := NewBarrier("a", "b", "c", "d")
	members := []string{"a", "b", "c", "d"}

	var wg sync.WaitGroup
	created := make([]int32, len(members))
	for i, m := range members {
		for k := 0; k < 8; k++ {
			wg.Add(1)
			go func(idx int, member string) {
				defer wg.Done()
				if _, ok := b.Arrive(1, member); ok {
					created[idx]++
				}
			}(i, m)
		}
	}
	wg.Wait()

	for i, m := range members {
		if created[i] != 1 {
			t.Fatalf("member %q: %d accepted arrivals, want exactly 1", m, created[i])
		}
	}
	certs := b.Certificates()
	if len(certs) != 1 {
		t.Fatalf("expected exactly 1 certificate, got %d", len(certs))
	}
	if len(certs[0].Dispositions) != 4 {
		t.Fatalf("certificate lists %d members, want 4", len(certs[0].Dispositions))
	}
}
