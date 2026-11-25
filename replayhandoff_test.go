package replayhandoff

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func signerSet(t *testing.T, nodes ...NodeID) (map[NodeID]*NodeSigner, KeyRing) {
	t.Helper()
	signers := make(map[NodeID]*NodeSigner, len(nodes))
	keys := make(KeyRing, len(nodes))
	for i, node := range nodes {
		var seed [ed25519.SeedSize]byte
		for j := range seed {
			seed[j] = byte(i + 1)
		}
		seed[0] = byte(i*37 + 11)
		signer, err := NewSeedSigner(node, seed)
		if err != nil {
			t.Fatalf("NewSeedSigner(%q): %v", node, err)
		}
		signers[node] = signer
		keys[node] = append(ed25519.PublicKey(nil), signer.Public...)
	}
	return signers, keys
}

func mustLog(t *testing.T, node NodeID, context ContextID, signer *NodeSigner) *NodeLog {
	t.Helper()
	log, err := NewNodeLog(node, context, signer)
	if err != nil {
		t.Fatalf("NewNodeLog: %v", err)
	}
	return log
}

func commitRange(t *testing.T, log *NodeLog, partition Partition, iv Interval, signer *NodeSigner) {
	t.Helper()
	if _, err := log.CommitInterval(partition, iv, signer); err != nil {
		t.Fatalf("CommitInterval: %v", err)
	}
}

func mustProof(t *testing.T, log *NodeLog, partition Partition, bounds Interval, signer *NodeSigner) RangeProof {
	t.Helper()
	proof, err := log.Proof(partition, bounds, signer)
	if err != nil {
		t.Fatalf("Proof: %v", err)
	}
	return proof
}

func mustHandoff(t *testing.T, config HandoffConfig) *Handoff {
	t.Helper()
	h, err := NewHandoff(config)
	if err != nil {
		t.Fatalf("NewHandoff: %v", err)
	}
	return h
}

func TestSequenceAndIntervalBoundaries(t *testing.T) {
	max := Interval{Lo: SequenceLimit - 1, Hi: SequenceLimit}
	if err := max.Validate(); err != nil {
		t.Fatalf("max interval validate: %v", err)
	}
	if !max.Contains(SequenceLimit-1) || max.Contains(SequenceLimit) {
		t.Fatal("half-open upper boundary is wrong")
	}
	if _, err := NewInterval(5, 0); !errors.Is(err, ErrInvertedInterval) {
		t.Fatalf("inverted interval error = %v", err)
	}
	if _, err := NewInterval(0, SequenceLimit+1); !errors.Is(err, ErrOutsideSequenceSpace) {
		t.Fatalf("outside interval error = %v", err)
	}
	if _, err := ParseInterval("[5,)"); !errors.Is(err, ErrMissingEndpoint) {
		t.Fatalf("missing endpoint error = %v", err)
	}
	if _, err := ParseInterval("[,9)"); !errors.Is(err, ErrMissingEndpoint) {
		t.Fatalf("missing endpoint error = %v", err)
	}
	parsed, err := ParseInterval("[1099511627775,1099511627776)")
	if err != nil {
		t.Fatalf("2^40 boundary parse: %v", err)
	}
	if parsed != max {
		t.Fatalf("boundary parsed = %v", parsed)
	}

	all := MustIntervalSet(Interval{Lo: 0, Hi: SequenceLimit})
	firstHalf := MustIntervalSet(Interval{Lo: 0, Hi: 1 << 39})
	secondHalf := MustIntervalSet(Interval{Lo: 1 << 39, Hi: SequenceLimit})
	if !all.Subtract(firstHalf).Equal(secondHalf) {
		t.Fatalf("complement partition mismatch: %s", all.Subtract(firstHalf))
	}
	if !all.ContainsInterval(Interval{Lo: 0, Hi: SequenceLimit}) {
		t.Fatal("full set should contain full interval")
	}
	if all.Contains(SequenceLimit) {
		t.Fatal("upper boundary must be exclusive")
	}
}

func TestIntervalSetCopyOnWrite(t *testing.T) {
	base := MustIntervalSet(Interval{Lo: 10, Hi: 20})
	next, err := base.Add(Interval{Lo: 30, Hi: 40})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if base.Contains(30) {
		t.Fatal("old snapshot mutated")
	}
	if !next.Contains(30) {
		t.Fatal("new snapshot missing interval")
	}
	overlap, err := NewIntervalSet(Interval{Lo: 15, Hi: 35})
	if err != nil {
		t.Fatalf("NewIntervalSet: %v", err)
	}
	if got := next.Intersect(overlap).Intervals(); len(got) != 2 ||
		got[0] != (Interval{Lo: 15, Hi: 20}) ||
		got[1] != (Interval{Lo: 30, Hi: 35}) {
		t.Fatalf("intersection = %v", got)
	}
	adjacent, err := NewIntervalSet(Interval{Lo: 20, Hi: 30})
	if err != nil {
		t.Fatalf("NewIntervalSet: %v", err)
	}
	union := base.Union(adjacent)
	if union.Count() != 1 {
		t.Fatalf("adjacent union count = %d", union.Count())
	}
	if !union.ContainsInterval(Interval{Lo: 10, Hi: 30}) {
		t.Fatalf("adjacent union = %s", union)
	}
}

func TestNodeLogChainAndProof(t *testing.T) {
	signers, keys := signerSet(t, "node-a")
	signer := signers["node-a"]
	partition := NewPartition("ctx-1", "sender-1")
	log := mustLog(t, "node-a", "ctx-1", signer)
	commitRange(t, log, partition, Interval{Lo: 4, Hi: 8}, signer)
	commitRange(t, log, partition, Interval{Lo: 12, Hi: 13}, signer)
	if log.Generation() != 2 {
		t.Fatalf("generation = %d", log.Generation())
	}
	if err := log.Verify(keys); err != nil {
		t.Fatalf("Verify log: %v", err)
	}
	proof := mustProof(t, log, partition, Interval{Lo: 0, Hi: 16}, signer)
	if err := proof.Verify(keys); err != nil {
		t.Fatalf("Verify proof: %v", err)
	}
	if err := proof.VerifyAgainstLog(log, keys); err != nil {
		t.Fatalf("VerifyAgainstLog: %v", err)
	}
	if !proof.Seen.Contains(4) || proof.Seen.Contains(9) || proof.Seen.Contains(8) || !proof.Seen.Contains(12) {
		t.Fatalf("proof seen = %s", proof.Seen)
	}
	bad := proof
	bad.Seen = MustIntervalSet(Interval{Lo: 1, Hi: 2})
	if err := bad.Verify(keys); err == nil {
		t.Fatal("tampered proof verified")
	}

	batch := log.Batches()[0]
	batch.Header.Generation = 99
	if err := batch.Verify(keys); err == nil {
		t.Fatal("tampered header verified")
	}
}

func TestDecisionAcceptRejectWaitAndCrossNodeRace(t *testing.T) {
	signers, keys := signerSet(t, "old-a", "old-b", "new")
	ctx := ContextID("ctx")
	partition := NewPartition("ctx", "sender")
	logA := mustLog(t, "old-a", ctx, signers["old-a"])
	logB := mustLog(t, "old-b", ctx, signers["old-b"])
	commitRange(t, logA, partition, Interval{Lo: 5, Hi: 6}, signers["old-a"])
	// old-b has no delivery.

	proofA := mustProof(t, logA, partition, Interval{Lo: 0, Hi: 16}, signers["old-a"])
	proofB := mustProof(t, logB, partition, Interval{Lo: 0, Hi: 16}, signers["old-b"])
	h := mustHandoff(t, HandoffConfig{
		Nodes:       []NodeID{"old-a", "old-b"},
		NewNode:     "new",
		Leases:      []Lease{{Node: "old-a", Closed: true, Barrier: proofA.Barrier}, {Node: "old-b", Closed: true, Barrier: proofB.Barrier}},
		KnownProofs: []RangeProof{proofA, proofB},
		Keys:        keys,
	})
	verdict, err := h.EvaluateSequence(partition, 5)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Decision != DecisionReject || len(verdict.SeenBy) == 0 {
		t.Fatalf("seq 5 verdict = %+v", verdict)
	}
	verdict, err = h.EvaluateSequence(partition, 6)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Decision != DecisionAccept {
		t.Fatalf("seq 6 verdict = %+v", verdict)
	}

	// Missing old-b evidence must wait and point at the cheapest page.
	missing := mustHandoff(t, HandoffConfig{
		Nodes:       []NodeID{"old-a", "old-b"},
		NewNode:     "new",
		Leases:      []Lease{{Node: "old-a", Closed: true, Barrier: proofA.Barrier}, {Node: "old-b", Closed: true, Barrier: proofB.Barrier}},
		KnownProofs: []RangeProof{proofA},
		CandidatePages: []ProofPage{
			{ID: "page-b", Cost: 3, Proofs: []RangeProof{proofB}},
		},
		Keys: keys,
	})
	verdict, err = missing.EvaluateSequence(partition, 6)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Decision != DecisionWait || !verdict.Resolvable {
		t.Fatalf("missing verdict = %+v", verdict)
	}
	if len(verdict.NeededPages) != 1 || verdict.NeededPages[0].ID != "page-b" || verdict.NeededCost != 3 {
		t.Fatalf("needed pages = %+v cost=%d", verdict.NeededPages, verdict.NeededCost)
	}
}

func TestLeaseNotClosedAndStaleProof(t *testing.T) {
	signers, keys := signerSet(t, "old", "new")
	partition := NewPartition("ctx", "sender")
	log := mustLog(t, "old", "ctx", signers["old"])
	proof := mustProof(t, log, partition, Interval{Lo: 0, Hi: 16}, signers["old"])
	base := HandoffConfig{
		Nodes:       []NodeID{"old"},
		NewNode:     "new",
		KnownProofs: []RangeProof{proof},
		Keys:        keys,
	}
	base.Leases = []Lease{{Node: "old", Closed: false, Barrier: 0}}
	h := mustHandoff(t, base)
	verdict, err := h.EvaluateSequence(partition, 1)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Decision != DecisionWait || !verdict.LeaseBlocked {
		t.Fatalf("open lease verdict = %+v", verdict)
	}

	base.Leases = []Lease{{Node: "old", Closed: true, Barrier: 1}}
	stale := mustHandoff(t, base)
	verdict, err = stale.EvaluateSequence(partition, 1)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Decision != DecisionWait || verdict.LeaseBlocked {
		t.Fatalf("stale proof verdict = %+v", verdict)
	}
	if len(verdict.MissingNodes) != 1 || verdict.MissingNodes[0] != "old" {
		t.Fatalf("stale proof missing nodes = %v", verdict.MissingNodes)
	}
}

func TestCrossContextProofIsNotReusable(t *testing.T) {
	signers, keys := signerSet(t, "old", "new")
	ctxA := ContextID("ctx-a")
	logA := mustLog(t, "old", ctxA, signers["old"])
	pa := NewPartition("ctx-a", "sender")
	pb := NewPartition("ctx-b", "sender")
	proofA := mustProof(t, logA, pa, Interval{Lo: 0, Hi: 16}, signers["old"])
	h := mustHandoff(t, HandoffConfig{
		Nodes:       []NodeID{"old"},
		NewNode:     "new",
		Leases:      []Lease{{Node: "old", Closed: true, Barrier: 0}},
		KnownProofs: []RangeProof{proofA},
		Keys:        keys,
	})
	verdict, err := h.EvaluateSequence(pb, 1)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Decision != DecisionWait || verdict.Resolvable {
		t.Fatalf("cross-context verdict = %+v", verdict)
	}
}

func TestUncoveredRangeIsUnknownNotEmpty(t *testing.T) {
	signers, keys := signerSet(t, "old", "new")
	partition := NewPartition("ctx", "sender")
	log := mustLog(t, "old", "ctx", signers["old"])
	proof := mustProof(t, log, partition, Interval{Lo: 0, Hi: 8}, signers["old"])
	h := mustHandoff(t, HandoffConfig{
		Nodes:       []NodeID{"old"},
		NewNode:     "new",
		Leases:      []Lease{{Node: "old", Closed: true, Barrier: 0}},
		KnownProofs: []RangeProof{proof},
		Keys:        keys,
	})
	inside, err := h.EvaluateSequence(partition, 7)
	if err != nil {
		t.Fatal(err)
	}
	if inside.Decision != DecisionAccept {
		t.Fatalf("inside verdict = %+v", inside)
	}
	outside, err := h.EvaluateSequence(partition, 8)
	if err != nil {
		t.Fatal(err)
	}
	if outside.Decision != DecisionWait || outside.Resolvable {
		t.Fatalf("outside range must be unknown, got %+v", outside)
	}
}

func TestBatchMinimumPagesAndCertificate(t *testing.T) {
	signers, keys := signerSet(t, "old-a", "old-b", "new")
	partition := NewPartition("ctx", "sender")
	logA := mustLog(t, "old-a", "ctx", signers["old-a"])
	logB := mustLog(t, "old-b", "ctx", signers["old-b"])
	commitRange(t, logA, partition, Interval{Lo: 5, Hi: 6}, signers["old-a"])

	proofA0 := mustProof(t, logA, partition, Interval{Lo: 0, Hi: 16}, signers["old-a"])
	proofB0 := mustProof(t, logB, partition, Interval{Lo: 0, Hi: 16}, signers["old-b"])
	proofA16 := mustProof(t, logA, partition, Interval{Lo: 16, Hi: 32}, signers["old-a"])
	proofB16 := mustProof(t, logB, partition, Interval{Lo: 16, Hi: 32}, signers["old-b"])

	pageA := ProofPage{ID: "page-a", Cost: 3, Proofs: []RangeProof{proofA16}}
	pageB := ProofPage{ID: "page-b", Cost: 4, Proofs: []RangeProof{proofB16}}
	pageAB := ProofPage{ID: "page-ab", Cost: 6, Proofs: []RangeProof{proofB16, proofA16}}
	pageABDup := ProofPage{ID: "page-ab-duplicate", Cost: 6, Proofs: []RangeProof{proofA16, proofB16, proofA16}}

	h := mustHandoff(t, HandoffConfig{
		Nodes:       []NodeID{"old-a", "old-b"},
		NewNode:     "new",
		Leases:      []Lease{{Node: "old-a", Closed: true, Barrier: proofA0.Barrier}, {Node: "old-b", Closed: true, Barrier: 0}},
		KnownProofs: []RangeProof{proofA0, proofB0},
		// Deliberately out of order and with duplicate pages.
		CandidatePages:    []ProofPage{pageB, pageABDup, pageA, pageAB},
		Keys:              keys,
		CertificateSigner: signers["new"],
	})
	queries := []QuerySegment{{Partition: partition, Range: Interval{Lo: 0, Hi: 32}}}
	report, err := h.AnalyzeBatch(queries)
	if err != nil {
		t.Fatalf("AnalyzeBatch: %v", err)
	}
	if !report.Feasible || !report.FullyDetermined {
		t.Fatalf("report not complete: feasible=%v determined=%v", report.Feasible, report.FullyDetermined)
	}
	if len(report.Supplement) != 1 || report.Supplement[0].ID != "page-ab" || report.SupplementCost != 6 {
		t.Fatalf("supplement = %+v cost=%d", report.Supplement, report.SupplementCost)
	}
	if report.Normalization.PageDuplicates != 1 || report.Normalization.ProofDuplicates != 1 {
		t.Fatalf("normalization = %+v", report.Normalization)
	}
	if report.Segments[0].Decision != DecisionWait || report.Segments[0].ProjectedDecision != DecisionMixed {
		t.Fatalf("segment decisions current=%s projected=%s", report.Segments[0].Decision, report.Segments[0].ProjectedDecision)
	}
	part5, err := report.DecisionAt(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if part5.Decision != DecisionReject {
		t.Fatalf("seq 5 projected = %+v", part5)
	}
	part16, err := report.DecisionAt(0, 16)
	if err != nil {
		t.Fatal(err)
	}
	if part16.Decision != DecisionAccept {
		t.Fatalf("seq 16 projected = %+v", part16)
	}
	if report.Certificate == nil {
		t.Fatal("missing certificate")
	}
	if err := VerifyCertificate(report.Certificate, keys); err != nil {
		t.Fatalf("VerifyCertificate: %v", err)
	}
	if err := VerifyCertificateAuthenticated(report.Certificate, keys); err != nil {
		t.Fatalf("VerifyCertificateAuthenticated: %v", err)
	}
	exhaustive, err := VerifyCertificateExhaustive(report.Certificate, keys, 32)
	if err != nil {
		t.Fatalf("VerifyCertificateExhaustive: %v", err)
	}
	if exhaustive.SequencesChecked != 32 || exhaustive.MinimumCost != 6 ||
		len(exhaustive.MinimumPages) != 1 || exhaustive.MinimumPages[0] != "page-ab" {
		t.Fatalf("exhaustive report = %+v", exhaustive)
	}
	tampered := *report.Certificate
	tampered.SelectedPages = []string{"page-a"}
	if err := VerifyCertificate(&tampered, keys); err == nil {
		t.Fatal("tampered certificate verified")
	}
	encoded, err := json.Marshal(report.Certificate)
	if err != nil {
		t.Fatalf("json marshal certificate: %v", err)
	}
	var decoded CoverageCertificate
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json unmarshal certificate: %v", err)
	}
	if err := VerifyCertificateAuthenticated(&decoded, keys); err != nil {
		t.Fatalf("verify decoded certificate: %v", err)
	}
}

func TestRootConflictIsReportedAndIsolated(t *testing.T) {
	signers, keys := signerSet(t, "old", "new")
	partition := NewPartition("ctx", "sender")
	first := mustLog(t, "old", "ctx", signers["old"])
	second := mustLog(t, "old", "ctx", signers["old"])
	commitRange(t, first, partition, Interval{Lo: 4, Hi: 5}, signers["old"])
	commitRange(t, second, partition, Interval{Lo: 9, Hi: 10}, signers["old"])
	proofFirst := mustProof(t, first, partition, Interval{Lo: 0, Hi: 8}, signers["old"])
	proofSecond := mustProof(t, second, partition, Interval{Lo: 8, Hi: 16}, signers["old"])
	h := mustHandoff(t, HandoffConfig{
		Nodes:       []NodeID{"old"},
		NewNode:     "new",
		Leases:      []Lease{{Node: "old", Closed: true, Barrier: 1}},
		KnownProofs: []RangeProof{proofFirst, proofSecond},
		Keys:        keys,
	})
	if len(h.Normalization().Conflicts) != 1 {
		t.Fatalf("conflicts = %+v", h.Normalization().Conflicts)
	}
	conflict := h.Normalization().Conflicts[0]
	if conflict.Partition != partition || conflict.Barrier != 1 || len(conflict.Roots) != 2 {
		t.Fatalf("conflict = %+v", conflict)
	}
	// The two roots are never unioned in one proof; separate atoms each retain
	// their own witness root.
	interval, err := h.EvaluateInterval(partition, Interval{Lo: 0, Hi: 16})
	if err != nil {
		t.Fatal(err)
	}
	if interval.Decision != DecisionMixed {
		t.Fatalf("interval decision = %s", interval.Decision)
	}
	for _, part := range interval.Parts {
		if part.Decision == DecisionAccept && len(part.Witnesses) == 0 {
			t.Fatalf("part = %+v", part)
		}
	}
}

func TestNewNodeLocalSeenRejectsRace(t *testing.T) {
	signers, keys := signerSet(t, "old", "new")
	partition := NewPartition("ctx", "sender")
	log := mustLog(t, "old", "ctx", signers["old"])
	proof := mustProof(t, log, partition, Interval{Lo: 0, Hi: 16}, signers["old"])
	h := mustHandoff(t, HandoffConfig{
		Nodes:       []NodeID{"old"},
		NewNode:     "new",
		Leases:      []Lease{{Node: "old", Closed: true, Barrier: 0}},
		LocalSeen:   map[Partition]IntervalSet{partition: MustIntervalSet(Interval{Lo: 7, Hi: 8})},
		KnownProofs: []RangeProof{proof},
		Keys:        keys,
	})
	verdict, err := h.EvaluateSequence(partition, 7)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Decision != DecisionReject || len(verdict.SeenBy) != 1 || verdict.SeenBy[0] != "new" {
		t.Fatalf("local race verdict = %+v", verdict)
	}
	verdict, err = h.EvaluateSequence(partition, 6)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Decision != DecisionAccept {
		t.Fatalf("unraced verdict = %+v", verdict)
	}
}

func TestPageLimitAndOutOfRangeRejected(t *testing.T) {
	signers, keys := signerSet(t, "old")
	partition := NewPartition("ctx", "sender")
	log := mustLog(t, "old", "ctx", signers["old"])
	pages := make([]ProofPage, 0, MaxCandidatePages+1)
	for i := 0; i <= MaxCandidatePages; i++ {
		rangeProof := mustProof(t, log, partition, Interval{Lo: Seq(i), Hi: Seq(i + 1)}, signers["old"])
		pages = append(pages, ProofPage{ID: string(rune('a' + i)), Cost: int64(i), Proofs: []RangeProof{rangeProof}})
	}
	if _, err := NewHandoff(HandoffConfig{
		Nodes:          []NodeID{"old"},
		NewNode:        "new",
		Leases:         []Lease{{Node: "old", Closed: true, Barrier: 0}},
		CandidatePages: pages,
		Keys:           keys,
	}); err == nil {
		t.Fatal("more than 20 distinct pages accepted")
	}
	if _, err := NewHandoff(HandoffConfig{
		Nodes:   []NodeID{"old"},
		NewNode: "new",
		Keys:    keys,
		KnownProofs: []RangeProof{{
			Node:      "old",
			Partition: partition,
			Range:     Interval{Lo: 5, Hi: 4},
			Seen:      IntervalSet{},
		}},
	}); err == nil {
		t.Fatal("inverted proof range accepted")
	}
}

func TestFortiethBitBoundaryHandoff(t *testing.T) {
	signers, keys := signerSet(t, "old", "new")
	partition := NewPartition("ctx", "sender")
	log := mustLog(t, "old", "ctx", signers["old"])
	bounds := Interval{Lo: SequenceLimit - 2, Hi: SequenceLimit}
	proof := mustProof(t, log, partition, bounds, signers["old"])
	h := mustHandoff(t, HandoffConfig{
		Nodes:       []NodeID{"old"},
		NewNode:     "new",
		Leases:      []Lease{{Node: "old", Closed: true, Barrier: 0}},
		KnownProofs: []RangeProof{proof},
		Keys:        keys,
	})
	verdict, err := h.EvaluateSequence(partition, SequenceLimit-1)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Decision != DecisionAccept {
		t.Fatalf("2^40-1 verdict = %+v", verdict)
	}
	if _, err := h.EvaluateSequence(partition, SequenceLimit); !errors.Is(err, ErrOutsideSequenceSpace) {
		t.Fatalf("2^40 sequence error = %v", err)
	}
	report, err := h.AnalyzeBatch([]QuerySegment{{Partition: partition, Range: bounds}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.FullyDetermined || report.Segments[0].ProjectedDecision != DecisionAccept {
		t.Fatalf("boundary batch = %+v", report.Segments[0])
	}
}

func TestOpenLeaseCertificateIsWaitAndBlocked(t *testing.T) {
	signers, keys := signerSet(t, "old", "new")
	partition := NewPartition("ctx", "sender")
	log := mustLog(t, "old", "ctx", signers["old"])
	proof := mustProof(t, log, partition, Interval{Lo: 0, Hi: 8}, signers["old"])
	h := mustHandoff(t, HandoffConfig{
		Nodes:       []NodeID{"old"},
		NewNode:     "new",
		Leases:      []Lease{{Node: "old", Closed: false}},
		KnownProofs: []RangeProof{proof},
		Keys:        keys,
	})
	report, err := h.AnalyzeBatch([]QuerySegment{{Partition: partition, Range: Interval{Lo: 0, Hi: 8}}})
	if err != nil {
		t.Fatal(err)
	}
	if report.FullyDetermined || !report.LeaseBlocked {
		t.Fatalf("lease report feasible=%v fully=%v blocked=%v", report.Feasible, report.FullyDetermined, report.LeaseBlocked)
	}
	if report.Segments[0].ProjectedDecision != DecisionWait ||
		len(report.Segments[0].ProjectedParts) != 1 ||
		report.Segments[0].ProjectedParts[0].Reason != ReasonLeaseOpen {
		t.Fatalf("lease segment = %+v", report.Segments[0])
	}
	if err := VerifyCertificate(report.Certificate, keys); err != nil {
		t.Fatalf("wait certificate should still be integrity-verifiable: %v", err)
	}
}

func TestCheapestPageMayResolveByShowingSeen(t *testing.T) {
	signers, keys := signerSet(t, "old-a", "old-b", "new")
	partition := NewPartition("ctx", "sender")
	logA := mustLog(t, "old-a", "ctx", signers["old-a"])
	logB := mustLog(t, "old-b", "ctx", signers["old-b"])
	commitRange(t, logB, partition, Interval{Lo: 5, Hi: 6}, signers["old-b"])
	proofA := mustProof(t, logA, partition, Interval{Lo: 0, Hi: 8}, signers["old-a"])
	proofBSeen := mustProof(t, logB, partition, Interval{Lo: 0, Hi: 8}, signers["old-b"])
	h := mustHandoff(t, HandoffConfig{
		Nodes:   []NodeID{"old-a", "old-b"},
		NewNode: "new",
		Leases:  []Lease{{Node: "old-a", Closed: true, Barrier: 0}, {Node: "old-b", Closed: true, Barrier: 1}},
		CandidatePages: []ProofPage{
			{ID: "show-a-unseen", Cost: 2, Proofs: []RangeProof{proofA}},
			{ID: "show-b-seen", Cost: 1, Proofs: []RangeProof{proofBSeen}},
		},
		Keys: keys,
	})
	verdict, err := h.EvaluateSequence(partition, 5)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Decision != DecisionWait || !verdict.Resolvable {
		t.Fatalf("verdict = %+v", verdict)
	}
	if len(verdict.NeededPages) != 1 || verdict.NeededPages[0].ID != "show-b-seen" || verdict.NeededCost != 1 {
		t.Fatalf("needed pages = %+v cost=%d", verdict.NeededPages, verdict.NeededCost)
	}
}

func TestGlobalMinimumAcrossMultipleQueries(t *testing.T) {
	signers, keys := signerSet(t, "old-a", "old-b", "new")
	partition := NewPartition("ctx", "sender")
	logA := mustLog(t, "old-a", "ctx", signers["old-a"])
	logB := mustLog(t, "old-b", "ctx", signers["old-b"])
	proofA0 := mustProof(t, logA, partition, Interval{Lo: 0, Hi: 8}, signers["old-a"])
	proofB0 := mustProof(t, logB, partition, Interval{Lo: 0, Hi: 4}, signers["old-b"])
	proofB8 := mustProof(t, logB, partition, Interval{Lo: 0, Hi: 8}, signers["old-b"])
	proofA16 := mustProof(t, logA, partition, Interval{Lo: 16, Hi: 24}, signers["old-a"])
	proofB16 := mustProof(t, logB, partition, Interval{Lo: 16, Hi: 24}, signers["old-b"])
	h := mustHandoff(t, HandoffConfig{
		Nodes:       []NodeID{"old-a", "old-b"},
		NewNode:     "new",
		Leases:      []Lease{{Node: "old-a", Closed: true, Barrier: 0}, {Node: "old-b", Closed: true, Barrier: 0}},
		KnownProofs: []RangeProof{proofA0, proofB0},
		CandidatePages: []ProofPage{
			{ID: "page-b8", Cost: 2, Proofs: []RangeProof{proofB8}},
			{ID: "page-both16", Cost: 4, Proofs: []RangeProof{proofB16, proofA16}},
		},
		Keys: keys,
	})
	queries := []QuerySegment{
		{Partition: partition, Range: Interval{Lo: 16, Hi: 24}},
		{Partition: partition, Range: Interval{Lo: 0, Hi: 8}},
	}
	report, err := h.AnalyzeBatch(queries)
	if err != nil {
		t.Fatal(err)
	}
	if !report.FullyDetermined {
		t.Fatalf("global analysis not complete")
	}
	if len(report.Supplement) != 2 || report.SupplementCost != 6 {
		t.Fatalf("supplement = %+v cost=%d", report.Supplement, report.SupplementCost)
	}
	if report.Segments[0].Index != 0 || report.Segments[1].Index != 1 {
		t.Fatalf("query order not preserved: %+v", report.Segments)
	}
	if report.Segments[0].ProjectedDecision != DecisionAccept || report.Segments[1].ProjectedDecision != DecisionAccept {
		t.Fatalf("segment decisions = %s, %s", report.Segments[0].ProjectedDecision, report.Segments[1].ProjectedDecision)
	}
	if err := VerifyCertificate(report.Certificate, keys); err != nil {
		t.Fatalf("VerifyCertificate: %v", err)
	}
	if _, err := VerifyCertificateExhaustive(report.Certificate, keys, 24); err != nil {
		t.Fatalf("VerifyCertificateExhaustive: %v", err)
	}
}

func TestCrossContextBatchIsolation(t *testing.T) {
	signers, keys := signerSet(t, "old", "new")
	partitionA := NewPartition("ctx-a", "sender")
	partitionB := NewPartition("ctx-b", "sender")
	logA := mustLog(t, "old", "ctx-a", signers["old"])
	proofA := mustProof(t, logA, partitionA, Interval{Lo: 0, Hi: 8}, signers["old"])
	h := mustHandoff(t, HandoffConfig{
		Nodes:          []NodeID{"old"},
		NewNode:        "new",
		Leases:         []Lease{{Node: "old", Closed: true, Barrier: 0}},
		CandidatePages: []ProofPage{{ID: "ctx-a-page", Cost: 1, Proofs: []RangeProof{proofA}}},
		Keys:           keys,
	})
	report, err := h.AnalyzeBatch([]QuerySegment{
		{Partition: partitionA, Range: Interval{Lo: 0, Hi: 8}},
		{Partition: partitionB, Range: Interval{Lo: 0, Hi: 8}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Feasible {
		t.Fatalf("cross-context proof was treated as reusable: supplement=%+v parts=%+v", report.Supplement, report.Segments)
	}
	if report.Segments[0].ProjectedDecision != DecisionWait {
		t.Fatalf("ctx-a = %s", report.Segments[0].ProjectedDecision)
	}
	if report.Segments[1].ProjectedDecision != DecisionWait {
		t.Fatalf("ctx-b = %s", report.Segments[1].ProjectedDecision)
	}
}

func TestRandomSmallDomainAgainstEnumeration(t *testing.T) {
	const domain = 16
	for trial := 0; trial < 20; trial++ {
		trial := trial
		t.Run(fmt.Sprintf("trial-%02d", trial), func(t *testing.T) {
			signers, keys := signerSet(t, "old-a", "old-b", "new")
			partition := NewPartition("ctx", "sender")
			rng := rand.New(rand.NewSource(int64(1000 + trial)))
			logA := mustLog(t, "old-a", "ctx", signers["old-a"])
			logB := mustLog(t, "old-b", "ctx", signers["old-b"])
			for _, log := range []*NodeLog{logA, logB} {
				for seq := Seq(0); seq < domain; seq++ {
					if rng.Intn(5) == 0 {
						commitRange(t, log, partition, Interval{Lo: seq, Hi: seq + 1}, signers[log.Node()])
					}
				}
			}
			proofA0 := mustProof(t, logA, partition, Interval{Lo: 0, Hi: 6}, signers["old-a"])
			proofA10 := mustProof(t, logA, partition, Interval{Lo: 10, Hi: 16}, signers["old-a"])
			proofB0 := mustProof(t, logB, partition, Interval{Lo: 0, Hi: 10}, signers["old-b"])
			proofA6 := mustProof(t, logA, partition, Interval{Lo: 6, Hi: 10}, signers["old-a"])
			proofB10 := mustProof(t, logB, partition, Interval{Lo: 10, Hi: 16}, signers["old-b"])
			proofA6to16 := mustProof(t, logA, partition, Interval{Lo: 6, Hi: 16}, signers["old-a"])
			proofB6to16 := mustProof(t, logB, partition, Interval{Lo: 6, Hi: 16}, signers["old-b"])
			h := mustHandoff(t, HandoffConfig{
				Nodes:       []NodeID{"old-a", "old-b"},
				NewNode:     "new",
				Leases:      []Lease{{Node: "old-a", Closed: true, Barrier: 0}, {Node: "old-b", Closed: true, Barrier: 0}},
				KnownProofs: []RangeProof{proofA0, proofA10, proofB0},
				CandidatePages: []ProofPage{
					{ID: "page-a", Cost: 2, Proofs: []RangeProof{proofA6}},
					{ID: "page-b", Cost: 3, Proofs: []RangeProof{proofB10}},
					{ID: "page-ab", Cost: 4, Proofs: []RangeProof{proofB6to16, proofA6to16}},
				},
				Keys: keys,
			})
			queries := []QuerySegment{{Partition: partition, Range: Interval{Lo: 0, Hi: domain}}}
			report, err := h.AnalyzeBatch(queries)
			if err != nil {
				t.Fatal(err)
			}
			if !report.FullyDetermined {
				t.Fatalf("not fully determined: selected=%+v cost=%d feasible=%v", report.Certificate.SelectedPages, report.SupplementCost, report.Feasible)
			}
			if err := VerifyCertificate(report.Certificate, keys); err != nil {
				t.Fatalf("VerifyCertificate: %v", err)
			}
			if _, err := VerifyCertificateExhaustive(report.Certificate, keys, domain); err != nil {
				t.Fatalf("VerifyCertificateExhaustive: %v", err)
			}
			selectedProofs := selectedProofsForReport(t, report)
			finalConfig := HandoffConfig{
				Nodes:       []NodeID{"old-a", "old-b"},
				NewNode:     "new",
				Leases:      []Lease{{Node: "old-a", Closed: true, Barrier: 0}, {Node: "old-b", Closed: true, Barrier: 0}},
				KnownProofs: append([]RangeProof{proofA0, proofA10, proofB0}, selectedProofs...),
				Keys:        keys,
			}
			final, err := NewHandoff(finalConfig)
			if err != nil {
				t.Fatal(err)
			}
			for seq := Seq(0); seq < domain; seq++ {
				part, err := report.DecisionAt(0, seq)
				if err != nil {
					t.Fatal(err)
				}
				verdict, err := final.EvaluateSequence(partition, seq)
				if err != nil {
					t.Fatal(err)
				}
				if part.Decision != verdict.Decision {
					t.Fatalf("seq %d part=%s verdict=%s", seq, part.Decision, verdict.Decision)
				}
			}
		})
	}
}

func TestIntervalSetAgainstEnumeration(t *testing.T) {
	const domain = 32
	rng := rand.New(rand.NewSource(77))
	randomSet := func() IntervalSet {
		intervals := make([]Interval, 0)
		for i := 0; i < 8; i++ {
			lo := Seq(rng.Intn(domain + 1))
			hi := Seq(rng.Intn(domain + 1))
			if lo > hi {
				lo, hi = hi, lo
			}
			intervals = append(intervals, Interval{Lo: lo, Hi: hi})
		}
		return MustIntervalSet(intervals...)
	}
	contains := func(set IntervalSet, seq int) bool {
		return set.Contains(Seq(seq))
	}
	for trial := 0; trial < 200; trial++ {
		a, b := randomSet(), randomSet()
		union := a.Union(b)
		inter := a.Intersect(b)
		diff := a.Subtract(b)
		comp := a.Complement()
		for seq := 0; seq < domain; seq++ {
			if contains(union, seq) != (contains(a, seq) || contains(b, seq)) {
				t.Fatalf("union mismatch at %d: a=%s b=%s union=%s", seq, a, b, union)
			}
			if contains(inter, seq) != (contains(a, seq) && contains(b, seq)) {
				t.Fatalf("intersection mismatch at %d: a=%s b=%s inter=%s", seq, a, b, inter)
			}
			if contains(diff, seq) != (contains(a, seq) && !contains(b, seq)) {
				t.Fatalf("difference mismatch at %d: a=%s b=%s diff=%s", seq, a, b, diff)
			}
			if contains(comp, seq) != !contains(a, seq) {
				t.Fatalf("complement mismatch at %d: a=%s comp=%s", seq, a, comp)
			}
		}
	}
}

func selectedProofsForReport(t *testing.T, report *BatchReport) []RangeProof {
	t.Helper()
	if report.Certificate == nil {
		return nil
	}
	selected := make(map[string]struct{}, len(report.Certificate.SelectedPages))
	for _, id := range report.Certificate.SelectedPages {
		selected[id] = struct{}{}
	}
	out := make([]RangeProof, 0)
	for _, page := range report.Certificate.Input.CandidatePages {
		if _, ok := selected[page.ID]; ok {
			out = append(out, page.Proofs...)
		}
	}
	return out
}
