package dsledger

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
)

// ConflictKind 标注双签冲突的类别。
type ConflictKind string

const (
	// ConflictSameHeight 同高度同阶段支持了两个不同区块。
	ConflictSameHeight ConflictKind = "same_height_block_conflict"
	// ConflictRound 同高度轮次冲突（轮次倒退导致前提不再成立）。
	ConflictRound ConflictKind = "round_conflict"
	// ConflictParentFork 跨高度父摘要分叉（父项指向已 commit 链之外的分叉）。
	ConflictParentFork ConflictKind = "parent_digest_fork"
)

// Evidence 是可脱离账本独立验证的最小双签证据：
// 同一成员的两份有效签名份额，外加冲突类别。
// 只需固定成员公钥即可验证，无需私钥或账本内部状态。
type Evidence struct {
	Kind   ConflictKind `json:"kind"`
	Signer MemberID     `json:"signer"`
	First  Share        `json:"first"`
	Second Share        `json:"second"`
}

// VerifyEvidence 独立验证双签证据。
//
// 校验内容：
//  1. 两份份额签名均对成员公钥有效，且签名者一致、链 ID 一致；
//  2. 依据冲突类别检查两份份额的结构确实构成该类双签。
//
// 任何字段被篡改、签名不属同一清单成员，或结构不构成冲突，均判为无效。
func VerifyEvidence(roster *Roster, ev Evidence) error {
	if roster == nil {
		return fmt.Errorf("dsledger: nil roster")
	}
	switch ev.Kind {
	case ConflictSameHeight, ConflictRound, ConflictParentFork:
	default:
		return fmt.Errorf("dsledger: unknown conflict kind %q", ev.Kind)
	}
	member, ok := roster.Member(ev.Signer)
	if !ok {
		return fmt.Errorf("dsledger: evidence signer %q not in roster", ev.Signer)
	}
	if err := verifyEvidenceShare(member, roster.ChainID(), ev.First, ev.Signer); err != nil {
		return fmt.Errorf("dsledger: evidence first share: %w", err)
	}
	if err := verifyEvidenceShare(member, roster.ChainID(), ev.Second, ev.Signer); err != nil {
		return fmt.Errorf("dsledger: evidence second share: %w", err)
	}
	if bytes.Equal(ev.First.Sig, ev.Second.Sig) {
		return fmt.Errorf("dsledger: evidence must contain two distinct shares")
	}

	switch ev.Kind {
	case ConflictSameHeight:
		if ev.First.Height != ev.Second.Height || ev.First.Phase != ev.Second.Phase {
			return fmt.Errorf("dsledger: same-height conflict requires equal height and phase")
		}
		if ev.First.Block == ev.Second.Block {
			return fmt.Errorf("dsledger: same-height conflict requires distinct blocks")
		}
	case ConflictRound:
		if ev.First.Height != ev.Second.Height {
			return fmt.Errorf("dsledger: round conflict requires equal height")
		}
		if ev.First.Phase != PhasePrepare || ev.Second.Phase != PhaseCommit {
			return fmt.Errorf("dsledger: round conflict requires one prepare and one commit")
		}
		if ev.First.Block != ev.Second.Block {
			return fmt.Errorf("dsledger: round conflict requires the same block")
		}
		if ev.First.Round <= ev.Second.Round {
			return fmt.Errorf("dsledger: round conflict requires prepare round > commit round")
		}
	case ConflictParentFork:
		if ev.First.Phase != PhasePrepare || ev.Second.Phase != PhaseCommit {
			return fmt.Errorf("dsledger: parent fork requires a prepare and a commit")
		}
		if ev.First.Height <= ev.Second.Height {
			return fmt.Errorf("dsledger: parent fork requires prepare height above the committed height")
		}
		if ev.First.Parent == ev.Second.Block {
			return fmt.Errorf("dsledger: parent fork requires parent digest to differ from committed block")
		}
	}
	return nil
}

func verifyEvidenceShare(member Member, chainID string, s Share, wantSigner MemberID) error {
	if s.Signer != wantSigner {
		return fmt.Errorf("share signer %q does not match evidence signer %q", s.Signer, wantSigner)
	}
	if s.ChainID != chainID {
		return fmt.Errorf("share chain id %q does not match roster", s.ChainID)
	}
	if !s.Phase.Valid() {
		return fmt.Errorf("share has invalid phase")
	}
	if len(s.Sig) != ed25519.SignatureSize {
		return fmt.Errorf("share has invalid signature size")
	}
	msg := SigningBytes(s.ChainID, s.Height, s.Round, s.Phase, s.Block, s.Parent)
	if !ed25519.Verify(member.PublicKey, msg, s.Sig) {
		return fmt.Errorf("share signature is invalid")
	}
	return nil
}
