package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// DoubleSignEvidence is the minimal independently verifiable proof that a
// member signed two different blocks at the same height and phase.
//
// The evidence contains only the two signed shares. A verifier locates the
// member's pinned public key in the fixed member list and checks both
// signatures. No private key or live ledger state is needed.
type DoubleSignEvidence struct {
	ShareA Share `json:"share_a"`
	ShareB Share `json:"share_b"`
}

// Evidence is a short alias for DoubleSignEvidence.
type Evidence = DoubleSignEvidence

// ErrNotDoubleSign reports that a candidate evidence pair is not a
// same-member, same-chain, same-height, same-phase, different-block pair.
var ErrNotDoubleSign = errors.New("netsec: shares do not constitute double signing")

// VerifyEvidence verifies e against a fixed member list.
func VerifyEvidence(members []Member, e DoubleSignEvidence) error {
	keys := make(map[string]Member, len(members))
	for _, m := range members {
		if m.ID == "" {
			return errors.New("netsec: empty member ID in member list")
		}
		if _, exists := keys[m.ID]; exists {
			return fmt.Errorf("netsec: duplicate member ID %q", m.ID)
		}
		if len(m.PublicKey) != 32 {
			return fmt.Errorf("netsec: member %q has invalid public key length", m.ID)
		}
		keys[m.ID] = m
	}
	return verifyEvidenceWithKeys(keys, e)
}

func verifyEvidenceWithKeys(keys map[string]Member, e DoubleSignEvidence) error {
	a, b := e.ShareA, e.ShareB
	if err := a.Validate(); err != nil {
		return fmt.Errorf("netsec: evidence share A: %w", err)
	}
	if err := b.Validate(); err != nil {
		return fmt.Errorf("netsec: evidence share B: %w", err)
	}
	if a.MemberID != b.MemberID {
		return fmt.Errorf("%w: different members", ErrNotDoubleSign)
	}
	if a.ChainID != b.ChainID {
		return fmt.Errorf("%w: different chains", ErrNotDoubleSign)
	}
	if a.Height != b.Height {
		return fmt.Errorf("%w: different heights", ErrNotDoubleSign)
	}
	if a.Phase != b.Phase {
		return fmt.Errorf("%w: different phases", ErrNotDoubleSign)
	}
	if a.BlockDigest == b.BlockDigest {
		return fmt.Errorf("%w: same block digest", ErrNotDoubleSign)
	}
	if a.EventID() == b.EventID() {
		return fmt.Errorf("%w: identical share", ErrNotDoubleSign)
	}

	member, ok := keys[a.MemberID]
	if !ok {
		return fmt.Errorf("netsec: unknown member %q", a.MemberID)
	}
	if err := a.Verify(member.PublicKey); err != nil {
		return fmt.Errorf("netsec: evidence share A: %w", err)
	}
	if err := b.Verify(member.PublicKey); err != nil {
		return fmt.Errorf("netsec: evidence share B: %w", err)
	}
	return nil
}

// Verify verifies e against the member list that owns this ledger.
func (e DoubleSignEvidence) Verify(members []Member) error {
	return VerifyEvidence(members, e)
}

// MemberID returns the member accused by the evidence.
func (e DoubleSignEvidence) MemberID() string { return e.ShareA.MemberID }

// ChainID returns the chain on which the conflict occurred.
func (e DoubleSignEvidence) ChainID() string { return e.ShareA.ChainID }

// Height returns the conflicting height.
func (e DoubleSignEvidence) Height() uint64 { return e.ShareA.Height }

// Phase returns the conflicting phase.
func (e DoubleSignEvidence) Phase() Phase { return e.ShareA.Phase }

// ID returns a stable identity for the evidence pair. It is independent of
// the order in which the two shares were observed.
func (e DoubleSignEvidence) ID() string {
	a, b := e.ShareA.EventID(), e.ShareB.EventID()
	if b < a {
		a, b = b, a
	}
	h := sha256.New()
	h.Write([]byte("example.org/netsec/q0057/evidence/v1"))
	h.Write([]byte{0})
	h.Write([]byte(a))
	h.Write([]byte{0})
	h.Write([]byte(b))
	return hex.EncodeToString(h.Sum(nil))
}

// MarshalJSON is the default serialization. It exists to document that only
// the two shares are serialized.
func (e DoubleSignEvidence) MarshalJSON() ([]byte, error) {
	type wire struct {
		ShareA Share `json:"share_a"`
		ShareB Share `json:"share_b"`
	}
	return json.Marshal(wire{ShareA: e.ShareA, ShareB: e.ShareB})
}

func orderEvidence(a, b Share) DoubleSignEvidence {
	if b.EventID() < a.EventID() {
		a, b = b, a
	}
	return DoubleSignEvidence{ShareA: a, ShareB: b}
}

// SortEvidence orders shares deterministically when callers build batches.
func SortEvidence(shares []Share) []Share {
	out := append([]Share(nil), shares...)
	sort.Slice(out, func(i, j int) bool { return out[i].EventID() < out[j].EventID() })
	return out
}
