package refresh_test

import (
	"bytes"
	"errors"
	"math/big"
	"testing"

	"prescreen.local/ddd-9449-20261122-v2/refresh"
)

var errUnknownID = errors.New("unknown participant id")

func testPrime() *big.Int { return big.NewInt(101) }

func standardHarness(t *testing.T) *harness {
	t.Helper()
	p := testPrime()
	field, err := refresh.NewField(p)
	if err != nil {
		t.Fatalf("field: %v", err)
	}
	senders := []string{"s1", "s2", "s3", "s4", "s5"}
	recipients := []string{"r1", "r2", "r3", "r4", "r5"}
	auth := newMemAuth(append(append([]string{}, senders...), recipients...), nil)
	group := NewAdditiveGroup(p)
	return newHarness(t, group, field, auth, senders, recipients, 3, 1)
}

func verifyHealthy(t *testing.T, h *harness) *refresh.PublicVerification {
	t.Helper()
	secret := big.NewInt(42)
	res, err := h.engine.VerifyPublicEvidence(refresh.PublicGateConfig{
		Round: h.round, Threshold: h.threshold,
		OldMaster:        h.oldMasterCommitment(secret),
		ExpectedConstant: h.expectedConstant(secret),
		SenderIDs:        h.senders, RecipientIDs: h.recipients,
		Views: h.healthyViews(),
	}, h.healthyBundle(t))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !res.Decision.Allowed {
		t.Fatalf("healthy round blocked: %v", res.Decision.Reasons)
	}
	return res
}

func containsReason(list []string, want string) bool {
	for _, r := range list {
		if r == want {
			return true
		}
	}
	return false
}

func TestHealthyRoundPublishes(t *testing.T) {
	h := standardHarness(t)
	res := verifyHealthy(t, h)
	if len(res.Triage.Verdicts) != len(h.senders)*len(h.recipients) {
		t.Fatalf("verdict count = %d", len(res.Triage.Verdicts))
	}
	for _, v := range res.Triage.Verdicts {
		if v.Status != refresh.VerdictAccepted {
			t.Fatalf("pair %v->%v not accepted: %s/%s",
				v.Pair.Sender, v.Pair.Recipient, v.Status, v.Reason)
		}
	}
}

func TestComplaintReplayIsDeduplicated(t *testing.T) {
	h := standardHarness(t)
	bundle := h.healthyBundle(t)
	c := h.complaint(t, "s1", "r1", refresh.ReasonMissingDelivery, h.digest)
	bundle.Complaints = append(bundle.Complaints, c, c)

	merged, err := h.engine.MergeEvidence(h.round, bundle)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	key := refresh.Pair{Sender: "s1", Recipient: "r1"}
	if got := len(merged.Complaints[key]); got != 1 {
		t.Fatalf("replayed complaint kept %d copies", got)
	}
	var replays int
	for _, ex := range merged.Excluded {
		if ex.Kind == "complaint" && ex.Reason == refresh.ReasonDuplicate {
			replays++
		}
	}
	if replays != 1 {
		t.Fatalf("expected one duplicate exclusion, got %d", replays)
	}
}

func TestForeignRoundEvidenceIsExcluded(t *testing.T) {
	h := standardHarness(t)
	bundle := h.healthyBundle(t)
	old := h.increment(t, "s1", "r1", h.digest)
	old.Round = h.round - 1
	old.Signature = nil
	if err := refresh.SignIncrement(h.auth, &old); err != nil {
		t.Fatalf("sign: %v", err)
	}
	bundle.Increments = append(bundle.Increments, old)

	merged, err := h.engine.MergeEvidence(h.round, bundle)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	key := refresh.Pair{Sender: "s1", Recipient: "r1"}
	if len(merged.Increments[key]) != 1 {
		t.Fatal("foreign-round increment must not enter the round evidence")
	}
	found := false
	for _, ex := range merged.Excluded {
		if ex.Reason == refresh.ReasonForeignRound {
			found = true
		}
	}
	if !found {
		t.Fatal("foreign-round evidence not audited as excluded")
	}
}

func TestEquivocationDifferentTranscripts(t *testing.T) {
	h := standardHarness(t)

	altCommitment := h.commitment(t, "s1")
	altDigest, err := refresh.TranscriptDigest(h.round, []refresh.Commitment{altCommitment})
	if err != nil {
		t.Fatalf("alt digest: %v", err)
	}

	bundle := h.healthyBundle(t)
	for i := range bundle.Increments {
		inc := bundle.Increments[i]
		if inc.SenderID != "s1" || inc.RecipientID != "r2" {
			continue
		}
		inc.TranscriptDigest = altDigest
		inc.Signature = nil
		if err := refresh.SignIncrement(h.auth, &inc); err != nil {
			t.Fatalf("resign: %v", err)
		}
		bundle.Increments[i] = inc
	}

	merged, err := h.engine.MergeEvidence(h.round, bundle)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	report, err := h.engine.Triage(h.round, merged, h.senders, h.recipients)
	if err != nil {
		t.Fatalf("triage: %v", err)
	}
	if _, bad := report.Disqualified["s1"]; !bad {
		t.Fatal("equivocating sender not disqualified")
	}
	for _, recipient := range h.recipients {
		v := report.Verdicts[refresh.Pair{Sender: "s1", Recipient: recipient}]
		if v.Status != refresh.VerdictRejected || v.Reason != refresh.ReasonEquivocation {
			t.Fatalf("pair s1->%s = %s/%s", recipient, v.Status, v.Reason)
		}
	}
}

func TestValidSignatureButWrongDeltaIsRejected(t *testing.T) {
	h := standardHarness(t)
	bundle := h.healthyBundle(t)
	for i := range bundle.Increments {
		if bundle.Increments[i].SenderID == "s2" && bundle.Increments[i].RecipientID == "r3" {
			bundle.Increments[i].Delta = h.field.Add(bundle.Increments[i].Delta, big.NewInt(7))
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
	v := report.Verdicts[refresh.Pair{Sender: "s2", Recipient: "r3"}]
	if v.Status != refresh.VerdictRejected || v.Reason != refresh.ReasonBadDelta {
		t.Fatalf("bad delta verdict = %s/%s", v.Status, v.Reason)
	}
	if _, bad := report.Disqualified["s2"]; !bad {
		t.Fatal("sender with algebraically bad increment not disqualified")
	}
}

func TestMissingMaterialListedPerPair(t *testing.T) {
	h := standardHarness(t)
	bundle := h.healthyBundle(t)

	filtered := bundle.Increments[:0]
	for _, inc := range bundle.Increments {
		if inc.SenderID == "s3" && inc.RecipientID == "r4" {
			continue
		}
		filtered = append(filtered, inc)
	}
	bundle.Increments = filtered

	merged, err := h.engine.MergeEvidence(h.round, bundle)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	report, err := h.engine.Triage(h.round, merged, h.senders, h.recipients)
	if err != nil {
		t.Fatalf("triage: %v", err)
	}
	v := report.Verdicts[refresh.Pair{Sender: "s3", Recipient: "r4"}]
	if v.Status != refresh.VerdictPending {
		t.Fatalf("missing delivery verdict = %s", v.Status)
	}
	hit := false
	for _, m := range report.Missing {
		if m.Sender == "s3" && m.Recipient == "r4" && m.What == "increment" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("missing item not reported per pair: %+v", report.Missing)
	}
}

func TestValidComplaintCannotBeIgnored(t *testing.T) {
	h := standardHarness(t)
	bundle := h.healthyBundle(t)

	filtered := bundle.Increments[:0]
	for _, inc := range bundle.Increments {
		if inc.SenderID == "s4" && inc.RecipientID == "r2" {
			continue
		}
		filtered = append(filtered, inc)
	}
	bundle.Increments = filtered
	bundle.Complaints = append(bundle.Complaints,
		h.complaint(t, "s4", "r2", refresh.ReasonMissingDelivery, h.digest))

	merged, err := h.engine.MergeEvidence(h.round, bundle)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	report, err := h.engine.Triage(h.round, merged, h.senders, h.recipients)
	if err != nil {
		t.Fatalf("triage: %v", err)
	}
	v := report.Verdicts[refresh.Pair{Sender: "s4", Recipient: "r2"}]
	if v.Status != refresh.VerdictRejected || v.Reason != refresh.ReasonComplaint {
		t.Fatalf("complaint verdict = %s/%s", v.Status, v.Reason)
	}

	decision, err := h.engine.PublishGate(h.round, h.threshold,
		h.oldMasterCommitment(big.NewInt(42)),
		h.expectedConstant(big.NewInt(42)), report, h.healthyViews())
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if decision.Allowed {
		t.Fatal("gate must not publish while a valid complaint is unresolved")
	}
}

func TestDigestMismatchBlocks(t *testing.T) {
	h := standardHarness(t)
	verifyHealthy(t, h)

	views := h.healthyViews()
	views[0].Digest = bytes.Repeat([]byte{9}, len(h.digest))
	merged, err := h.engine.MergeEvidence(h.round, h.healthyBundle(t))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	report, err := h.engine.Triage(h.round, merged, h.senders, h.recipients)
	if err != nil {
		t.Fatalf("triage: %v", err)
	}
	decision, err := h.engine.PublishGate(h.round, h.threshold,
		h.oldMasterCommitment(big.NewInt(42)),
		h.expectedConstant(big.NewInt(42)), report, views)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if decision.Allowed || !containsReason(decision.Reasons, refresh.GateDigestMismatch) {
		t.Fatalf("digest mismatch not blocked: allowed=%v reasons=%v",
			decision.Allowed, decision.Reasons)
	}
}
