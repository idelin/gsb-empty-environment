package selftest

import (
	"errors"
	"fmt"
	"testing"

	. "formal.local/h26-c50a7102ec"
)

func testRegistry() Registry {
	return Registry{
		"tiny": {
			Name:    "tiny",
			Version: "v1",
			Func: func(ctx EvalContext) (bool, error) {
				return ctx.Input.Args[0] == 1, nil
			},
		},
	}
}

func TestCompilePolicyStrictAndExhaustiveEquivalent(t *testing.T) {
	rel := ArgMaskLEArg(0, 1<<63, 1)
	policy := &Policy{
		Rules: []Rule{
			{
				ID: "x64-execve", When: And(ArchIn(ArchX86_64), NrIs(59)),
				Action: ActionKill, Payload: Payload{Code: 42}, Audit: []string{"kill-audit"},
				Notify: []string{"kill-notify"},
			},
			{
				ID: "high-bit-relation", When: rel,
				Action: ActionErrno, Payload: ErrnoPayload(7), Audit: []string{"rel-audit"},
			},
			{
				ID: "named-tiny", When: NamedPred("tiny"),
				Action: ActionLog, Payload: Payload{Trace: 9}, Audit: []string{"named-audit"},
			},
			{
				ID: "equal-errno-later", When: ArgMaskEquals(1, 1, 1),
				Action: ActionErrno, Payload: ErrnoPayload(9), Audit: []string{"later-audit"},
			},
		},
	}

	dag, err := CompilePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}

	report, err := Verify(policy, dag, &Options{
		Registry: testRegistry(),
		Strict:   true,
		Exhaustive: &ExhaustiveConfig{
			ArgBits: 2,
			Numbers: map[Arch][]uint64{ArchX86_64: {0, 59}, ArchIA32: {0, 11}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("canonical DAG must be equivalent: %+v", report.Mismatches)
	}
	if report.CheckedInputs == 0 || !report.ExhaustiveComplete {
		t.Fatalf("expected complete exhaustive run, checked=%d complete=%v", report.CheckedInputs, report.ExhaustiveComplete)
	}
}

func TestDualArchAliasCollapseReturnsDisjointRegionsAndBothDecisions(t *testing.T) {
	aliases := NewAliasTable().Bind("widget", 4, 7)
	widget, err := aliases.Predicate("widget")
	if err != nil {
		t.Fatal(err)
	}
	policy := &Policy{Rules: []Rule{{
		ID: "block-widget", When: widget,
		Action: ActionErrno, Payload: ErrnoPayload(111),
		Audit: []string{"audit-widget"}, Notify: []string{"notify-widget"},
	}}}

	bad := &DAG{Nodes: []*DAGNode{
		{Test: And(ArchIn(ArchX86_64), NrIs(4)), True: Edge{To: 1}, False: Edge{To: 2}},
		{Decision: &Decision{
			Action: ActionErrno, Payload: ErrnoPayload(111),
			Audit: []string{"audit-widget"}, Notify: []string{"notify-widget"},
		}},
		{Decision: &Decision{Action: ActionAllow}},
	}}

	report, err := Verify(policy, bad, &Options{
		Exhaustive: &ExhaustiveConfig{Numbers: map[Arch][]uint64{
			ArchX86_64: {4, 7}, ArchIA32: {4, 7},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatal("alias collapse must be rejected")
	}

	foundIA32NR7 := false
	symbolicMismatches := 0
	for _, mismatch := range report.Mismatches {
		if mismatch.Region.AtomTruth != nil {
			symbolicMismatches++
		}
		w := mismatch.Region.Witness
		if w == nil {
			continue
		}
		nr := mismatch.Region.Nr
		if w.Arch == ArchIA32 && nr != nil && *nr == 7 &&
			mismatch.Reference.Action == ActionErrno &&
			mismatch.Candidate.Action == ActionAllow {
			foundIA32NR7 = true
		}
	}
	if !foundIA32NR7 {
		t.Fatalf("expected ia32 nr=7 escape witness with both decisions, got %+v", report.Mismatches)
	}
	if symbolicMismatches < 1 {
		t.Fatalf("expected symbolic mismatch regions")
	}
	assertRegionsDisjoint(t, report.Mismatches)

	var verr *VerificationError
	if !errors.As(report.Err(), &verr) {
		t.Fatalf("Report.Err should wrap VerificationError, got %T", report.Err())
	}
}

func TestSameNumberCrossArchRequiresTransparentExactness(t *testing.T) {
	policy := &Policy{Rules: []Rule{{
		ID: "block-x64-nr0", When: And(ArchIn(ArchX86_64), NrIs(0)),
		Action: ActionKill, Payload: Payload{Code: 1},
	}}}
	bad := &DAG{Nodes: []*DAGNode{
		{Test: NrIs(0), True: Edge{To: 1}, False: Edge{To: 2}},
		{Decision: &Decision{Action: ActionKill, Payload: Payload{Code: 1}}},
		{Decision: AllowDecisionPtr()},
	}}

	global, err := Verify(policy, bad, &Options{
		Exhaustive: &ExhaustiveConfig{Numbers: map[Arch][]uint64{
			ArchX86_64: {0, 3}, ArchIA32: {0, 3},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !global.OK {
		t.Fatalf("cross-arch over-block is stricter and should pass no-relax mode: %+v", global.Mismatches)
	}

	transparent, err := Verify(policy, bad, &Options{
		Transparent: &Zone{Archs: []Arch{ArchIA32}, NrRanges: [][2]uint64{{0, 0}}},
		Exhaustive: &ExhaustiveConfig{Numbers: map[Arch][]uint64{
			ArchX86_64: {0, 3}, ArchIA32: {0, 3},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if transparent.OK {
		t.Fatal("transparent ia32/nr0 zone must reject the architecture-blind DAG")
	}
	found := false
	for _, m := range transparent.Mismatches {
		for _, v := range m.Violations {
			if v.Code == ViolationStrongerInZone || v.Code == ViolationActionMismatch {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected transparent action mismatch, got %+v", transparent.Mismatches)
	}
}

func TestMaskedRelationIsUnsignedAcrossHighBit(t *testing.T) {
	p := ArgMaskLEArg(0, 1<<63, 1)
	cases := []struct {
		x, y uint64
		want bool
	}{
		{0, 0, true},
		{1 << 63, 1 << 63, true},
		{1 << 63, (1 << 63) - 1, false},
		{^uint64(0), 1 << 63, true}, // x&(1<<63)==1<<63, unsigned <=
		{(1 << 63) + 1, (1 << 63) - 1, false},
	}
	for _, tc := range cases {
		got, err := p.Eval(Input{Args: [6]uint64{tc.x, tc.y}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("ArgMaskLEArg(%#x,%#x)=%v want %v", tc.x, tc.y, got, tc.want)
		}
	}

	bits := uint(2)
	small := ArgMaskLEArg(0, 1<<(bits-1), 1).WithWidth(bits)
	policy := &Policy{Rules: []Rule{{When: small, Action: ActionErrno, Payload: ErrnoPayload(8)}}}
	dag, err := CompilePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Verify(policy, dag, &Options{
		Strict:     true,
		Exhaustive: &ExhaustiveConfig{ArgBits: bits, NrBits: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("small-width exhaustive relation reference failed: %+v", report.Mismatches)
	}

	relaxed := &DAG{Nodes: []*DAGNode{
		{Test: ArgMaskLEArg(0, ^uint64(0), 1), True: Edge{To: 1}, False: Edge{To: 2}},
		{Decision: &Decision{Action: ActionErrno, Payload: ErrnoPayload(8)}},
		{Decision: AllowDecisionPtr()},
	}}
	badReport, err := Verify(policy, relaxed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if badReport.OK {
		t.Fatal("relaxed/different dual-arg relation must be rejected")
	}
}

func TestEqualPriorityTakesNearestPayload(t *testing.T) {
	policy := &Policy{Rules: []Rule{
		{When: ArgMaskEquals(0, 1, 1), Action: ActionErrno, Payload: ErrnoPayload(101), Audit: []string{"first"}},
		{When: ArgMaskEquals(1, 1, 1), Action: ActionErrno, Payload: ErrnoPayload(202), Audit: []string{"second"}},
	}}

	bad := &DAG{Nodes: []*DAGNode{
		{Test: ArgMaskEquals(0, 1, 1), True: Edge{To: 1}, False: Edge{To: 2}},
		{Decision: &Decision{
			Action: ActionErrno, Payload: ErrnoPayload(101), Audit: []string{"first", "second"},
		}},
		{Test: ArgMaskEquals(1, 1, 1), True: Edge{To: 3}, False: Edge{To: 4}},
		{Decision: &Decision{Action: ActionErrno, Payload: ErrnoPayload(202), Audit: []string{"second"}}},
		{Decision: AllowDecisionPtr()},
	}}

	report, err := Verify(policy, bad, &Options{
		Exhaustive: &ExhaustiveConfig{ArgBits: 1, NrBits: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatal("stale equal-priority payload must be rejected")
	}
	foundPayload := false
	for _, m := range report.Mismatches {
		for _, v := range m.Violations {
			if v.Code == ViolationPayloadMismatch &&
				m.Reference.Payload.Errno == 202 &&
				m.Candidate.Payload.Errno == 101 {
				foundPayload = true
			}
		}
	}
	if !foundPayload {
		t.Fatalf("expected errno 202 vs 101 payload overwrite mismatch: %+v", report.Mismatches)
	}
}

func TestAuditAndNotifyLossRejected(t *testing.T) {
	policy := &Policy{Rules: []Rule{{
		When: NrIs(1), Action: ActionKill, Payload: Payload{Code: 5},
		Audit: []string{"audit://required"}, Notify: []string{"notify://required"},
	}}}
	dag := &DAG{Nodes: []*DAGNode{
		{Test: NrIs(1), True: Edge{To: 1}, False: Edge{To: 2}},
		{Decision: &Decision{Action: ActionKill, Payload: Payload{Code: 5}}},
		{Decision: AllowDecisionPtr()},
	}}
	report, err := Verify(policy, dag, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatal("missing audit/notify obligations must fail")
	}
	var codes []ViolationCode
	for _, m := range report.Mismatches {
		for _, v := range m.Violations {
			codes = append(codes, v.Code)
		}
	}
	if !containsCode(codes, ViolationMissingAudit) || !containsCode(codes, ViolationMissingNotify) {
		t.Fatalf("expected missing audit and notify, got %v", codes)
	}
}

func TestPassthroughNRScramblingRejectedEvenWhenActionStronger(t *testing.T) {
	nr := uint64(42)
	wrongNR := uint64(43)
	policy := &Policy{Rules: []Rule{{
		When: NrIs(1), Action: ActionErrno,
		Payload: Payload{Errno: 7, PassthroughNR: &nr},
	}}}
	dag := &DAG{Nodes: []*DAGNode{
		{Test: NrIs(1), True: Edge{To: 1}, False: Edge{To: 2}},
		{Decision: &Decision{Action: ActionKill, Payload: Payload{PassthroughNR: &wrongNR}}},
		{Decision: AllowDecisionPtr()},
	}}
	report, err := Verify(policy, dag, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatal("scrambled passthrough syscall number must fail even for stronger action")
	}
	found := false
	for _, m := range report.Mismatches {
		for _, v := range m.Violations {
			if v.Code == ViolationPayloadMismatch {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected payload/passthrough mismatch, got %+v", report.Mismatches)
	}
}

func TestForwardOnlyDAGValidation(t *testing.T) {
	backward := &DAG{Nodes: []*DAGNode{
		{Test: NrIs(1), True: Edge{To: 0}, False: Edge{To: 1}},
		{Decision: AllowDecisionPtr()},
	}}
	if err := backward.ValidateForward(); err == nil {
		t.Fatal("backward/self edge must be rejected")
	}
}

func TestIncrementalVerifierBindsNamedPredicateDigest(t *testing.T) {
	key := []byte("host-key")
	dag := namedBranchDAG()
	registry := Registry{
		"custom": {Name: "custom", Version: "v1", Func: func(EvalContext) (bool, error) { return true, nil }},
	}
	iv := &IncrementalVerifier{DAG: dag, Registry: registry, Key: key}
	result, err := iv.UpdateNamedPredicate(NamedPredicate{
		Name: "custom", Version: "v2",
		Func: func(EvalContext) (bool, error) { return false, nil },
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Change.OldDigest == result.Change.NewDigest {
		t.Fatal("named predicate version change must alter digest")
	}
	if err := VerifyPartialReuseCertificate(dag, result.Certificate, key); err != nil {
		t.Fatal(err)
	}

	cert, err := BuildCertificateWithRegistry(dag, registry, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCertificateWithRegistry(dag, registry, cert, key); err != nil {
		t.Fatal(err)
	}
	changed, err := registry.Put(NamedPredicate{Name: "custom", Version: "v3"})
	if err != nil {
		t.Fatal(err)
	}
	if changed.NewDigest == cert.Manifest.NamedPredicates[0].Digest {
		t.Fatal("test setup should change digest")
	}
	if err := VerifyCertificateWithRegistry(dag, registry, cert, key); err == nil {
		t.Fatal("stale named predicate implementation digest must be rejected")
	}
}

func TestProvenanceAndPartialReuseCertificateTampering(t *testing.T) {
	key := []byte("host-hmac-key-for-test")
	dag := namedBranchDAG()

	graph, err := BuildTraceGraph(dag)
	if err != nil {
		t.Fatal(err)
	}
	affected := graph.AffectedByNamed("custom")
	if fmt.Sprint(affected) != "[0 1]" {
		t.Fatalf("only root and named branch should be affected, got %v", affected)
	}
	if got := graph.ControlReachableIndices("custom"); fmt.Sprint(got) != "[1 2 3]" {
		t.Fatalf("named control branches should be [1 2 3], got %v", got)
	}

	cert, err := BuildCertificate(dag, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCertificate(dag, cert, key); err != nil {
		t.Fatal(err)
	}

	originalCode := dag.Nodes[2].Decision.Payload.Code
	dag.Nodes[2].Decision.Payload.Code = 999
	if err := VerifyCertificate(dag, cert, key); err == nil {
		t.Fatal("DAG payload tampering must invalidate provenance certificate")
	}
	dag.Nodes[2].Decision.Payload.Code = originalCode

	tampered := *cert
	tampered.Manifest.Root = "sha256:forged"
	if err := VerifyCertificate(dag, &tampered, key); err == nil {
		t.Fatal("tampered certificate manifest must fail HMAC verification")
	}

	change := NamedChange{Name: "custom", OldDigest: "sha256:v1", NewDigest: "sha256:v2"}
	var gotIndices []int
	result, err := RecomputeAffectedBranches(dag, change, key, func(indices []int) ([]ReuseEntry, error) {
		gotIndices = append(gotIndices, indices...)
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(gotIndices) != "[0 1]" || result.RebuildCalls != 1 {
		t.Fatalf("rebuild must be called once with [0 1], got indices=%v calls=%d", gotIndices, result.RebuildCalls)
	}
	if err := VerifyPartialReuseCertificate(dag, result.Certificate, key); err != nil {
		t.Fatalf("valid partial reuse certificate failed: %v", err)
	}
	for _, idx := range []int{2, 3, 4} {
		found := false
		for _, entry := range result.Certificate.Reused {
			if entry.DAGIndex == idx {
				found = true
			}
		}
		if !found {
			t.Fatalf("unaffected node %d should have reuse certificate", idx)
		}
	}

	if err := VerifyPartialReuseCertificate(dag, result.Certificate, nil); err == nil {
		t.Fatal("partial reuse verification without host HMAC key must be refused")
	}

	tamperedReuse := clonePartialCert(result.Certificate)
	tamperedReuse.Reused[0].Digest = "sha256:forged-partial-reuse"
	if err := VerifyPartialReuseCertificate(dag, tamperedReuse, key); err == nil {
		t.Fatal("tampered partial reuse entry must be rejected")
	}

	full, err := BuildCertificate(dag, key)
	if err != nil {
		t.Fatal(err)
	}
	affectedEntry := ReuseEntry{
		NodeID:   full.Manifest.Nodes[1].ID,
		DAGIndex: 1,
		Digest:   full.Manifest.Nodes[1].Digest,
	}
	overclaimed := append([]ReuseEntry(nil), result.Certificate.Reused...)
	overclaimed = append(overclaimed, affectedEntry)
	evilButSigned := IssuePartialReuseCertificate(
		key, change, result.Certificate.BaseRoot, full.Manifest.Root,
		overclaimed, result.Certificate.Recomputed,
	)
	if err := VerifyPartialReuseCertificate(dag, evilButSigned, key); err == nil {
		t.Fatal("reusing a branch that depends on changed named predicate must be rejected")
	}

	forged := IssuePartialReuseCertificate(
		[]byte("attacker-key"), change, result.Certificate.BaseRoot, full.Manifest.Root,
		result.Certificate.Reused, result.Certificate.Recomputed,
	)
	if err := VerifyPartialReuseCertificate(dag, forged, key); err == nil {
		t.Fatal("certificate signed with attacker key must be rejected")
	}
}

func namedBranchDAG() *DAG {
	return &DAG{Nodes: []*DAGNode{
		{Test: NrIs(42), True: Edge{To: 1}, False: Edge{To: 4}},
		{Test: NamedPred("custom"), True: Edge{To: 2}, False: Edge{To: 3}},
		{Decision: &Decision{Action: ActionKill, Payload: Payload{Code: 1}}},
		{Decision: &Decision{Action: ActionErrno, Payload: ErrnoPayload(2)}},
		{Decision: AllowDecisionPtr()},
	}}
}

func AllowDecisionPtr() *Decision {
	d := AllowDecision()
	return &d
}

func clonePartialCert(in *PartialReuseCertificate) *PartialReuseCertificate {
	cp := *in
	cp.Reused = append([]ReuseEntry(nil), in.Reused...)
	cp.Recomputed = append([]ReuseEntry(nil), in.Recomputed...)
	cp.Signature = append([]byte(nil), in.Signature...)
	return &cp
}

func containsCode(codes []ViolationCode, want ViolationCode) bool {
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}

func assertRegionsDisjoint(t *testing.T, mismatches []Mismatch) {
	t.Helper()
	var regions []Region
	for _, m := range mismatches {
		if m.Region.AtomTruth != nil {
			regions = append(regions, m.Region)
		}
	}
	for i := 0; i < len(regions); i++ {
		for j := i + 1; j < len(regions); j++ {
			disjoint := false
			for key, value := range regions[i].AtomTruth {
				if other, ok := regions[j].AtomTruth[key]; ok && other != value {
					disjoint = true
					break
				}
			}
			if !disjoint {
				t.Fatalf("regions %d and %d are not disjoint: %+v vs %+v", i, j, regions[i].AtomTruth, regions[j].AtomTruth)
			}
		}
	}
}
