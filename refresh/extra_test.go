package refresh_test

import (
	"math/big"
	"reflect"
	"testing"

	"prescreen.local/ddd-9449-20261122-v2/refresh"
)

func TestForeignRoundComplaintIsExcluded(t *testing.T) {
	h := standardHarness(t)
	bundle := h.healthyBundle(t)

	c := h.complaint(t, "s1", "r1", refresh.ReasonMissingDelivery, h.digest)
	c.Round = h.round - 1
	c.Signature = nil
	if err := refresh.SignComplaint(h.auth, &c); err != nil {
		t.Fatalf("resign: %v", err)
	}
	bundle.Complaints = append(bundle.Complaints, c)

	merged, err := h.engine.MergeEvidence(h.round, bundle)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	key := refresh.Pair{Sender: "s1", Recipient: "r1"}
	if len(merged.Complaints[key]) != 0 {
		t.Fatal("foreign-round complaint must not bind the current round")
	}
	found := false
	for _, ex := range merged.Excluded {
		if ex.Kind == "complaint" && ex.Reason == refresh.ReasonForeignRound {
			found = true
		}
	}
	if !found {
		t.Fatal("foreign-round complaint not retained in audit trail")
	}

	// The current round still publishes: carried-over complaints never count.
	res, err := h.engine.VerifyPublicEvidence(refresh.PublicGateConfig{
		Round:            h.round,
		Threshold:        h.threshold,
		OldMaster:        h.oldMasterCommitment(big.NewInt(42)),
		ExpectedConstant: h.expectedConstant(big.NewInt(42)),
		SenderIDs:        h.senders,
		RecipientIDs:     h.recipients,
		Views:            h.healthyViews(),
	}, bundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.Decision.Allowed {
		t.Fatalf("round blocked by carried-over complaint: %v", res.Decision.Reasons)
	}
}

func TestAuditTypesHaveNoScalarFields(t *testing.T) {
	auditTypes := []reflect.Type{
		reflect.TypeOf(refresh.AuditReport{}),
		reflect.TypeOf(refresh.AuditPair{}),
		reflect.TypeOf(refresh.AuditMissing{}),
		reflect.TypeOf(refresh.AuditExcluded{}),
	}
	bigIntType := reflect.TypeOf(big.Int{})
	var check func(t reflect.Type, seen map[reflect.Type]bool)
	check = func(typ reflect.Type, seen map[reflect.Type]bool) {
		switch typ.Kind() {
		case reflect.Ptr, reflect.Slice, reflect.Array:
			check(typ.Elem(), seen)
			return
		case reflect.Struct:
			if seen[typ] {
				return
			}
			seen[typ] = true
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				if f.Type == bigIntType {
					t.Fatalf("audit type %s leaks scalar field %s", typ.Name(), f.Name)
				}
				check(f.Type, seen)
			}
		}
	}
	for _, typ := range auditTypes {
		check(typ, map[reflect.Type]bool{})
	}
}

func TestDisqualifiedSenderIsRemovedFromEffectiveSet(t *testing.T) {
	h := standardHarness(t)
	bundle := h.healthyBundle(t)

	// s5 hands r1 an algebraically wrong increment: s5 is proven faulty.
	for i := range bundle.Increments {
		if bundle.Increments[i].SenderID == "s5" && bundle.Increments[i].RecipientID == "r1" {
			bundle.Increments[i].Delta = h.field.Add(bundle.Increments[i].Delta, big.NewInt(3))
			if err := refresh.SignIncrement(h.auth, &bundle.Increments[i]); err != nil {
				t.Fatalf("resign: %v", err)
			}
		}
	}
	merged, err := h.engine.MergeEvidence(h.round, bundle)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	report, err := h.engine.Triage(h.round, merged, h.senders, h.recipients)
	if err != nil {
		t.Fatalf("triage: %v", err)
	}
	if _, bad := report.Disqualified["s5"]; !bad {
		t.Fatal("s5 not disqualified")
	}

	// All recipients still reference s5 in their sender set: the gate must
	// block rather than silently ignore the proven fault to align the sets.
	decision, err := h.engine.PublishGate(h.round, h.threshold,
		h.oldMasterCommitment(big.NewInt(42)),
		h.expectedConstant(big.NewInt(42)), report, h.healthyViews())
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if decision.Allowed {
		t.Fatal("gate published while recipients still reference a disqualified sender")
	}

	// Once every recipient drops s5 from their referenced sender set, the same
	// evidence publishes atomically over the remaining senders.
	views := h.healthyViews()
	for i := range views {
		kept := views[i].SenderSet[:0]
		for _, s := range views[i].SenderSet {
			if s != "s5" {
				kept = append(kept, s)
			}
		}
		views[i].SenderSet = kept
	}
	decision2, err := h.engine.PublishGate(h.round, h.threshold,
		h.oldMasterCommitment(big.NewInt(42)),
		h.expectedConstant(big.NewInt(42)), report, views)
	if err != nil {
		t.Fatalf("gate after removal: %v", err)
	}
	if !decision2.Allowed {
		t.Fatalf("round over honest senders blocked: %v", decision2.Reasons)
	}
	for _, s := range decision2.EffectiveSenders {
		if s == "s5" {
			t.Fatal("disqualified sender remains in effective set")
		}
	}
}

func TestMergeBundlesAccumulatesMaterial(t *testing.T) {
	h := standardHarness(t)
	full := h.healthyBundle(t)

	// Split the healthy evidence across two independently submitted bundles.
	var first, second refresh.EvidenceBundle
	first.Round, second.Round = h.round, h.round
	first.Commitments = full.Commitments[:2]
	second.Commitments = full.Commitments[2:]
	for i, inc := range full.Increments {
		if i%2 == 0 {
			first.Increments = append(first.Increments, inc)
		} else {
			second.Increments = append(second.Increments, inc)
		}
	}
	merged, err := h.engine.MergeEvidence(h.round, first, second)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(merged.Commitments) != len(h.senders) {
		t.Fatalf("merged commitments = %d", len(merged.Commitments))
	}
	if len(merged.Increments) != len(h.senders)*len(h.recipients) {
		t.Fatalf("merged pair count = %d", len(merged.Increments))
	}
	report, err := h.engine.Triage(h.round, merged, h.senders, h.recipients)
	if err != nil {
		t.Fatalf("triage: %v", err)
	}
	for _, v := range report.Verdicts {
		if v.Status != refresh.VerdictAccepted {
			t.Fatalf("split-bundle pair %v->%v = %s", v.Pair.Sender, v.Pair.Recipient, v.Status)
		}
	}
}
