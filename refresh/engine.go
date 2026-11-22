package refresh

import (
	"bytes"
	"errors"
	"fmt"
)

// Reason codes used in machine-readable triage / gate reports.
const (
	ReasonBadSignature     = "bad_signature"
	ReasonIdentityUnknown  = "identity_unknown"
	ReasonBadCommitment    = "bad_commitment"
	ReasonNoCommitment     = "no_commitment"
	ReasonBadDelta         = "delta_mismatch"
	ReasonConflictingDelta = "conflicting_delta"
	ReasonEquivocation     = "equivocation"
	ReasonComplaint        = "valid_complaint"
	ReasonForeignRound     = "foreign_round"
	ReasonDuplicate        = "duplicate"
	ReasonMissingMaterial  = "missing_material"
)

// Pair identifies a directed edge in canonical, alias-free form.
type Pair struct {
	Sender    string
	Recipient string
}

// MissingItem names a precise (sender, recipient) pair and the material absent
// for it. Missing material is always reported down to the directed pair.
type MissingItem struct {
	Sender    string
	Recipient string
	What      string
}

// ExcludedRecord documents evidence that could not participate in this round
// (duplicate replay, foreign round, unparseable entry).
type ExcludedRecord struct {
	Sender    string
	Recipient string
	Kind      string
	Reason    string
	Round     uint64
}

// MergedEvidence is the alias-resolved, de-duplicated view of one or more
// bundles for a round.
type MergedEvidence struct {
	Round       uint64
	Commitments map[string]Commitment
	Increments  map[Pair][]RecordedIncrement
	Complaints  map[Pair][]RecordedComplaint
	Referenced  map[string]struct{}
	Excluded    []ExcludedRecord
}

// Engine bundles the caller-injected primitives. All protocol state lives in
// the injected Store; the Engine itself is stateless.
type Engine struct {
	Field *Field
	Group Group
	Auth  Authenticator
}

// NewEngine validates that all primitives are present.
func NewEngine(f *Field, g Group, a Authenticator) (*Engine, error) {
	if f == nil || g == nil || a == nil {
		return nil, errors.New("refresh: field, group and authenticator are required")
	}
	return &Engine{Field: f, Group: g, Auth: a}, nil
}

func (e *Engine) resolve(id string) (string, PublicKey, error) {
	canon, key, err := e.Auth.Canonical(id)
	if err != nil {
		return "", nil, err
	}
	return canon, key, nil
}

func pairKey(s, r string) Pair { return Pair{Sender: s, Recipient: r} }

func (e *Engine) validSignature(canonicalID string, msg, sig []byte) bool {
	if len(sig) == 0 {
		return false
	}
	return e.Auth.Verify(canonicalID, msg, sig)
}

// MergeEvidence combines public bundles for a round. Aliases are collapsed
// first; duplicate replays and foreign-round entries stay in the audit trail
// but are never applied twice.
func (e *Engine) MergeEvidence(round uint64, bundles ...EvidenceBundle) (*MergedEvidence, error) {
	merged := &MergedEvidence{
		Round:       round,
		Commitments: map[string]Commitment{},
		Increments:  map[Pair][]RecordedIncrement{},
		Complaints:  map[Pair][]RecordedComplaint{},
		Referenced:  map[string]struct{}{},
	}
	for _, b := range bundles {
		if b.Round != round {
			merged.Excluded = append(merged.Excluded, ExcludedRecord{
				Kind: "bundle", Reason: ReasonForeignRound, Round: b.Round,
			})
			continue
		}
		if err := e.mergeCommitments(round, b, merged); err != nil {
			return nil, err
		}
		for _, inc := range b.Increments {
			rec, ex := e.canonicalIncrement(round, inc)
			if ex != nil {
				merged.Excluded = append(merged.Excluded, *ex)
				continue
			}
			key := pairKey(rec.SenderID, rec.RecipientID)
			if containsIncrement(merged.Increments[key], rec) {
				merged.Excluded = append(merged.Excluded, ExcludedRecord{
					Kind: "increment", Sender: rec.SenderID, Recipient: rec.RecipientID,
					Reason: ReasonDuplicate, Round: round,
				})
				continue
			}
			merged.Increments[key] = append(merged.Increments[key], rec)
		}
		for _, c := range b.Complaints {
			rec, ex := e.canonicalComplaint(round, c)
			if ex != nil {
				merged.Excluded = append(merged.Excluded, *ex)
				continue
			}
			key := pairKey(rec.SenderID, rec.RecipientID)
			if containsComplaint(merged.Complaints[key], rec) {
				merged.Excluded = append(merged.Excluded, ExcludedRecord{
					Kind: "complaint", Sender: rec.SenderID, Recipient: rec.RecipientID,
					Reason: ReasonDuplicate, Round: round,
				})
				continue
			}
			merged.Complaints[key] = append(merged.Complaints[key], rec)
		}
	}
	return merged, nil
}

func (e *Engine) mergeCommitments(round uint64, b EvidenceBundle, merged *MergedEvidence) error {
	for _, enc := range b.Commitments {
		if enc.Round != round {
			merged.Excluded = append(merged.Excluded, ExcludedRecord{
				Kind: "commitment", Sender: enc.SenderID, Reason: ReasonForeignRound, Round: enc.Round,
			})
			continue
		}
		sender, _, err := e.resolve(enc.SenderID)
		if err != nil {
			merged.Excluded = append(merged.Excluded, ExcludedRecord{
				Kind: "commitment", Sender: enc.SenderID, Reason: ReasonIdentityUnknown, Round: enc.Round,
			})
			continue
		}
		terms := make([]Point, len(enc.Terms))
		bad := false
		for i, raw := range enc.Terms {
			p, perr := e.Group.ParsePoint(raw)
			if perr != nil {
				bad = true
				break
			}
			terms[i] = p
		}
		if bad {
			merged.Excluded = append(merged.Excluded, ExcludedRecord{
				Kind: "commitment", Sender: sender, Reason: ReasonBadCommitment, Round: round,
			})
			continue
		}
		c := Commitment{Round: round, SenderID: sender, Terms: terms}
		if existing, ok := merged.Commitments[sender]; ok {
			if !commitmentsEqual(existing, c) {
				return fmt.Errorf("refresh: conflicting commitments for sender %s", sender)
			}
			merged.Excluded = append(merged.Excluded, ExcludedRecord{
				Kind: "commitment", Sender: sender, Reason: ReasonDuplicate, Round: round,
			})
			continue
		}
		merged.Commitments[sender] = c
		merged.Referenced[sender] = struct{}{}
	}
	return nil
}

func (e *Engine) canonicalIncrement(round uint64, inc RecordedIncrement) (RecordedIncrement, *ExcludedRecord) {
	if inc.Round != round {
		return inc, &ExcludedRecord{
			Kind: "increment", Sender: inc.SenderID, Recipient: inc.RecipientID,
			Reason: ReasonForeignRound, Round: inc.Round,
		}
	}
	sender, _, err := e.resolve(inc.SenderID)
	if err != nil {
		return inc, &ExcludedRecord{
			Kind: "increment", Sender: inc.SenderID, Recipient: inc.RecipientID,
			Reason: ReasonIdentityUnknown, Round: round,
		}
	}
	recipient, _, err := e.resolve(inc.RecipientID)
	if err != nil {
		return inc, &ExcludedRecord{
			Kind: "increment", Sender: sender, Recipient: inc.RecipientID,
			Reason: ReasonIdentityUnknown, Round: round,
		}
	}
	inc.SenderID, inc.RecipientID = sender, recipient
	return inc, nil
}

func (e *Engine) canonicalComplaint(round uint64, c RecordedComplaint) (RecordedComplaint, *ExcludedRecord) {
	if c.Round != round {
		return c, &ExcludedRecord{
			Kind: "complaint", Sender: c.SenderID, Recipient: c.RecipientID,
			Reason: ReasonForeignRound, Round: c.Round,
		}
	}
	sender, _, err := e.resolve(c.SenderID)
	if err != nil {
		return c, &ExcludedRecord{
			Kind: "complaint", Sender: c.SenderID, Recipient: c.RecipientID,
			Reason: ReasonIdentityUnknown, Round: round,
		}
	}
	recipient, _, err := e.resolve(c.RecipientID)
	if err != nil {
		return c, &ExcludedRecord{
			Kind: "complaint", Sender: sender, Recipient: c.RecipientID,
			Reason: ReasonIdentityUnknown, Round: round,
		}
	}
	c.SenderID, c.RecipientID = sender, recipient
	return c, nil
}

func containsIncrement(list []RecordedIncrement, target RecordedIncrement) bool {
	for _, x := range list {
		if incrementsEqual(x, target) {
			return true
		}
	}
	return false
}

func incrementsEqual(a, b RecordedIncrement) bool {
	return a.Round == b.Round && a.SenderID == b.SenderID && a.RecipientID == b.RecipientID &&
		bytes.Equal(a.TranscriptDigest, b.TranscriptDigest) &&
		bytes.Equal(a.Signature, b.Signature) &&
		(a.Delta == nil) == (b.Delta == nil) &&
		(a.Delta == nil || a.Delta.Cmp(b.Delta) == 0)
}

func containsComplaint(list []RecordedComplaint, target RecordedComplaint) bool {
	for _, x := range list {
		if complaintsEqual(x, target) {
			return true
		}
	}
	return false
}

func complaintsEqual(a, b RecordedComplaint) bool {
	return a.Round == b.Round && a.SenderID == b.SenderID && a.RecipientID == b.RecipientID &&
		a.Reason == b.Reason && bytes.Equal(a.TranscriptDigest, b.TranscriptDigest) &&
		bytes.Equal(a.Signature, b.Signature)
}

func commitmentsEqual(a, b Commitment) bool {
	if a.SenderID != b.SenderID || len(a.Terms) != len(b.Terms) {
		return false
	}
	for i := range a.Terms {
		if !a.Terms[i].Equals(b.Terms[i]) {
			return false
		}
	}
	return true
}
