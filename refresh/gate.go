package refresh

import (
	"bytes"
	"errors"
	"sort"
)

// Gate blocking reason codes.
const (
	GateSenderSetMismatch   = "sender_set_mismatch"
	GateDigestMismatch      = "transcript_digest_mismatch"
	GateConstantMismatch    = "constant_term_mismatch"
	GateBelowThreshold      = "below_threshold"
	GatePendingEvidence     = "pending_evidence"
	GateRejectedSender      = "rejected_sender"
	GateInvalidCommitment   = "invalid_commitment"
	GateEmptyParticipantSet = "empty_participant_set"
)

// RecipientView is what one recipient references when asking to publish.
type RecipientView struct {
	RecipientID     string
	SenderSet       []string
	Digest          []byte
	AcceptedSenders []string
}

// PublishDecision is the atomic, all-or-nothing gate result. When Allowed is
// false the reasons explain every blocking condition; the structure is safe to
// serialize into audit logs because it holds no scalars.
type PublishDecision struct {
	Allowed          bool
	Round            uint64
	Threshold        int
	EffectiveSenders []string
	AcceptedPairs    []Pair
	BlockedPairs     []Pair
	Reasons          []string
	Digest           []byte
}

// PublishGate evaluates whether a new round may be atomically published.
//
// Conditions, all required together:
//   - aliases resolved before any check;
//   - every recipient references the same sender set and transcript digest;
//   - every counted pair is ACCEPTED (a PENDING pair blocks rather than
//     silently dropping the recipient, and a valid complaint can never be
//     ignored to make the sets line up);
//   - at least threshold recipients complete the full effective sender set;
//   - summed new commitment has the same constant term as the old master
//     commitment (both identities here, since refresh constants are zero).
func (e *Engine) PublishGate(round uint64, threshold int, oldMaster []Point, expectedConstant Point,
	report *TriageReport, views []RecipientView) (*PublishDecision, error) {
	if report == nil || report.Round != round {
		return nil, errors.New("refresh: triage report does not match round")
	}
	if threshold <= 0 {
		return nil, errors.New("refresh: threshold must be positive")
	}
	if len(views) == 0 {
		return nil, errors.New("refresh: no recipient views supplied")
	}

	decision := &PublishDecision{Round: round, Threshold: threshold}

	disqualified := sortedKeys(report.Disqualified)
	effective, reasons := e.effectiveSenderSet(views, disqualified)
	decision.Reasons = append(decision.Reasons, reasons...)
	decision.EffectiveSenders = effective
	if len(effective) == 0 {
		decision.Reasons = append(decision.Reasons, GateEmptyParticipantSet)
	}

	digest, digestReasons := e.uniformDigest(views)
	decision.Reasons = append(decision.Reasons, digestReasons...)
	decision.Digest = digest

	complete := 0
	for _, view := range views {
		recipient, _, err := e.resolve(view.RecipientID)
		if err != nil {
			decision.Reasons = append(decision.Reasons, GateInvalidCommitment)
			continue
		}
		ok := true
		for _, sender := range effective {
			v := report.Verdicts[pairKey(sender, recipient)]
			switch v.Status {
			case VerdictAccepted:
				decision.AcceptedPairs = append(decision.AcceptedPairs, pairKey(sender, recipient))
			case VerdictRejected:
				ok = false
				decision.BlockedPairs = append(decision.BlockedPairs, pairKey(sender, recipient))
				decision.Reasons = append(decision.Reasons, GateRejectedSender)
			default:
				ok = false
				decision.BlockedPairs = append(decision.BlockedPairs, pairKey(sender, recipient))
				decision.Reasons = append(decision.Reasons, GatePendingEvidence)
			}
		}
		// A globally disqualified sender blocks any recipient that still
		// references it: the gate never drops a valid complaint or proven fault
		// merely to make the referenced sender sets line up. Senders already
		// removed from every view are simply absent from the effective set.
		referenced := map[string]struct{}{}
		for _, s := range view.SenderSet {
			canon, _, err := e.resolve(s)
			if err != nil {
				continue
			}
			referenced[canon] = struct{}{}
		}
		for _, bad := range disqualified {
			if _, stillReferenced := referenced[bad]; !stillReferenced {
				continue
			}
			decision.BlockedPairs = append(decision.BlockedPairs, pairKey(bad, recipient))
			decision.Reasons = append(decision.Reasons, GateRejectedSender)
			ok = false
		}
		if ok && len(effective) > 0 {
			complete++
		}
	}
	if complete < threshold {
		decision.Reasons = append(decision.Reasons, GateBelowThreshold)
	}

	if len(effective) > 0 {
		if reason := e.checkCommitments(effective, oldMaster, expectedConstant, report); reason != "" {
			decision.Reasons = append(decision.Reasons, reason)
		}
	}

	decision.Reasons = dedupe(decision.Reasons)
	decision.Allowed = len(decision.Reasons) == 0 && complete >= threshold
	sort.Slice(decision.AcceptedPairs, func(i, j int) bool {
		return pairLess(decision.AcceptedPairs[i], decision.AcceptedPairs[j])
	})
	sort.Slice(decision.BlockedPairs, func(i, j int) bool {
		return pairLess(decision.BlockedPairs[i], decision.BlockedPairs[j])
	})
	return decision, nil
}

func (e *Engine) effectiveSenderSet(views []RecipientView, disqualified []string) ([]string, []string) {
	var reasons []string
	banned := map[string]struct{}{}
	for _, s := range disqualified {
		banned[s] = struct{}{}
	}
	var baseline []string
	uniform := true
	for i, view := range views {
		set, err := e.canonList(view.SenderSet)
		if err != nil {
			return nil, []string{GateInvalidCommitment}
		}
		sort.Strings(set)
		if i == 0 {
			baseline = set
			continue
		}
		if !equalStringSets(baseline, set) {
			uniform = false
		}
	}
	if !uniform {
		reasons = append(reasons, GateSenderSetMismatch)
	}
	effective := make([]string, 0, len(baseline))
	for _, s := range baseline {
		if _, bad := banned[s]; bad {
			continue
		}
		effective = append(effective, s)
	}
	// A disqualified sender only leaves the effective set when every recipient
	// explicitly removes it. If the views still carry it, publishing would
	// silently rewrite the referenced sender set, which is forbidden.
	if uniform && len(banned) > 0 {
		for _, s := range baseline {
			if _, bad := banned[s]; bad {
				reasons = append(reasons, GateSenderSetMismatch)
				break
			}
		}
	}
	return effective, reasons
}

func (e *Engine) uniformDigest(views []RecipientView) ([]byte, []string) {
	var digest []byte
	var reasons []string
	for _, view := range views {
		if digest == nil {
			digest = append([]byte(nil), view.Digest...)
			continue
		}
		if !bytes.Equal(digest, view.Digest) {
			reasons = append(reasons, GateDigestMismatch)
		}
	}
	return digest, dedupe(reasons)
}

func (e *Engine) checkCommitments(effective []string, oldMaster []Point,
	expectedConstant Point, report *TriageReport) string {
	rounds := make([]Commitment, 0, len(effective))
	for _, sender := range effective {
		c, ok := report.commitmentFor(sender)
		if !ok {
			return GateInvalidCommitment
		}
		if !ZeroConstant(e.Group, c) {
			return GateInvalidCommitment
		}
		rounds = append(rounds, c)
	}
	summed, err := SumCommitments(e.Group, rounds)
	if err != nil {
		return GateInvalidCommitment
	}
	// Refresh polynomials have zero constant term, so the summed refresh
	// commitment's constant term must be identity; the new master constant
	// term (old master constant + zero) must still equal the persisted
	// expected constant term.
	if len(oldMaster) == 0 || len(summed) == 0 || expectedConstant == nil {
		return GateConstantMismatch
	}
	if !summed[0].Equals(e.Group.Identity()) {
		return GateConstantMismatch
	}
	if !e.Group.Add(oldMaster[0], summed[0]).Equals(expectedConstant) {
		return GateConstantMismatch
	}
	return ""
}

// commitmentFor looks up a sender's public commitment via the round evidence.
// The report itself intentionally stores no points, so the merged evidence is
// reached through an engine-level cache populated at triage time.
func (r *TriageReport) commitmentFor(sender string) (Commitment, bool) {
	c, ok := r.commitments[sender]
	return c, ok
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedupe(in []string) []string {
	seen := map[string]struct{}{}
	out := in[:0]
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func pairLess(a, b Pair) bool {
	if a.Sender != b.Sender {
		return a.Sender < b.Sender
	}
	return a.Recipient < b.Recipient
}
