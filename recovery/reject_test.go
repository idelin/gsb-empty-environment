package recovery

import "testing"

// instanceE has a second transaction whose predicate fails on the site that
// only committed txn 0, producing a deterministic reject receipt.
func instanceE() *Problem {
	txns := []Txn{
		{ID: 0, Group: 0, Reads: nil, WriteKey: "k0", WriteVal: "a", HasWrite: true},
		{ID: 1, Group: 0, Reads: []Read{{Low: "k0", High: "k1", Want: "q"}}, WriteKey: "k1", WriteVal: "b", HasWrite: true},
		{ID: 2, Group: 1, Reads: nil, WriteKey: "k0", WriteVal: "q", HasWrite: true},
	}
	sites := []Site{
		{Name: "empty", Committed: 0, KV: map[Key]Value{}},
		{Name: "one", Committed: 1, KV: map[Key]Value{"k0": "a"}},
	}
	return &Problem{
		Txns:            txns,
		Sites:           sites,
		TargetKV:        map[Key]Value{"k0": "q", "k1": "b"},
		TargetCommitted: BitsetAll(3),
		Commands: []Command{
			{Name: "c1", Group: 0, Prefix: 1, Cost: 3},
			{Name: "c2", Group: 0, Prefix: 2, Cost: 5},
			{Name: "fix", Group: 1, Prefix: 1, Cost: 4},
		},
		Probes: []Probe{{Name: "p0", Group: 0, Cost: 2}},
	}
}

func TestRejectionIsChargedAndLeavesStateUntouched(t *testing.T) {
	p := instanceE()
	if err := p.Validate(); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	exec := NewExecutor(p, p.Sites[1]) // k0=a, so txn1 predicate (want q) fails.
	before := exec.Committed()
	beforeKV := exec.KV()
	beforeFee := exec.Spent()
	cmd, _ := p.FindCommand("c2")
	r := exec.RunCommit(cmd)
	rej, ok := r.(RejectReceipt)
	if !ok {
		t.Fatalf("receipt = %T, want RejectReceipt", r)
	}
	if rej.RejectID != 1 {
		t.Fatalf("reject id = %d, want 1", rej.RejectID)
	}
	if exec.Committed() != before {
		t.Fatal("reject must not change the committed bitmap")
	}
	if !kvEqual(exec.KV(), beforeKV) {
		t.Fatal("reject must not change the key/value state")
	}
	if exec.Spent() != beforeFee+cmd.Cost {
		t.Fatalf("reject fee = %d, want %d (no refund)", exec.Spent()-beforeFee, cmd.Cost)
	}
}

func TestCoordinatorConsumesRejectReceipt(t *testing.T) {
	p := instanceE()
	sol, err := p.Solve()
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if !sol.Solvable() {
		t.Fatal("instanceE must be solvable: the empty site succeeds directly")
	}
	exec := NewExecutor(p, p.Sites[1])
	coord, _ := NewCoordinator(sol)
	cmd, _ := p.FindCommand("c2")
	r := exec.RunCommit(cmd)
	if _, ok := r.(RejectReceipt); !ok {
		t.Fatalf("expected reject, got %T", r)
	}
	// The same receipt must be a legal observation of the initial belief: the
	// coordinator's transition machinery accepts it without inventing worlds.
	obs := p.applyCommit(coord.belief, cmd)
	legal := false
	for _, o := range obs {
		if _, ok := o.receipt.(RejectReceipt); ok {
			legal = true
		}
	}
	if !legal {
		t.Fatal("a genuine executor reject must be a legal coordinator receipt")
	}
}

func TestProbeFeeAndReliableParity(t *testing.T) {
	p := instanceB()
	probe, _ := p.FindProbe("p0")
	for _, s := range p.Sites {
		e := NewExecutor(p, s)
		r1 := e.RunProbe(probe)
		r2 := e.RunProbe(probe)
		if r1.Answer != r2.Answer {
			t.Fatal("probe must be reliable across repeated calls")
		}
		if e.Spent() != 2*probe.Cost {
			t.Fatalf("repeated probe fee = %d, want %d", e.Spent(), 2*probe.Cost)
		}
	}
}
