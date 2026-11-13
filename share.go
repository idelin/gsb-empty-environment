package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// SigningDomain separates share signatures from signatures in other protocols.
const SigningDomain = "example.org/netsec/q0057/share/v1"

// Phase is the threshold-signing phase of a share.
type Phase string

const (
	// Prepare is a prepare-phase share.
	Prepare Phase = "prepare"
	// Commit is a commit-phase share.
	Commit Phase = "commit"

	// PhasePrepare and PhaseCommit are aliases for callers that prefer explicit names.
	PhasePrepare = Prepare
	PhaseCommit  = Commit
)

// Valid reports whether p is a supported phase.
func (p Phase) Valid() bool {
	return p == Prepare || p == Commit
}

func (p Phase) wire() byte {
	switch p {
	case Prepare:
		return 1
	case Commit:
		return 2
	default:
		return 0
	}
}

// Member is a fixed member of the signing committee.
type Member struct {
	ID        string            `json:"id"`
	PublicKey ed25519.PublicKey `json:"public_key"`
}

// NewMember constructs a member from an ID and public key.
func NewMember(id string, publicKey ed25519.PublicKey) Member {
	return Member{ID: id, PublicKey: append(ed25519.PublicKey(nil), publicKey...)}
}

func (m Member) clone() Member {
	return Member{ID: m.ID, PublicKey: append(ed25519.PublicKey(nil), m.PublicKey...)}
}

// Share is an Ed25519-signed threshold-signing share.
//
// The signature covers the domain separator, chain ID, member ID, height,
// round, phase, block digest, and parent digest. It does not cover the
// signature field itself.
type Share struct {
	ChainID      string `json:"chain_id"`
	MemberID     string `json:"member_id"`
	Height       uint64 `json:"height"`
	Round        uint64 `json:"round"`
	Phase        Phase  `json:"phase"`
	BlockDigest  Digest `json:"block_digest"`
	ParentDigest Digest `json:"parent_digest"`
	Signature    []byte `json:"signature"`
}

// Validate checks the statically checkable fields of a share.
func (s Share) Validate() error {
	if s.ChainID == "" {
		return errors.New("netsec: empty chain ID")
	}
	if s.MemberID == "" {
		return errors.New("netsec: empty member ID")
	}
	if !s.Phase.Valid() {
		return fmt.Errorf("netsec: invalid phase %q", s.Phase)
	}
	if len(s.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("netsec: invalid signature length %d", len(s.Signature))
	}
	return nil
}

// SigningBytes returns the canonical byte string covered by the signature.
// Every variable-length field is length-prefixed, which makes the encoding
// unambiguous.
func (s Share) SigningBytes() []byte {
	var buf bytes.Buffer
	buf.Grow(64 + len(SigningDomain) + len(s.ChainID) + len(s.MemberID))
	buf.WriteString(SigningDomain)
	buf.WriteByte(0)
	writeString(&buf, s.ChainID)
	writeString(&buf, s.MemberID)

	var number [8]byte
	binary.BigEndian.PutUint64(number[:], s.Height)
	buf.Write(number[:])
	binary.BigEndian.PutUint64(number[:], s.Round)
	buf.Write(number[:])

	buf.WriteByte(s.Phase.wire())
	buf.Write(s.BlockDigest[:])
	buf.Write(s.ParentDigest[:])
	return buf.Bytes()
}

func writeString(buf *bytes.Buffer, s string) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(s)))
	buf.Write(length[:])
	buf.WriteString(s)
}

// EventID is the stable identity of a signed share. Ed25519 is deterministic,
// so replaying the same share produces the same ID and is idempotent.
func (s Share) EventID() string {
	h := sha256.New()
	h.Write([]byte("example.org/netsec/q0057/event/v1"))
	h.Write([]byte{0})
	h.Write(s.SigningBytes())
	h.Write(s.Signature)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return hex.EncodeToString(sum[:])
}

// SignWith returns a copy of s with an Ed25519 signature.
func (s Share) SignWith(privateKey ed25519.PrivateKey) (Share, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Share{}, fmt.Errorf("netsec: invalid private key length %d", len(privateKey))
	}
	s.Signature = ed25519.Sign(privateKey, s.SigningBytes())
	return s, nil
}

// SignShare signs s with privateKey.
func SignShare(privateKey ed25519.PrivateKey, s Share) (Share, error) {
	return s.SignWith(privateKey)
}

// Verify checks the signature against publicKey.
func (s Share) Verify(publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("netsec: invalid public key length %d", len(publicKey))
	}
	if len(s.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("netsec: invalid signature length %d", len(s.Signature))
	}
	if !ed25519.Verify(publicKey, s.SigningBytes(), s.Signature) {
		return errors.New("netsec: invalid share signature")
	}
	return nil
}
