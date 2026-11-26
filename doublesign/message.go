package doublesign

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// Phase is the consensus phase a signed share belongs to.
type Phase string

const (
	PhasePrepare    Phase = "prepare"
	PhaseCommit     Phase = "commit"
	PhaseViewChange Phase = "view_change"
)

const signingDomain = "BFT-AUDIT/V1/SIGN"

// Message is a single signed consensus share. Every field that contributes to
// safety is covered by the member's Ed25519 signature (see SigningBytes), so a
// third party can verify the share with nothing but the anchored epoch key set.
//
// QCHeight/QCRound carry the highest prepare QC claimed by a view_change. They
// are present in the signed payload for every phase and must be zero outside
// view_change.
type Message struct {
	ChainID      string `json:"chainID"`
	Epoch        uint64 `json:"epoch"`
	Height       uint64 `json:"height"`
	Round        uint64 `json:"round"`
	Phase        Phase  `json:"phase"`
	MemberID     string `json:"memberID"`
	BlockDigest  []byte `json:"blockDigest"`
	ParentDigest []byte `json:"parentDigest"`
	LockedRound  uint64 `json:"lockedRound"`
	QCHeight     uint64 `json:"qcHeight"`
	QCRound      uint64 `json:"qcRound"`
	Signature    []byte `json:"signature"`
}

// SigningBytes returns the deterministic byte string a member must sign.
func SigningBytes(chainID string, m Message) []byte {
	b := appendString(nil, signingDomain)
	b = appendString(b, chainID)
	b = appendU64(b, m.Epoch)
	b = appendU64(b, m.Height)
	b = appendU64(b, m.Round)
	b = appendString(b, string(m.Phase))
	b = appendBytes(b, m.BlockDigest)
	b = appendBytes(b, m.ParentDigest)
	b = appendU64(b, m.LockedRound)
	b = appendU64(b, m.QCHeight)
	b = appendU64(b, m.QCRound)
	return b
}

// Sign produces a share signature with the member's Ed25519 private key.
func Sign(priv ed25519.PrivateKey, chainID string, m Message) []byte {
	return ed25519.Sign(priv, SigningBytes(chainID, m))
}

// Verify checks the share signature against the member's anchored public key.
func Verify(pub ed25519.PublicKey, chainID string, m Message) bool {
	if len(m.Signature) != ed25519.SignatureSize || len(pub) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(pub, SigningBytes(chainID, m), m.Signature)
}

// MessageID is a content-addressed identifier used for replay de-duplication.
func MessageID(m Message) string {
	h := sha256.New()
	h.Write(SigningBytes(m.ChainID, m))
	h.Write(m.Signature)
	return hex.EncodeToString(h.Sum(nil))
}

// WellFormed reports structural validity independent of signature or state.
func (m Message) WellFormed(chainID string) error {
	if m.ChainID != chainID {
		return errors.New("chainID mismatch")
	}
	switch m.Phase {
	case PhasePrepare, PhaseCommit, PhaseViewChange:
	default:
		return errors.New("unknown phase")
	}
	if m.MemberID == "" {
		return errors.New("missing member id")
	}
	if len(m.BlockDigest) == 0 {
		return errors.New("missing block digest")
	}
	if m.Phase != PhaseViewChange && (m.QCHeight != 0 || m.QCRound != 0) {
		return errors.New("qc claim only allowed on view_change")
	}
	return nil
}
