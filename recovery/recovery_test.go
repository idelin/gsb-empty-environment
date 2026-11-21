package recovery

import "testing"

func TestValidateAcceptsFixtures(t *testing.T) {
	for _, p := range []*Problem{instanceA(), instanceB(), instanceC(), instanceD()} {
		if err := p.Validate(); err != nil {
			t.Fatalf("fixture should be valid: %v", err)
		}
	}
}

func TestOptimalCostInstanceA(t *testing.T) {
	p := instanceA()
	sol, err := p.Solve()
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if !sol.Solvable() {
		t.Fatal("instanceA must be solvable")
	}
	got, ok := sol.OptimalCost()
	if !ok || got != 28 {
		t.Fatalf("optimal worst-case cost = %d (%v), want 28 (one paid redelivery after a disconnect)", got, ok)
	}
	naive, solvable := bruteForceValue(p, p.initialBelief(), map[string]int{}, 0)
	if !solvable || naive != got {
		t.Fatalf("independent solver = %d solvable=%v, want %d true", naive, solvable, got)
	}

	// Without any disconnect the same policy finishes with the single-command
	// baseline fee; the disconnect adversary is what doubles the worst case.
	noFault := 0
	for _, s := range p.Sites {
		e := NewExecutor(p, s)
		c, _ := NewCoordinator(sol)
		for !c.Done() {
			a, err := c.Next()
			if err != nil {
				t.Fatalf("next: %v", err)
			}
			r := deliver(t, p, e, a)
			if err := c.Observe(r); err != nil {
				t.Fatalf("observe: %v", err)
			}
		}
		if e.Spent() > noFault {
			noFault = e.Spent()
		}
		if !e.AtTarget() {
			t.Fatalf("site %s did not reach target", s.Name)
		}
	}
	if noFault != 14 {
		t.Fatalf("no-fault baseline = %d, want 14", noFault)
	}
}

func TestImpossibilityCertificateInstanceB(t *testing.T) {
	p := instanceB()
	sol, err := p.Solve()
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if sol.Solvable() {
		t.Fatal("instanceB must be unsolvable")
	}
	cert := sol.Certificate()
	if cert == nil {
		t.Fatal("unsolvable instance must carry a certificate")
	}
	if err := p.Verify(cert); err != nil {
		t.Fatalf("certificate failed independent verification: %v", err)
	}
	if _, err := NewCoordinator(sol); err == nil {
		t.Fatal("coordinator must reject an unsolvable instance")
	}
}

func TestCertificateRejectsTampering(t *testing.T) {
	p := instanceB()
	sol, err := p.Solve()
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	cert := sol.Certificate()
	if err := p.Verify(cert); err != nil {
		t.Fatalf("base certificate: %v", err)
	}
	cert.Losing[cert.Initial] = false
	if err := p.Verify(cert); err == nil {
		t.Fatal("tampered certificate must not verify")
	}
}

func TestSameSummaryDifferentValues(t *testing.T) {
	p := instanceC()
	for _, pr := range p.Probes {
		answers := map[Parity]bool{}
		for _, s := range p.Sites {
			e := NewExecutor(p, s)
			r := e.RunProbe(pr)
			answers[r.Answer] = true
		}
		if len(answers) != 1 {
			t.Fatalf("probe %s must be uninformative across sites, got %v", pr.Name, answers)
		}
	}
	sol, err := p.Solve()
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if sol.Solvable() {
		t.Fatal("identical summaries cannot repair divergent exact values")
	}
	if err := p.Verify(sol.Certificate()); err != nil {
		t.Fatalf("certificate: %v", err)
	}
}

func TestSameCommittedSetDifferentFinalWriter(t *testing.T) {
	p := instanceD()
	sol, err := p.Solve()
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if sol.Solvable() {
		t.Fatal("bitmap convergence alone must not count as completion; values diverge")
	}
	if err := p.Verify(sol.Certificate()); err != nil {
		t.Fatalf("certificate: %v", err)
	}
	for _, s := range p.Sites {
		e := NewExecutor(p, s)
		if e.AtTarget() {
			t.Fatalf("site %s cannot already equal target with different final writer", s.Name)
		}
	}
}
