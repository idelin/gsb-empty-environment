package netsec

import "testing"

func testValidator(t *testing.T) *Validator {
	t.Helper()
	poset, err := NewPoset(
		[]string{"public", "internal", "confidential", "quarantine"},
		[]PosetEdge{
			{Lower: "public", Upper: "internal"},
			{Lower: "internal", Upper: "confidential"},
		},
	)
	if err != nil {
		t.Fatalf("NewPoset: %v", err)
	}
	validator, err := NewValidator(
		poset,
		[]EvidenceSpec{
			{ID: "ticket", Capacity: 2},
			{ID: "approval", Capacity: 1},
		},
		[]Transition{
			{Name: "review-c2i", Subject: "auditor", From: "confidential", To: "internal", Requires: []string{"ticket", "approval"}},
			{Name: "review-c2i-batch", Subject: "auditor", From: "confidential", To: "internal", Requires: []string{"ticket"}},
			{Name: "review-i2p", Subject: "auditor", From: "internal", To: "public", Requires: []string{"ticket"}},
		},
	)
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return validator
}

func TestVerifyAllowsPromotionAndReusesEvidenceViaBranches(t *testing.T) {
	v := testValidator(t)
	events := []Event{
		{Subject: "app", Object: "obj-1", From: "internal", To: "confidential"},     // 升级
		{Subject: "auditor", Object: "obj-1", From: "confidential", To: "internal"}, // 首次降级：两条转换边产生两个分支
		{Subject: "auditor", Object: "obj-2", From: "confidential", To: "internal"}, // 仅省票分支仍可走通
	}
	violation, proof := v.Verify(events)
	if violation != nil {
		t.Fatalf("expected valid trace, got violation: %v", violation)
	}
	if len(proof.Steps) != len(events) {
		t.Fatalf("proof has %d steps, want %d", len(proof.Steps), len(events))
	}
	if proof.Steps[0].Transition != "" {
		t.Errorf("promotion must not use a transition, got %q", proof.Steps[0].Transition)
	}
	used := map[string]int{}
	for _, step := range proof.Steps {
		for _, ev := range step.Consumed {
			used[ev]++
		}
	}
	if used["ticket"] != 2 || used["approval"] > 1 {
		t.Fatalf("unexpected consumption: %v (proof=%+v)", used, proof.Steps)
	}
	if used["approval"] != 1 {
		t.Fatalf("full-review branch must consume the single approval, got %v", used)
	}
	if proof.EvidenceLeft[0] != 0 || proof.EvidenceLeft[1] != 0 {
		t.Fatalf("evidence left = %v, want [0 0]", proof.EvidenceLeft)
	}
}

func TestVerifyFlagsFirstUndocumentedDowngradeWhenEvidenceExhausted(t *testing.T) {
	v := testValidator(t)
	events := []Event{
		{Subject: "auditor", Object: "obj-1", From: "confidential", To: "internal"},
		{Subject: "auditor", Object: "obj-2", From: "confidential", To: "internal"},
		{Subject: "auditor", Object: "obj-3", From: "confidential", To: "internal"}, // ticket 耗尽
	}
	violation, proof := v.Verify(events)
	if proof != nil {
		t.Fatal("expected no proof for exhausted trace")
	}
	if violation == nil || violation.EventIndex != 2 {
		t.Fatalf("expected violation at index 2, got %+v", violation)
	}
	if violation.Reason != ReasonEvidenceExhausted {
		t.Fatalf("reason = %q, want %q", violation.Reason, ReasonEvidenceExhausted)
	}
	foundTicket := false
	for _, m := range violation.MissingConditions {
		if m.Reason == ReasonEvidenceExhausted && m.Evidence == "ticket" {
			foundTicket = true
		}
	}
	if !foundTicket {
		t.Fatalf("missing conditions do not mention exhausted ticket: %+v", violation.MissingConditions)
	}
	if violation.Error() == "" {
		t.Fatal("violation must render as error")
	}
}

func TestVerifySubjectDenial(t *testing.T) {
	v := testValidator(t)
	events := []Event{
		{Subject: "intruder", Object: "obj-1", From: "confidential", To: "internal"},
	}
	violation, _ := v.Verify(events)
	if violation == nil || violation.Reason != ReasonSubjectDenied {
		t.Fatalf("expected subject denial, got %+v", violation)
	}
	for _, m := range violation.MissingConditions {
		if m.Transition == "" || m.Subject != "intruder" {
			t.Fatalf("missing condition must name transition and subject: %+v", m)
		}
	}
}

func TestVerifyNoTransition(t *testing.T) {
	v := testValidator(t)
	violation, _ := v.Verify([]Event{
		{Subject: "auditor", Object: "obj-1", From: "confidential", To: "public"},
	})
	if violation == nil || violation.Reason != ReasonNoTransition {
		t.Fatalf("expected no_transition, got %+v", violation)
	}
}

func TestVerifyIncomparableFlowAndUnknownLabel(t *testing.T) {
	v := testValidator(t)
	violation, _ := v.Verify([]Event{
		{Subject: "app", Object: "obj-1", From: "quarantine", To: "internal"},
	})
	if violation == nil || violation.Reason != ReasonIncomparableFlow {
		t.Fatalf("expected incomparable flow, got %+v", violation)
	}

	violation, _ = v.Verify([]Event{
		{Subject: "app", Object: "obj-1", From: "confidential", To: "top-secret"},
	})
	if violation == nil || violation.Reason != ReasonUnknownLabel {
		t.Fatalf("expected unknown label, got %+v", violation)
	}
}

func TestVerifyReturnsFirstViolationOnly(t *testing.T) {
	v := testValidator(t)
	events := []Event{
		{Subject: "auditor", Object: "obj-1", From: "confidential", To: "internal"},
		{Subject: "auditor", Object: "obj-2", From: "confidential", To: "internal"},
		{Subject: "auditor", Object: "obj-3", From: "confidential", To: "internal"}, // 第三个先违规
		{Subject: "intruder", Object: "obj-4", From: "confidential", To: "internal"},
	}
	violation, _ := v.Verify(events)
	if violation == nil || violation.EventIndex != 2 {
		t.Fatalf("expected first violation at index 2, got %+v", violation)
	}
}

func TestNewValidatorRejectsNonDowngradeTransitionAndBadEvidence(t *testing.T) {
	poset, err := NewPoset([]string{"internal", "confidential"}, []PosetEdge{{Lower: "internal", Upper: "confidential"}})
	if err != nil {
		t.Fatalf("NewPoset: %v", err)
	}
	if _, err := NewValidator(poset, []EvidenceSpec{{ID: "ticket", Capacity: 1}}, []Transition{
		{Name: "up", Subject: "auditor", From: "internal", To: "confidential", Requires: []string{"ticket"}},
	}); err == nil {
		t.Fatal("expected non-downgrade transition error")
	}
	if _, err := NewValidator(poset, []EvidenceSpec{{ID: "ticket", Capacity: 0}}, nil); err == nil {
		t.Fatal("expected non-positive capacity error")
	}
	if _, err := NewValidator(poset, []EvidenceSpec{{ID: "ticket", Capacity: 1}}, []Transition{
		{Name: "down", Subject: "auditor", From: "confidential", To: "internal", Requires: []string{"missing"}},
	}); err == nil {
		t.Fatal("expected unknown evidence reference error")
	}
}
