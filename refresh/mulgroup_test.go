package refresh_test

import (
	"math/big"
	"testing"

	"prescreen.local/ddd-9449-20261122-v2/refresh"
)

// mulGroupHarness builds the second transparent reference: scalar field F_53
// and the order-53 subgroup <4> of F_107^*. Both are small enough to inspect by
// hand (107 = 2*53+1, 4^53 mod 107 = 1 and 4 has no smaller exponent there).
func mulGroupHarness(t *testing.T) *harness {
	t.Helper()
	fieldPrime := big.NewInt(53)
	groupPrime := big.NewInt(107)
	order := big.NewInt(53)
	base := big.NewInt(4)

	field, err := refresh.NewField(fieldPrime)
	if err != nil {
		t.Fatalf("field: %v", err)
	}
	group := NewMultiplicativeGroup(groupPrime, base, order)
	if !group.ScalarBaseMul(order).Equals(group.Identity()) {
		t.Fatal("reference subgroup order check failed")
	}
	senders := []string{"s1", "s2", "s3"}
	recipients := []string{"r1", "r2", "r3"}
	auth := newMemAuth(append(append([]string{}, senders...), recipients...), nil)
	return newHarness(t, group, field, auth, senders, recipients, 2, 1)
}

func TestMultiplicativeGroupReference(t *testing.T) {
	h := mulGroupHarness(t)
	res, err := h.engine.VerifyPublicEvidence(refresh.PublicGateConfig{
		Round:            h.round,
		Threshold:        h.threshold,
		OldMaster:        h.oldMasterCommitment(big.NewInt(9)),
		ExpectedConstant: h.expectedConstant(big.NewInt(9)),
		SenderIDs:        h.senders,
		RecipientIDs:     h.recipients,
		Views:            h.healthyViews(),
	}, h.healthyBundle(t))
	if err != nil {
		t.Fatalf("multiplicative verify: %v", err)
	}
	if !res.Decision.Allowed {
		t.Fatalf("multiplicative round blocked: %v", res.Decision.Reasons)
	}
}

func TestMultiplicativeGroupRejectsTamperedIncrement(t *testing.T) {
	h := mulGroupHarness(t)
	bundle := h.healthyBundle(t)
	for i := range bundle.Increments {
		if bundle.Increments[i].SenderID == "s1" && bundle.Increments[i].RecipientID == "r1" {
			bundle.Increments[i].Delta = h.field.Add(bundle.Increments[i].Delta, big.NewInt(1))
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
	v := report.Verdicts[refresh.Pair{Sender: "s1", Recipient: "r1"}]
	if v.Status != refresh.VerdictRejected || v.Reason != refresh.ReasonBadDelta {
		t.Fatalf("multiplicative tamper verdict = %s/%s", v.Status, v.Reason)
	}
}
