package refresh

import (
	"bytes"
	"errors"
)

// Triage verdicts. Every directed (sender, recipient) pair ends in exactly one.
const (
	// VerdictAccepted: the pair is proven good — the signed, transcript-bound
	// delivery evaluates correctly under the sender's coefficient commitment.
	VerdictAccepted = "ACCEPTED"
	// VerdictRejected: the pair is proven bad — a signature or algebraic check
	// failed, equivocation is on record, or a valid complaint is sustained.
	VerdictRejected = "REJECTED"
	// VerdictPending: neither side is proven. Material is missing; the pair
	// cannot be counted yet.
	VerdictPending = "PENDING"
)

// PairVerdict is the triage outcome for one directed pair.
type PairVerdict struct {
	Pair   Pair
	Status string
	Reason string
	// Digest is the transcript digest referenced by the pair's material, if any.
	Digest []byte
}

// TriageReport carries the three-way classification plus the gap list. It
// contains no custodian scalars: increments are referenced by pair only.
type TriageReport struct {
	Round        uint64
	Verdicts     map[Pair]PairVerdict
	Missing      []MissingItem
	Disqualified map[string]struct{}
	// commitments is engine-private evidence used by the publish gate's
	// constant-term check; the audit projection strips it.
	commitments map[string]Commitment
}

func (e *Engine) canonList(ids []string) ([]string, error) {
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		canon, _, err := e.resolve(id)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[canon]; ok {
			continue
		}
		seen[canon] = struct{}{}
		out = append(out, canon)
	}
	return out, nil
}

// Triage classifies every directed pair among expectedSenders and
// expectedRecipients into accepted / rejected / pending, then marks senders
// with any proven misbehavior globally disqualified. Aliases are resolved
// before any proof or threshold is examined.
func (e *Engine) Triage(round uint64, merged *MergedEvidence, expectedSenders, expectedRecipients []string) (*TriageReport, error) {
	if merged == nil || merged.Round != round {
		return nil, errors.New("refresh: triage input does not match round")
	}
	senders, err := e.canonList(expectedSenders)
	if err != nil {
		return nil, err
	}
	recipients, err := e.canonList(expectedRecipients)
	if err != nil {
		return nil, err
	}
	report := &TriageReport{
		Round:        round,
		Verdicts:     map[Pair]PairVerdict{},
		Disqualified: map[string]struct{}{},
		commitments:  merged.Commitments,
	}

	for _, sender := range senders {
		commitment, haveCommitment := merged.Commitments[sender]
		if haveCommitment && !ZeroConstant(e.Group, commitment) {
			report.Disqualified[sender] = struct{}{}
		}
		for _, recipient := range recipients {
			key := pairKey(sender, recipient)
			verdict := e.triagePair(round, commitment, haveCommitment, merged, key)
			report.Verdicts[key] = verdict
			if verdict.Status == VerdictRejected {
				report.Disqualified[sender] = struct{}{}
			}
		}
	}

	e.markEquivocation(senders, recipients, merged, report)
	e.collectMissing(senders, recipients, merged, report)
	return report, nil
}

func (e *Engine) triagePair(round uint64, commitment Commitment, haveCommitment bool,
	merged *MergedEvidence, key Pair) PairVerdict {
	deliveries := merged.Increments[key]
	complaints := merged.Complaints[key]

	signedOK := make([]RecordedIncrement, 0, len(deliveries))
	signedBad := false
	for _, inc := range deliveries {
		if e.validSignature(inc.SenderID, IncrementSigningBytes(inc), inc.Signature) {
			signedOK = append(signedOK, inc)
		} else {
			signedBad = true
		}
	}

	// A signed delivery whose algebra fails is a proven fault: signature alone
	// never makes an increment correct.
	if haveCommitment {
		for _, inc := range signedOK {
			ok, err := e.incrementMatches(commitment, inc, key.Recipient)
			if err == nil && !ok {
				return PairVerdict{Pair: key, Status: VerdictRejected, Reason: ReasonBadDelta,
					Digest: inc.TranscriptDigest}
			}
		}
		// Two distinct signed increments for one pair cannot both be right.
		if distinctDeltas(signedOK) {
			d := firstDigest(signedOK)
			return PairVerdict{Pair: key, Status: VerdictRejected, Reason: ReasonConflictingDelta, Digest: d}
		}
		if len(signedOK) > 0 {
			inc := signedOK[0]
			if validComplaintAgainstDelivery(e, complaints, inc) {
				return PairVerdict{Pair: key, Status: VerdictRejected, Reason: ReasonComplaint,
					Digest: inc.TranscriptDigest}
			}
			return PairVerdict{Pair: key, Status: VerdictAccepted, Reason: "",
				Digest: inc.TranscriptDigest}
		}
	}

	// No usable delivery. A valid complaint proves the sender failed this pair;
	// an invalid one proves nothing and the pair stays pending.
	if validComplaint(e, complaints) {
		return PairVerdict{Pair: key, Status: VerdictRejected, Reason: ReasonComplaint,
			Digest: firstComplaintDigest(complaints)}
	}

	reason := ReasonMissingMaterial
	if signedBad {
		reason = ReasonBadSignature
	}
	if len(deliveries) > 0 && !haveCommitment {
		reason = ReasonNoCommitment
	}
	return PairVerdict{Pair: key, Status: VerdictPending, Reason: reason,
		Digest: firstDigest(deliveries)}
}

// incrementMatches checks g*delta == Eval(C, x) at the recipient's coordinate.
func (e *Engine) incrementMatches(c Commitment, inc RecordedIncrement, recipientID string) (bool, error) {
	if inc.Delta == nil {
		return false, errors.New("refresh: increment carries no scalar")
	}
	x, err := RecipientCoordinate(e.Field, recipientID)
	if err != nil {
		return false, err
	}
	want, err := EvalCommitment(e.Group, c, x)
	if err != nil {
		return false, err
	}
	got := e.Group.ScalarBaseMul(e.Field.Mod(inc.Delta))
	return got.Equals(want), nil
}

// markEquivocation disqualifies a sender who delivered different transcript
// digests to different recipients, even when every individual pair verifies.
func (e *Engine) markEquivocation(senders, recipients []string, merged *MergedEvidence, report *TriageReport) {
	for _, sender := range senders {
		digests := map[string]struct{}{}
		for _, recipient := range recipients {
			for _, inc := range merged.Increments[pairKey(sender, recipient)] {
				if e.validSignature(inc.SenderID, IncrementSigningBytes(inc), inc.Signature) {
					digests[string(inc.TranscriptDigest)] = struct{}{}
				}
			}
		}
		if len(digests) <= 1 {
			continue
		}
		report.Disqualified[sender] = struct{}{}
		for _, recipient := range recipients {
			key := pairKey(sender, recipient)
			v := report.Verdicts[key]
			v.Status, v.Reason = VerdictRejected, ReasonEquivocation
			report.Verdicts[key] = v
		}
	}
}

func (e *Engine) collectMissing(senders, recipients []string, merged *MergedEvidence, report *TriageReport) {
	for _, sender := range senders {
		_, haveCommitment := merged.Commitments[sender]
		for _, recipient := range recipients {
			key := pairKey(sender, recipient)
			if report.Verdicts[key].Status != VerdictPending {
				continue
			}
			if !haveCommitment {
				report.Missing = append(report.Missing, MissingItem{
					Sender: sender, Recipient: recipient, What: "commitment",
				})
			}
			if len(merged.Increments[key]) == 0 {
				report.Missing = append(report.Missing, MissingItem{
					Sender: sender, Recipient: recipient, What: "increment",
				})
			}
		}
	}
}

func validComplaint(e *Engine, complaints []RecordedComplaint) bool {
	for _, c := range complaints {
		if e.validSignature(c.RecipientID, ComplaintSigningBytes(c), c.Signature) {
			return true
		}
	}
	return false
}

// validComplaintAgainstDelivery honors a complaint only when it binds the same
// transcript as the delivered increment. A complaint for a different transcript
// cannot invalidate this delivery; the global digest-consistency gate handles
// divergence between recipients.
func validComplaintAgainstDelivery(e *Engine, complaints []RecordedComplaint, inc RecordedIncrement) bool {
	for _, c := range complaints {
		if !e.validSignature(c.RecipientID, ComplaintSigningBytes(c), c.Signature) {
			continue
		}
		if bytes.Equal(c.TranscriptDigest, inc.TranscriptDigest) {
			return true
		}
	}
	return false
}

func distinctDeltas(list []RecordedIncrement) bool {
	for i := 1; i < len(list); i++ {
		a, b := list[i-1].Delta, list[i].Delta
		if a == nil || b == nil || a.Cmp(b) != 0 {
			return true
		}
	}
	return false
}

func firstDigest(list []RecordedIncrement) []byte {
	if len(list) == 0 {
		return nil
	}
	return list[0].TranscriptDigest
}

func firstComplaintDigest(list []RecordedComplaint) []byte {
	for _, c := range list {
		return c.TranscriptDigest
	}
	return nil
}
