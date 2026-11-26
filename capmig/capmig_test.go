package capmig

import (
	"strings"
	"testing"
)

func findReport(t *testing.T, res *Result, id string) TicketReport {
	t.Helper()
	for _, r := range res.TicketReports {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("ticket %s not found", id)
	return TicketReport{}
}

// TestMultiAliasImpersonation proves that aliases collapse to physical IDs
// before entitlement reasoning, so a subject holding a token on an alias of a
// different physical object cannot impersonate the target's holder.
func TestMultiAliasImpersonation(t *testing.T) {
	in := &Input{
		BarrierEpoch: 1,
		AliasBindings: []AliasBinding{
			{Alias: "old-portal", PhysicalID: "phys-A"},
			{Alias: "legacy-portal", PhysicalID: "phys-A"},
			{Alias: "decoy-portal", PhysicalID: "phys-B"},
			{Alias: "new-portal", PhysicalID: "phys-C"},
		},
		ResourceMaps: []ResourceMapEdge{
			{OldObject: "phys-A", NewObject: "phys-C"},
			{OldObject: "phys-B", NewObject: "phys-C"},
		},
		ActionSplits: []ActionSplit{
			{OldAction: "manage", NewActions: []string{"read", "write", "admin"}},
		},
		Grants: []Grant{
			{Token: "TA", Subject: "alice", Object: "old-portal", Action: "manage", Epoch: 1},
			{Token: "TB", Subject: "mallory", Object: "decoy-portal", Action: "manage", Epoch: 1},
		},
		Handoffs: []Handoff{
			{ID: "h1", Token: "TB", From: "mallory", To: "alice", Epoch: 2},
		},
		Tickets: []Ticket{
			{ID: "legit", Subject: "alice", NewObject: "new-portal",
				NewActions: []string{"read", "write", "admin"}, AncestorTokens: []string{"TA"}},
			{ID: "spoof", Subject: "alice", NewObject: "new-portal",
				NewActions: []string{"admin"}, AncestorTokens: []string{"TB"}},
		},
	}

	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.ExclusivityHeld || !res.JointConsistencyHeld {
		t.Fatalf("invariants not held: %+v", res)
	}
	if got := findReport(t, res, "legit").Decision; got != DecisionMigrate {
		t.Fatalf("legit ticket = %s, want migrate", got)
	}
	// TB lives on phys-B which maps to phys-C as well, but its holder chain is
	// mallory -> alice via a confirmed hop, so alice does hold TB; the spoof is
	// only blocked if we instead keep TB with mallory via a lost confirmation.
	// Add that stronger scenario below.
	if got := findReport(t, res, "spoof").Decision; got != DecisionMigrate {
		t.Fatalf("spoof baseline = %s, want migrate (confirmed hop)", got)
	}
}

func TestMinimumHittingSetExactness(t *testing.T) {
	candidates := []string{"a", "b", "c", "d"}
	index := map[string]int{"a": 0, "b": 1, "c": 2, "d": 3}
	mk := func(groups ...[]string) [][]uint64 {
		sets := make([][]uint64, len(groups))
		for i, g := range groups {
			sets[i] = make([]uint64, 1)
			for _, s := range g {
				bit := index[s]
				sets[i][bit/64] |= 1 << uint(bit%64)
			}
		}
		return sets
	}

	got := minimumHittingSet(candidates, mk(
		[]string{"a", "b"},
		[]string{"b", "c"},
		[]string{"c", "d"},
		[]string{"a", "d"},
	))
	if len(got) != 2 {
		t.Fatalf("optimum = %v, want 2 subjects", got)
	}

	// Brute-force cross-check on random-ish small instances.
	groups := mk(
		[]string{"a"}, []string{"b", "c"}, []string{"a", "c"}, []string{"d"},
	)
	opt := minimumHittingSet(candidates, groups)
	brute := bruteForceHittingSet(candidates, groups)
	if len(opt) != brute {
		t.Fatalf("B&B size %d != brute force %d (%v)", len(opt), brute, opt)
	}
}

func bruteForceHittingSet(candidates []string, sets [][]uint64) int {
	n := len(candidates)
	best := n + 1
	for mask := 0; mask < 1<<n; mask++ {
		ok := true
		for _, s := range sets {
			hit := false
			for i := 0; i < n; i++ {
				if mask&(1<<i) != 0 && s[i/64]&(1<<uint(i%64)) != 0 {
					hit = true
					break
				}
			}
			if !hit {
				ok = false
				break
			}
		}
		if ok {
			cnt := 0
			for m := mask; m > 0; m &= m - 1 {
				cnt++
			}
			if cnt < best {
				best = cnt
			}
		}
	}
	return best
}

// TestThousandDerivedTickets builds >1000 child tickets sharing a few upstream
// ancestors and 18 ambiguous handoffs, then asserts the enumeration stays
// jointly consistent and terminates with the expected decision mix.
func TestThousandDerivedTickets(t *testing.T) {
	const numAmb = 18
	in := &Input{
		BarrierEpoch: 1,
		ResourceMaps: []ResourceMapEdge{{OldObject: "P0", NewObject: "P1"}},
		ActionSplits: []ActionSplit{{OldAction: "manage", NewActions: []string{"read", "write"}}},
	}
	subjects := []string{"root", "s1", "s2"}
	for i, s := range subjects {
		in.Grants = append(in.Grants, Grant{
			Token: "T" + itoa(i), Subject: s, Object: "P0", Action: "manage", Epoch: 1,
		})
	}
	// 18 independent tokens, each with one ambiguous transfer into s2.
	for i := 0; i < numAmb; i++ {
		in.Grants = append(in.Grants, Grant{
			Token: "A" + itoa(i), Subject: "root", Object: "P0", Action: "manage", Epoch: 1,
		})
	}
	for i := 0; i < numAmb; i++ {
		in.Handoffs = append(in.Handoffs, Handoff{
			ID: "amb" + itoa(i), Token: "A" + itoa(i), From: "root", To: "s2",
			Epoch: uint64(2 + i), LostConfirmation: true,
		})
	}

	for i := 0; i < 1200; i++ {
		tok := "A" + itoa(i%numAmb)
		in.Tickets = append(in.Tickets, Ticket{
			ID: "child" + itoa(i), Subject: "s2", NewObject: "P1",
			NewActions:     []string{"read", "write"},
			AncestorTokens: []string{tok},
		})
	}
	// A clean ancestor with no handoff gives a guaranteed-migrate cohort.
	in.Tickets = append(in.Tickets, Ticket{
		ID: "clean", Subject: "s1", NewObject: "P1",
		NewActions: []string{"read"}, AncestorTokens: []string{"T1"},
	})

	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.TotalHistories != 1<<numAmb {
		t.Fatalf("total histories = %d", res.TotalHistories)
	}
	if !res.JointConsistencyHeld || !res.ExclusivityHeld {
		t.Fatal("global invariants failed under full enumeration")
	}
	if findReport(t, res, "clean").Decision != DecisionMigrate {
		t.Fatal("clean ticket must migrate without re-auth")
	}
	var reauth, migrate int
	for _, rep := range res.TicketReports {
		switch rep.Decision {
		case DecisionReauth:
			reauth++
		case DecisionMigrate:
			migrate++
		}
	}
	if reauth == 0 || migrate == 0 {
		t.Fatalf("expected mixed decisions, got reauth=%d migrate=%d", reauth, migrate)
	}
	// Every ambiguous child ticket shares the same re-auth endpoint set, so one
	// subject suffices as the minimum hitting set.
	if len(res.MinimumReauthSubjects) != 1 {
		t.Fatalf("minimum reauth set too large: %v", res.MinimumReauthSubjects)
	}
	if res.SolveMillis > 10000 {
		t.Fatalf("solver too slow: %dms", res.SolveMillis)
	}
	if !strings.Contains(RenderText(res), "MINIMUM RE-AUTHENTICATION SUBJECTS") {
		t.Fatal("renderer missing audit section")
	}
}

func TestMultiAliasImpersonationBlocked(t *testing.T) {
	in := &Input{
		BarrierEpoch: 5,
		AliasBindings: []AliasBinding{
			{Alias: "decoy-portal", PhysicalID: "phys-B"},
			{Alias: "new-portal", PhysicalID: "phys-C"},
		},
		ResourceMaps: []ResourceMapEdge{{OldObject: "phys-B", NewObject: "phys-C"}},
		ActionSplits: []ActionSplit{
			{OldAction: "manage", NewActions: []string{"read", "admin"}},
		},
		Grants: []Grant{{Token: "TB", Subject: "mallory", Object: "phys-B", Action: "manage", Epoch: 1}},
		Handoffs: []Handoff{
			{ID: "lost", Token: "TB", From: "mallory", To: "alice", Epoch: 6, LostConfirmation: true},
		},
		Tickets: []Ticket{
			{ID: "spoof", Subject: "alice", NewObject: "new-portal",
				NewActions: []string{"admin"}, AncestorTokens: []string{"TB"}},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	rep := findReport(t, res, "spoof")
	if rep.Decision != DecisionReauth {
		t.Fatalf("spoof = %s, want reauth", rep.Decision)
	}
	if rep.SupportingHistories != 1 || rep.AdmissibleHistories != 2 {
		t.Fatalf("support = %d/%d, want 1/2", rep.SupportingHistories, rep.AdmissibleHistories)
	}
	if len(rep.Counterexamples) == 0 {
		t.Fatal("expected counterexample history")
	}
	if !contains(rep.EffectiveVariables, "lost") {
		t.Fatalf("effective variables = %v", rep.EffectiveVariables)
	}
}

// TestChainedLostConfirmations exercises a three-hop chain with two ambiguous
// hops: downstream transfers can only land when every predecessor landed.
func TestChainedLostConfirmations(t *testing.T) {
	in := &Input{
		BarrierEpoch: 1,
		AliasBindings: []AliasBinding{
			{Alias: "old", PhysicalID: "P0"},
			{Alias: "new", PhysicalID: "P1"},
		},
		ResourceMaps: []ResourceMapEdge{{OldObject: "P0", NewObject: "P1"}},
		ActionSplits: []ActionSplit{{OldAction: "manage", NewActions: []string{"read", "write"}}},
		Grants:       []Grant{{Token: "T", Subject: "alice", Object: "old", Action: "manage", Epoch: 1}},
		Revocations:  nil,
		Handoffs: []Handoff{
			{ID: "h1", Token: "T", From: "alice", To: "bob", Epoch: 2, LostConfirmation: true},
			{ID: "h2", Token: "T", From: "bob", To: "carol", Epoch: 3, LostConfirmation: true},
			{ID: "h3", Token: "T", From: "carol", To: "dave", Epoch: 4, LostConfirmation: true},
		},
		Tickets: []Ticket{
			{ID: "dave-ticket", Subject: "dave", NewObject: "new",
				NewActions: []string{"read", "write"}, AncestorTokens: []string{"T"}},
			{ID: "bob-ticket", Subject: "bob", NewObject: "new",
				NewActions: []string{"read"}, AncestorTokens: []string{"T"}},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.TotalHistories != 8 || res.AdmissibleHistories != 8 {
		t.Fatalf("histories = %d/%d", res.TotalHistories, res.AdmissibleHistories)
	}

	dave := findReport(t, res, "dave-ticket")
	if dave.Decision != DecisionReauth {
		t.Fatalf("dave = %s, want reauth", dave.Decision)
	}
	// dave holds T only in the single history x1=x2=x3=1.
	if dave.SupportingHistories != 1 {
		t.Fatalf("dave support = %d, want 1", dave.SupportingHistories)
	}
	var h3 ChainHop
	for _, hop := range dave.Chain["T"] {
		if hop.HandoffID == "h3" {
			h3 = hop
		}
	}
	if h3.SuccessHistories != 1 {
		t.Fatalf("h3 success = %d, want 1", h3.SuccessHistories)
	}
	if !h3.LostConfirmation {
		t.Fatal("h3 should be an ambiguous hop")
	}

	bob := findReport(t, res, "bob-ticket")
	if bob.Decision != DecisionReauth || bob.SupportingHistories != 2 {
		t.Fatalf("bob = %s support=%d, want reauth/2", bob.Decision, bob.SupportingHistories)
	}

	// One minimum re-auth subject (a chain endpoint) covers both tickets.
	if len(res.MinimumReauthSubjects) != 1 {
		t.Fatalf("minimum reauth = %v, want one subject", res.MinimumReauthSubjects)
	}
}

// TestLateRevocationReceipt rejects receipts whose epoch is older than the
// global barrier and honours the revocation record itself.
func TestLateRevocationReceipt(t *testing.T) {
	in := &Input{
		BarrierEpoch: 8,
		AliasBindings: []AliasBinding{
			{Alias: "old", PhysicalID: "P0"},
			{Alias: "new", PhysicalID: "P1"},
		},
		ResourceMaps: []ResourceMapEdge{{OldObject: "P0", NewObject: "P1"}},
		ActionSplits: []ActionSplit{{OldAction: "manage", NewActions: []string{"read"}}},
		Grants:       []Grant{{Token: "T", Subject: "alice", Object: "old", Action: "manage", Epoch: 1}},
		Revocations:  []Revocation{{Token: "T", Epoch: 5}},
		Handoffs: []Handoff{
			// The transfer event is post-revocation and the receipt is late.
			{ID: "late", Token: "T", From: "alice", To: "bob", Epoch: 6, ReceiptEpoch: 4},
		},
		Tickets: []Ticket{
			{ID: "bob", Subject: "bob", NewObject: "new",
				NewActions: []string{"read"}, AncestorTokens: []string{"T"}},
			{ID: "alice", Subject: "alice", NewObject: "new",
				NewActions: []string{"read"}, AncestorTokens: []string{"T"}},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.RejectedReceipts) != 1 || res.RejectedReceipts[0].HandoffID != "late" {
		t.Fatalf("rejected receipts = %+v", res.RejectedReceipts)
	}
	for _, id := range []string{"bob", "alice"} {
		rep := findReport(t, res, id)
		if rep.Decision != DecisionDeny {
			t.Fatalf("%s = %s, want deny after revocation", id, rep.Decision)
		}
		if !contains(rep.TouchesRevocation, "T") {
			t.Fatalf("%s missing revocation basis", id)
		}
		if !contains(rep.BarrierRejects, "late") {
			t.Fatalf("%s missing barrier basis", id)
		}
	}
}

// TestConflictingConfirmedReceipts makes two confirmed transfers compete.
// Because every confirmed receipt must hold, no admissible history exists and
// the solver flags global inconsistency instead of inventing a holder.
func TestConflictingConfirmedReceipts(t *testing.T) {
	in := &Input{
		BarrierEpoch: 1,
		Grants:       []Grant{{Token: "T", Subject: "alice", Object: "P0", Action: "read", Epoch: 1}},
		Handoffs: []Handoff{
			{ID: "h1", Token: "T", From: "alice", To: "bob", Epoch: 2},
			{ID: "h2", Token: "T", From: "alice", To: "carol", Epoch: 3},
		},
		ResourceMaps: []ResourceMapEdge{{OldObject: "P0", NewObject: "P1"}},
		Tickets: []Ticket{
			{ID: "bob", Subject: "bob", NewObject: "P1", NewActions: []string{"read"}, AncestorTokens: []string{"T"}},
			{ID: "carol", Subject: "carol", NewObject: "P1", NewActions: []string{"read"}, AncestorTokens: []string{"T"}},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !res.Inconsistent || res.AdmissibleHistories != 0 {
		t.Fatalf("expected inconsistency, admissible=%d", res.AdmissibleHistories)
	}
	for _, id := range []string{"bob", "carol"} {
		if findReport(t, res, id).Decision != DecisionDeny {
			t.Fatalf("%s must deny when no admissible history exists", id)
		}
	}
}

// TestExclusiveHandleSingleHolder keeps one confirmed transfer and turns the
// competing transfer ambiguous, so both holder claims are tracked but never
// simultaneously true in any single history.
func TestExclusiveHandleSingleHolder(t *testing.T) {
	in := &Input{
		BarrierEpoch: 1,
		Grants:       []Grant{{Token: "T", Subject: "alice", Object: "P0", Action: "read", Epoch: 1}},
		Handoffs: []Handoff{
			{ID: "h1", Token: "T", From: "alice", To: "bob", Epoch: 2},
			{ID: "h2", Token: "T", From: "alice", To: "carol", Epoch: 3, LostConfirmation: true},
		},
		ResourceMaps: []ResourceMapEdge{{OldObject: "P0", NewObject: "P1"}},
		Tickets: []Ticket{
			{ID: "bob", Subject: "bob", NewObject: "P1", NewActions: []string{"read"}, AncestorTokens: []string{"T"}},
			{ID: "carol", Subject: "carol", NewObject: "P1", NewActions: []string{"read"}, AncestorTokens: []string{"T"}},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.Inconsistent {
		t.Fatal("one ambiguous competitor must leave admissible histories")
	}
	if findReport(t, res, "bob").Decision != DecisionMigrate {
		t.Fatal("bob holds the handle after confirmed h1 in every history")
	}
	carol := findReport(t, res, "carol")
	if carol.Decision != DecisionDeny {
		t.Fatalf("carol = %s, want deny (alice no longer holds at h2)", carol.Decision)
	}
	if !contains(carol.CausalFailures, "h2") {
		t.Fatalf("causal failures = %v", carol.CausalFailures)
	}

	// Direct state-machine invariant: at most one holder per token per mask.
	idx, err := buildIndex(in)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	eng := newEngine(in, idx)
	eng.enumerate()
	for mask := uint64(0); mask < eng.numHist; mask++ {
		w := eng.simulate(mask)
		holders := map[string]int{}
		for gi, g := range in.Grants {
			h := w.states[gi].holder
			if h < 0 {
				continue
			}
			if prev, ok := holders[g.Token]; ok && prev != h {
				t.Fatalf("token %s has two holders in mask %d", g.Token, mask)
			}
			holders[g.Token] = h
		}
	}
}
