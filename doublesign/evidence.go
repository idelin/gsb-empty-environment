package doublesign

import (
	"crypto/ed25519"
	"errors"
)

// Violation labels the safety rule a member was convicted under.
type Violation string

const (
	ViolationEquivocation     Violation = "equivocation"
	ViolationCommitNoPrepare  Violation = "commit_without_prepare"
	ViolationLockedQCRollback Violation = "locked_qc_rollback"
	ViolationParentMismatch   Violation = "parent_link_mismatch"
	ViolationViewQCForged     Violation = "view_qc_forged"
)

// Evidence is the minimal, self-contained conviction artifact. It embeds the
// authorized epoch set (with its signed root anchor) and the exact signed
// shares proving the violation. No private key, store or host state is needed
// to verify it: call VerifyEvidence with the authority public key.
type Evidence struct {
	ChainID   string          `json:"chainID"`
	Violation Violation       `json:"violation"`
	MemberID  string          `json:"memberID"`
	EpochSet  EpochSet        `json:"epochSet"`
	Anchor    AuthorityAnchor `json:"anchor"`
	Messages  []Message       `json:"messages"`
}

// VerifyEvidence performs a fully offline, standalone verification of a
// conviction. It returns nil only when:
//   - the embedded epoch set is internally valid and root-anchor signed by the
//     authority public key,
//   - the accused member was a member of that epoch,
//   - every embedded share is a valid Ed25519 signature under the member key,
//   - the shares actually satisfy the labeled violation predicate.
func VerifyEvidence(ev Evidence, authorityPub ed25519.PublicKey) error {
	if ev.ChainID != ev.EpochSet.ChainID {
		return errors.New("evidence chain id mismatch")
	}
	if err := ev.EpochSet.Validate(); err != nil {
		return err
	}
	if err := ev.Anchor.Verify(ev.EpochSet, authorityPub); err != nil {
		return err
	}
	member, ok := ev.EpochSet.member(ev.MemberID)
	if !ok {
		return errors.New("accused member not present in anchored epoch")
	}
	if len(ev.Messages) == 0 {
		return errors.New("evidence contains no messages")
	}
	for _, m := range ev.Messages {
		if err := m.WellFormed(ev.ChainID); err != nil {
			return err
		}
		if m.MemberID != ev.MemberID {
			return errors.New("evidence message signed by different member")
		}
		if m.Epoch != ev.EpochSet.Epoch {
			return errors.New("evidence message epoch differs from anchored epoch")
		}
		if !Verify(member.PubKey, ev.ChainID, m) {
			return errors.New("invalid member signature in evidence")
		}
	}
	return verifyPredicate(ev)
}

func verifyPredicate(ev Evidence) error {
	msgs := ev.Messages
	switch ev.Violation {
	case ViolationEquivocation:
		if len(msgs) != 2 {
			return errors.New("equivocation needs exactly two shares")
		}
		a, b := msgs[0], msgs[1]
		if a.Phase != b.Phase || a.Epoch != b.Epoch || a.Height != b.Height ||
			a.Round != b.Round || bytesEqual(a.BlockDigest, b.BlockDigest) {
			return errors.New("shares do not prove same-round equivocation")
		}
		return nil

	case ViolationCommitNoPrepare:
		if len(msgs) < 1 || msgs[0].Phase != PhaseCommit {
			return errors.New("commit_without_prepare needs a commit share first")
		}
		commit := msgs[0]
		// Any valid prepare for the same height/block with round<=commit.round
		// would exonerate the member; its absence is the offense.
		for _, m := range msgs[1:] {
			if m.Phase == PhasePrepare && m.Height == commit.Height &&
				bytesEqual(m.BlockDigest, commit.BlockDigest) &&
				m.Round <= commit.Round {
				return errors.New("a qualifying prepare is present: no offense")
			}
		}
		return nil

	case ViolationLockedQCRollback:
		if len(msgs) != 2 {
			return errors.New("locked_qc_rollback needs two shares")
		}
		high, low := msgs[0], msgs[1]
		if high.Epoch != low.Epoch || high.Height != low.Height {
			return errors.New("rollback shares must share epoch and height")
		}
		if high.Phase != PhasePrepare && high.Phase != PhaseViewChange {
			return errors.New("first share must be prepare or view_change")
		}
		if low.Phase != PhasePrepare && low.Phase != PhaseViewChange {
			return errors.New("second share must be prepare or view_change")
		}
		if !(high.LockedRound > low.LockedRound && high.Round < low.Round &&
			!bytesEqual(high.BlockDigest, low.BlockDigest)) {
			return errors.New("shares do not prove a locked-qc rollback")
		}
		return nil

	case ViolationParentMismatch:
		if len(msgs) != 2 {
			return errors.New("parent_link_mismatch needs commit and prepare")
		}
		commit, prepare := msgs[0], msgs[1]
		if commit.Phase != PhaseCommit || prepare.Phase != PhasePrepare {
			return errors.New("expected commit then prepare shares")
		}
		if prepare.Height != commit.Height+1 {
			return errors.New("shares do not prove parent mismatch")
		}
		if bytesEqual(prepare.ParentDigest, commit.BlockDigest) {
			return errors.New("prepare correctly links to commit: no offense")
		}
		return nil

	case ViolationViewQCForged:
		if len(msgs) < 2 {
			return errors.New("view_qc_forged needs view_change and prepare shares")
		}
		view := msgs[0]
		if view.Phase != PhaseViewChange {
			return errors.New("first share must be view_change")
		}
		// The claimed prepare QC must match one of the member's own prepares.
		for _, m := range msgs[1:] {
			if m.Phase == PhasePrepare && m.Height == view.QCHeight &&
				m.Round == view.QCRound &&
				bytesEqual(m.BlockDigest, view.BlockDigest) {
				return errors.New("claimed qc is backed by a prepare: no offense")
			}
		}
		return nil

	default:
		return errors.New("unknown violation type")
	}
}
