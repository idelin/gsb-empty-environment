package refresh_test

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"prescreen.local/ddd-9449-20261122-v2/refresh"
)

func TestBelowThresholdBlocks(t *testing.T) {
	h := standardHarness(t)
	bundle := h.healthyBundle(t)

	filtered := bundle.Increments[:0]
	for _, inc := range bundle.Increments {
		if inc.RecipientID == "r3" || inc.RecipientID == "r4" || inc.RecipientID == "r5" {
			continue
		}
		filtered = append(filtered, inc)
	}
	bundle.Increments = filtered

	res, err := h.engine.VerifyPublicEvidence(refresh.PublicGateConfig{
		Round: h.round, Threshold: h.threshold,
		OldMaster:        h.oldMasterCommitment(big.NewInt(42)),
		ExpectedConstant: h.expectedConstant(big.NewInt(42)),
		SenderIDs:        h.senders, RecipientIDs: h.recipients,
		Views: h.healthyViews(),
	}, bundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Decision.Allowed {
		t.Fatal("below-threshold round must not publish")
	}
	if !containsReason(res.Decision.Reasons, refresh.GateBelowThreshold) ||
		!containsReason(res.Decision.Reasons, refresh.GatePendingEvidence) {
		t.Fatalf("reasons = %v", res.Decision.Reasons)
	}
}

func TestConstantTermMismatchBlocks(t *testing.T) {
	h := standardHarness(t)
	bundle := h.healthyBundle(t)

	// All refresh commitments legitimately have zero constant term, but the
	// host presents an old master commitment whose constant encodes a different
	// secret. The gate must detect that the new master would not share the old
	// constant term once the zero-constant refresh is applied.
	wrongOldMaster := h.oldMasterCommitment(big.NewInt(42))
	wrongOldMaster[0] = h.group.Add(wrongOldMaster[0], h.group.Generator())

	res, err := h.engine.VerifyPublicEvidence(refresh.PublicGateConfig{
		Round: h.round, Threshold: h.threshold,
		OldMaster:        wrongOldMaster,
		ExpectedConstant: h.expectedConstant(big.NewInt(42)),
		SenderIDs:        h.senders, RecipientIDs: h.recipients,
		Views: h.healthyViews(),
	}, bundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res.Decision.Allowed ||
		!containsReason(res.Decision.Reasons, refresh.GateConstantMismatch) {
		t.Fatalf("constant mismatch not blocked: %v", res.Decision.Reasons)
	}
}

func TestAliasesCollapseBeforeVerification(t *testing.T) {
	p := testPrime()
	field, _ := refresh.NewField(p)
	senders := []string{"s1", "s2", "s3"}
	recipients := []string{"r1", "r2", "r3"}
	auth := newMemAuth(append(append([]string{}, senders...), recipients...),
		map[string]string{
			"custodian-1": "s1",
			"node-a":      "r1",
		})
	h := newHarness(t, NewAdditiveGroup(p), field, auth, senders, recipients, 2, 1)

	bundle := h.healthyBundle(t)
	for i := range bundle.Increments {
		if bundle.Increments[i].SenderID == "s1" && bundle.Increments[i].RecipientID == "r1" {
			bundle.Increments[i].SenderID = "custodian-1"
			bundle.Increments[i].RecipientID = "node-a"
		}
	}
	views := h.healthyViews()
	views[0].RecipientID = "node-a"

	res, err := h.engine.VerifyPublicEvidence(refresh.PublicGateConfig{
		Round: h.round, Threshold: h.threshold,
		OldMaster:        h.oldMasterCommitment(big.NewInt(7)),
		ExpectedConstant: h.expectedConstant(big.NewInt(7)),
		SenderIDs:        []string{"custodian-1", "s2", "s3"},
		RecipientIDs:     []string{"node-a", "r2", "r3"},
		Views:            views,
	}, bundle)
	if err != nil {
		t.Fatalf("verify with aliases: %v", err)
	}
	if !res.Decision.Allowed {
		t.Fatalf("alias round blocked: %v", res.Decision.Reasons)
	}
	for key := range res.Triage.Verdicts {
		if strings.HasPrefix(key.Sender, "custodian") || strings.HasPrefix(key.Recipient, "node-") {
			t.Fatalf("alias leaked into canonical verdict: %+v", key)
		}
	}
}

func TestAuditReportContainsNoScalars(t *testing.T) {
	h := standardHarness(t)
	bundle := h.healthyBundle(t)
	filtered := bundle.Increments[:0]
	for _, inc := range bundle.Increments {
		if inc.SenderID == "s1" && inc.RecipientID == "r1" {
			continue
		}
		filtered = append(filtered, inc)
	}
	bundle.Increments = filtered

	res, err := h.engine.VerifyPublicEvidence(refresh.PublicGateConfig{
		Round: h.round, Threshold: h.threshold,
		OldMaster:        h.oldMasterCommitment(big.NewInt(42)),
		ExpectedConstant: h.expectedConstant(big.NewInt(42)),
		SenderIDs:        h.senders, RecipientIDs: h.recipients,
		Views: h.healthyViews(),
	}, bundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	raw, err := res.Audit.JSON()
	if err != nil {
		t.Fatalf("audit json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal audit: %v", err)
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if k == "scalar" || k == "delta" {
					t.Fatalf("audit JSON contains scalar-bearing key %q", k)
				}
				walk(val)
			}
		case []any:
			for _, val := range x {
				walk(val)
			}
		}
	}
	walk(decoded)
	for _, inc := range filtered {
		if inc.Delta == nil {
			continue
		}
		leak := []byte(`"scalar":` + inc.Delta.String())
		if bytes.Contains(raw, leak) {
			t.Fatalf("audit blob leaks tagged delta %s", inc.Delta.String())
		}
	}
	if bytes.Contains(raw, []byte(`"scalar"`)) {
		t.Fatal("audit report contains a scalar-named field")
	}
}
