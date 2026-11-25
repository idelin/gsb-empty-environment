package handoff

import (
	"crypto/ed25519"
	"errors"
	"net/netip"
	"testing"
)

func testSpec(egresses ...EgressID) Spec {
	if len(egresses) == 0 {
		egresses = []EgressID{"e1", "e2", "e3"}
	}
	authz := NewAuthorization().
		Grant("tenant-a", MustPrefix("10.10.0.0/24"))
	return Spec{
		PlanID:    "partial-aggregate-handoff",
		Domain:    MustPrefix("10.10.0.0/24"),
		Egresses:  egresses,
		OldTenant: "tenant-a",
		NewTenant: "tenant-b",
		Transfer: []netip.Prefix{
			MustPrefix("10.10.0.128/25"),
		},
		Critical: []netip.Prefix{
			MustPrefix("10.10.0.0/26"),
			MustPrefix("10.10.0.128/25"),
		},
		InitialClaims: []Claim{
			{Prefix: MustPrefix("10.10.0.0/24"), Tenant: "tenant-a"},
		},
		InitialAuthz: authz,
		Window:       2,
		Policy:       DefaultPolicy(),
	}
}

func TestSafePlanEveryAddressAndInterleaving(t *testing.T) {
	spec := testSpec("e1", "e2", "e3")
	plan, err := BuildSafePlan(spec)
	if err != nil {
		t.Fatal(err)
	}

	cert, err := IssueCertificate(plan.Protocol, VerifyOptions{})
	if err != nil {
		t.Fatalf("issue certificate: %v", err)
	}
	if err := cert.RequireSafe(); err != nil {
		t.Fatalf("safe plan certificate: %v\nfirst violations: %+v", err, cert.Violations)
	}
	if cert.ClassCount != 3 {
		t.Fatalf("ClassCount = %d, want 3 (/26, /26, /25)", cert.ClassCount)
	}
	if _, err := VerifyCertificate(cert, plan.Protocol, VerifyOptions{}); err != nil {
		t.Fatalf("independent verify: %v", err)
	}

	traces, err := plan.Protocol.EnumerateTraces(10000)
	if err != nil {
		t.Fatal(err)
	}
	// Three independent deliver->ack chains, with cut after all acks:
	// 6! / (2^3) = 90 linear extensions.
	if len(traces) != 90 {
		t.Fatalf("traces = %d, want 90", len(traces))
	}

	addresses, err := EnumerateAddresses(spec.Domain, 512)
	if err != nil {
		t.Fatal(err)
	}
	for traceIndex, trace := range traces {
		states := replayAllStates(plan.Protocol, trace)
		for _, st := range states {
			cutDone := st.done[plan.CutEvent]
			for _, egress := range spec.Egresses {
				view := st.views[egress]
				for _, addr := range addresses {
					decision := Select(view, addr, st.authz, spec.Policy)
					if !decision.Forward() {
						t.Fatalf("trace %d path=%v egress=%s addr=%s: %s", traceIndex, st.path, egress, addr, decision.Outcome)
					}
					want := spec.OldTenant
					if cutDone && spec.Transfer[0].Contains(addr) {
						want = spec.NewTenant
					}
					if decision.Tenant != want {
						t.Fatalf("trace %d path=%v egress=%s addr=%s tenant=%s, want %s", traceIndex, st.path, egress, addr, decision.Tenant, want)
					}
					if !st.authz.Authorized(decision.Tenant, addr) {
						t.Fatalf("trace %d addr=%s forwarded to unauthorized %s", traceIndex, addr, decision.Tenant)
					}
				}
			}
		}
	}
}

func replayAllStates(p *Protocol, trace []EventID) []*simState {
	states := []*simState{p.initialState()}
	st := p.initialState()
	for _, id := range trace {
		ev := p.Events[id]
		next := st.cloneFor(id)
		p.apply(next, ev)
		st = next
		states = append(states, st)
	}
	return states
}

func TestNaivePlanFinalCorrectButIntermediateMisdirects(t *testing.T) {
	spec := testSpec("e1", "e2")
	naive, err := BuildNaiveImmediatePlan(spec)
	if err != nil {
		t.Fatal(err)
	}

	admissionCert, err := IssueCertificate(naive.Protocol, VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if admissionCert.CriticalReachable {
		t.Fatal("admission certificate unexpectedly keeps every critical class reachable")
	}
	if admissionCert.Safe != true {
		t.Fatal("fallback admission should block rather than make an unauthorized forward")
	}

	rawCert, err := IssueCertificate(naive.Protocol, VerifyOptions{RequireRawAuthorized: true})
	if err != nil {
		t.Fatal(err)
	}
	if rawCert.Safe {
		t.Fatal("raw LPM certificate must expose intermediate misdelivery")
	}
	foundRawViolation := false
	for _, v := range rawCert.Violations {
		if v.Kind == "raw_selection_unauthorized" {
			foundRawViolation = true
			break
		}
	}
	if !foundRawViolation {
		t.Fatalf("expected a raw_selection_unauthorized witness, got %+v", rawCert.Violations)
	}
	if _, err := VerifyCertificate(rawCert, naive.Protocol, VerifyOptions{RequireRawAuthorized: true}); err != nil {
		t.Fatalf("raw certificate verify: %v", err)
	}

	traces, err := naive.Protocol.EnumerateTraces(10000)
	if err != nil {
		t.Fatal(err)
	}
	foundMisdelivery := false
	for _, trace := range traces {
		for _, st := range replayAllStates(naive.Protocol, trace) {
			for _, egress := range spec.Egresses {
				raw := SelectRaw(st.views[egress], netip.MustParseAddr("10.10.0.128"))
				if raw.Found && !raw.Conflict && len(raw.Tenants) == 1 &&
					!st.authz.Authorized(raw.Tenants[0], netip.MustParseAddr("10.10.0.128")) {
					foundMisdelivery = true
				}
			}
		}
	}
	if !foundMisdelivery {
		t.Fatal("exhaustive traces did not contain an intermediate raw misdelivery")
	}

	// Every complete allowed order ends in the globally correct state.
	for _, trace := range traces {
		st := traceState(naive.Protocol, trace)
		if st == nil || len(st.done) != len(naive.Protocol.Events) {
			t.Fatalf("traceState did not replay complete trace: %v", trace)
		}
		for _, egress := range spec.Egresses {
			for _, addr := range []netip.Addr{
				netip.MustParseAddr("10.10.0.1"),
				netip.MustParseAddr("10.10.0.128"),
				netip.MustParseAddr("10.10.0.255"),
			} {
				d := Select(st.views[egress], addr, st.authz, spec.Policy)
				if !d.Forward() {
					t.Fatalf("final trace %v addr %s outcome %s", trace, addr, d.Outcome)
				}
				want := spec.OldTenant
				if spec.Transfer[0].Contains(addr) {
					want = spec.NewTenant
				}
				if d.Tenant != want {
					t.Fatalf("final addr %s tenant %s want %s", addr, d.Tenant, want)
				}
			}
		}
	}

	needs := RecoveryNeeds(naive.Protocol, []Violation{{
		Trace:  []EventID{naive.CutEvent},
		Egress: "e2",
		Class:  "10.10.0.128/25",
	}}, spec.Policy)
	if !hasAckNeed(needs, "e2", naive.PrepositionStatement.Digest()) {
		t.Fatalf("missing e2 preposition ACK need: %+v", needs)
	}
}

func hasAckNeed(needs []AckRequirement, egress EgressID, digest Digest) bool {
	for _, n := range needs {
		if n.Egress == egress && n.Digest == digest {
			return true
		}
	}
	return false
}

func TestEqualLengthConflictRequiresWithdrawalAck(t *testing.T) {
	spec := testSpec("e1", "e2")
	spec.InitialClaims = []Claim{
		{Prefix: MustPrefix("10.10.0.0/24"), Tenant: "tenant-a"},
		{Prefix: MustPrefix("10.10.0.128/25"), Tenant: "tenant-a"},
	}
	plan, err := BuildSafePlan(spec)
	if err != nil {
		t.Fatal(err)
	}
	if plan.WithdrawStatement == nil || len(plan.ConflictClaimDigests) != 1 {
		t.Fatalf("expected one conflict withdrawal, got %+v", plan.ConflictClaimDigests)
	}
	cert, err := IssueCertificate(plan.Protocol, VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.RequireSafe(); err != nil {
		t.Fatalf("barrier plan should be safe and reachable: %v\n%+v", err, cert.Violations)
	}

	// Deliberately replay a forbidden intermediate that has the new statement
	// but not the withdrawal. Recovery must name the exact withdrawal ACK.
	forbidden := []EventID{EventID("deliver:" + plan.PrepositionStatement.ID + ":e1")}
	needs := RecoveryNeeds(plan.Protocol, []Violation{{
		Trace:  forbidden,
		Egress: "e1",
		Class:  "10.10.0.128/25",
	}}, spec.Policy)
	if plan.WithdrawStatement == nil || !hasAckNeed(needs, "e1", plan.WithdrawStatement.Digest()) {
		t.Fatalf("expected e1 withdrawal ACK, got %+v", needs)
	}
}

func TestReceiptReplayAndGenerationsAreEgressBound(t *testing.T) {
	spec := testSpec("e1", "e2")
	plan, err := BuildSafePlan(spec)
	if err != nil {
		t.Fatal(err)
	}
	pub1, priv1, err := GenerateEgressKey()
	if err != nil {
		t.Fatal(err)
	}
	pub2, priv2, err := GenerateEgressKey()
	if err != nil {
		t.Fatal(err)
	}
	keys := map[EgressID]ed25519.PublicKey{"e1": pub1, "e2": pub2}
	book := NewReceiptBook(keys)
	stmt := plan.PrepositionStatement

	old := stmt
	old.Generation = stmt.Generation - 1
	oldReceipt := SignReceipt(priv1, Receipt{
		Egress: "e1", PlanID: spec.PlanID, Statement: old.Digest(),
		Generation: old.Generation, ViewVersion: 8, Nonce: 2,
	})
	if _, err := book.Observe(oldReceipt); err != nil {
		t.Fatalf("valid old-generation receipt should be stored: %v", err)
	}
	if book.HasCurrentGeneration("e1", stmt) {
		t.Fatal("old-generation receipt satisfied current statement")
	}
	if phase := plan.CurrentPhase(book); phase != PhasePreposition {
		t.Fatalf("phase after stale ACK = %s, want %s", phase, PhasePreposition)
	}
	if missing := plan.MissingAcks(book); len(missing) != 2 {
		t.Fatalf("missing with only stale ACK = %+v", missing)
	}

	first := SignReceipt(priv1, Receipt{
		Egress: "e1", PlanID: spec.PlanID, Statement: stmt.Digest(),
		Generation: stmt.Generation, ViewVersion: 7, Nonce: 1,
	})
	if added, err := book.Observe(first); !added || err != nil {
		t.Fatalf("first receipt added=%v err=%v", added, err)
	}
	if added, err := book.Observe(first); added || !errors.Is(err, ErrReplay) {
		t.Fatalf("replay added=%v err=%v, want ErrReplay", added, err)
	}

	if missing := plan.MissingAcks(book); len(missing) != 1 || missing[0].Egress != "e2" {
		t.Fatalf("missing after e1 current ACK = %+v", missing)
	}

	forgedAsE2 := SignReceipt(priv1, Receipt{
		Egress: "e2", PlanID: spec.PlanID, Statement: stmt.Digest(),
		Generation: stmt.Generation, Nonce: 3,
	})
	if _, err := book.Observe(forgedAsE2); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("cross-egress receipt err=%v, want ErrBadSignature", err)
	}
	e2 := SignReceipt(priv2, Receipt{
		Egress: "e2", PlanID: spec.PlanID, Statement: stmt.Digest(),
		Generation: stmt.Generation, ViewVersion: 42, Nonce: 4,
	})
	if _, err := book.Observe(e2); err != nil {
		t.Fatal(err)
	}
	if batch := plan.NextBatch(book); batch.Phase != PhaseCut {
		t.Fatalf("NextBatch phase=%s, want cut", batch.Phase)
	}
	if sr, ok := book.Receipt("e2", stmt.Digest()); !ok || sr.Receipt.ViewVersion != 42 {
		t.Fatalf("egress-local generation receipt not retained: %+v %v", sr, ok)
	}
	controller := &Controller{Plan: plan, Book: book}
	if batch := controller.NextBatch(); batch.Phase != PhaseCut {
		t.Fatalf("controller phase = %s, want cut", batch.Phase)
	}
	controller.MarkCutExecuted()
	if batch := controller.NextBatch(); batch.Phase != PhaseDone || !batch.Done {
		t.Fatalf("controller after cut = %+v, want done", batch)
	}
}

func TestMergedPrefixDigestAndEgressLocalAck(t *testing.T) {
	merged := MergePrefixes([]netip.Prefix{
		MustPrefix("10.20.0.0/25"),
		MustPrefix("10.20.0.128/25"),
	})
	if len(merged) != 1 || merged[0].String() != "10.20.0.0/24" {
		t.Fatalf("merged = %v", merged)
	}
	mergedStmt := Statement{
		PlanID: "merge", ID: "merged", Generation: 1,
		Announces: []Claim{{Prefix: merged[0], Tenant: "tenant-b"}},
	}.Canonical()
	splitStmt := Statement{
		PlanID: "merge", ID: "split", Generation: 1,
		Announces: []Claim{
			{Prefix: MustPrefix("10.20.0.0/25"), Tenant: "tenant-b"},
			{Prefix: MustPrefix("10.20.0.128/25"), Tenant: "tenant-b"},
		},
	}.Canonical()
	if mergedStmt.Digest() == splitStmt.Digest() {
		t.Fatal("merged and split statements must not share an exact digest")
	}

	pub1, priv1, _ := GenerateEgressKey()
	pub2, _, _ := GenerateEgressKey()
	book := NewReceiptBook(map[EgressID]ed25519.PublicKey{"e1": pub1, "e2": pub2})
	sr := SignReceipt(priv1, Receipt{Egress: "e1", Statement: mergedStmt.Digest(), Nonce: 1})
	if _, err := book.Observe(sr); err != nil {
		t.Fatal(err)
	}
	if !book.Has("e1", mergedStmt.Digest()) {
		t.Fatal("e1 merged ACK missing")
	}
	if book.Has("e2", mergedStmt.Digest()) {
		t.Fatal("e1 ACK must not count for e2")
	}
	if book.Has("e1", splitStmt.Digest()) {
		t.Fatal("merged ACK must not satisfy a different exact statement digest")
	}
}

func TestOverlappingAnnouncementsAndExclusionHoles(t *testing.T) {
	authz := NewAuthorization().
		Grant("tenant-a", MustPrefix("10.30.0.0/24")).
		Grant("tenant-b", MustPrefix("10.30.0.192/26"))
	view := NewView("e1",
		Claim{Prefix: MustPrefix("10.30.0.0/24"), Tenant: "tenant-a"},
		Claim{Prefix: MustPrefix("10.30.0.0/25"), Tenant: "tenant-a"},
		Claim{
			Prefix:     MustPrefix("10.30.0.128/25"),
			Tenant:     "tenant-b",
			Exclusions: []netip.Prefix{MustPrefix("10.30.0.128/26")},
		},
		Claim{Prefix: MustPrefix("10.30.0.192/26"), Tenant: "tenant-b"},
	)
	for _, tc := range []struct {
		addr string
		want TenantID
	}{
		{"10.30.0.1", "tenant-a"},   // overlapping same-tenant /25 wins
		{"10.30.0.128", "tenant-a"}, // inside B exclusion hole, falls back to A /24
		{"10.30.0.192", "tenant-b"}, // B /26 wins
		{"10.30.0.200", "tenant-b"},
		{"10.30.0.255", "tenant-b"},
	} {
		d := Select(view, netip.MustParseAddr(tc.addr), authz, DefaultPolicy())
		if !d.Forward() || d.Tenant != tc.want {
			t.Fatalf("addr %s: outcome=%s tenant=%s prefix=%v, want %s", tc.addr, d.Outcome, d.Tenant, d.Prefix, tc.want)
		}
	}

	conflictView := NewView("e2",
		Claim{Prefix: MustPrefix("10.30.0.128/25"), Tenant: "tenant-a"},
		Claim{Prefix: MustPrefix("10.30.0.128/25"), Tenant: "tenant-b"},
	)
	d := Select(conflictView, netip.MustParseAddr("10.30.0.128"), authz, DefaultPolicy())
	if d.Outcome != OutcomeConflict {
		t.Fatalf("equal-length outcome = %s, want conflict", d.Outcome)
	}

	classes, err := EquivalenceClasses(MustPrefix("10.30.0.0/24"),
		MustPrefix("10.30.0.0/25"), MustPrefix("10.30.0.128/25"), MustPrefix("10.30.0.128/26"), MustPrefix("10.30.0.192/26"))
	if err != nil {
		t.Fatal(err)
	}
	if len(classes) != 3 {
		t.Fatalf("equivalence classes = %v, want /25,/26,/26 hole boundaries", classes)
	}
}

func TestCertificateTamperFailsVerification(t *testing.T) {
	plan, err := BuildSafePlan(testSpec("e1"))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := IssueCertificate(plan.Protocol, VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cert.Safe = false
	if _, err := VerifyCertificate(cert, plan.Protocol, VerifyOptions{}); !errors.Is(err, ErrCertificateMismatch) {
		t.Fatalf("tampered Safe verify err=%v, want ErrCertificateMismatch", err)
	}
}
