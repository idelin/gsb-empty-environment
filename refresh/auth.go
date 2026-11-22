package refresh

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/big"
)

// Domain separation prefixes keep the three message classes from being
// interchangeable even if fields happen to collide.
const (
	domainIncrement  = "refresh-increment-v1|"
	domainComplaint  = "refresh-complaint-v1|"
	domainTranscript = "refresh-transcript-v1|"
)

// Complaint reasons carried on signed complaint records.
const (
	ReasonMissingDelivery = "missing_delivery"
	ReasonBadIncrement    = "bad_increment"
)

// PublicKey is an authenticator-owned identity key. Multiple participant IDs
// may be aliases of the same key; Canonical resolves them to one identity.
type PublicKey interface {
	Equals(other PublicKey) bool
	Marshal() ([]byte, error)
}

// Authenticator abstracts member authentication and alias resolution. The key
// host injects its KMS / signer implementation; this package never touches raw
// private key material.
type Authenticator interface {
	// Canonical maps any participant ID (including an alias) to a stable
	// canonical ID for the underlying public key. Two aliases of one key must
	// resolve to the same canonical ID. Unknown IDs return an error.
	Canonical(participantID string) (canonicalID string, key PublicKey, err error)
	// Sign produces a signature over message on behalf of participantID.
	Sign(participantID string, message []byte) ([]byte, error)
	// Verify checks a signature against the key behind canonicalID.
	Verify(canonicalID string, message, signature []byte) bool
}

// RecordedIncrement is the signed, point-to-point delivery from a sender to a
// recipient for one refresh round.
type RecordedIncrement struct {
	Round            uint64   `json:"round"`
	SenderID         string   `json:"sender_id"`
	RecipientID      string   `json:"recipient_id"`
	Delta            *big.Int `json:"delta"`
	TranscriptDigest []byte   `json:"transcript_digest"`
	Signature        []byte   `json:"signature"`
}

// RecordedComplaint is a recipient's signed complaint against a sender. It
// binds the round and both party identities, so it cannot be replayed into a
// different round or reassigned to another pair.
type RecordedComplaint struct {
	Round            uint64 `json:"round"`
	SenderID         string `json:"sender_id"`
	RecipientID      string `json:"recipient_id"`
	Reason           string `json:"reason"`
	TranscriptDigest []byte `json:"transcript_digest,omitempty"`
	Signature        []byte `json:"signature"`
}

// EncodedCommitment is a commitment in wire form: group terms are encoded via
// Point.Marshal and decoded with Group.ParsePoint.
type EncodedCommitment struct {
	Round    uint64   `json:"round"`
	SenderID string   `json:"sender_id"`
	Terms    [][]byte `json:"terms"`
}

// EvidenceBundle is the public, mergeable collection of material gathered for a
// round. Any field may be empty: missing material is reported precisely rather
// than patched over during merging.
type EvidenceBundle struct {
	Round       uint64              `json:"round"`
	Commitments []EncodedCommitment `json:"commitments"`
	Increments  []RecordedIncrement `json:"increments"`
	Complaints  []RecordedComplaint `json:"complaints"`
}

func appendString(dst []byte, s string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(s)))
	return append(dst, s...)
}

func appendBytes(dst, b []byte) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(b)))
	return append(dst, b...)
}

func appendScalar(dst []byte, s *big.Int) []byte {
	if s == nil {
		return binary.AppendUvarint(dst, 0)
	}
	return appendBytes(dst, s.Bytes())
}

// IncrementSigningBytes returns the exact byte string covered by an increment
// signature. It is exported so hosts can inspect or reconstruct signatures.
func IncrementSigningBytes(inc RecordedIncrement) []byte {
	out := appendString(nil, domainIncrement)
	out = binary.AppendUvarint(out, inc.Round)
	out = appendString(out, inc.SenderID)
	out = appendString(out, inc.RecipientID)
	out = appendBytes(out, inc.TranscriptDigest)
	out = appendScalar(out, inc.Delta)
	return out
}

// ComplaintSigningBytes returns the exact byte string covered by a complaint
// signature.
func ComplaintSigningBytes(c RecordedComplaint) []byte {
	out := appendString(nil, domainComplaint)
	out = binary.AppendUvarint(out, c.Round)
	out = appendString(out, c.SenderID)
	out = appendString(out, c.RecipientID)
	out = appendString(out, c.Reason)
	out = appendBytes(out, c.TranscriptDigest)
	return out
}

// SignIncrement fills the record's signature on behalf of the sender.
func SignIncrement(auth Authenticator, inc *RecordedIncrement) error {
	if inc == nil {
		return errors.New("refresh: nil increment record")
	}
	sig, err := auth.Sign(inc.SenderID, IncrementSigningBytes(*inc))
	if err != nil {
		return err
	}
	inc.Signature = sig
	return nil
}

// SignComplaint fills the complaint's signature on behalf of the recipient.
func SignComplaint(auth Authenticator, c *RecordedComplaint) error {
	if c == nil {
		return errors.New("refresh: nil complaint record")
	}
	sig, err := auth.Sign(c.RecipientID, ComplaintSigningBytes(*c))
	if err != nil {
		return err
	}
	c.Signature = sig
	return nil
}

// TranscriptDigest hashes the round's public transcript: the round number, the
// ordered sender set and each sender's full coefficient commitment. All
// recipients must reference the same digest before a round may be published.
func TranscriptDigest(round uint64, commitments []Commitment) ([]byte, error) {
	ordered := make([]Commitment, len(commitments))
	copy(ordered, commitments)
	sortCommitments(ordered)
	out := appendString(nil, domainTranscript)
	out = binary.AppendUvarint(out, round)
	out = binary.AppendUvarint(out, uint64(len(ordered)))
	for _, c := range ordered {
		out = appendString(out, c.SenderID)
		raw, err := MarshalCommitment(c)
		if err != nil {
			return nil, err
		}
		out = appendBytes(out, raw)
	}
	sum := sha256.Sum256(out)
	return sum[:], nil
}

func sortCommitments(cs []Commitment) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j-1].SenderID > cs[j].SenderID; j-- {
			cs[j-1], cs[j] = cs[j], cs[j-1]
		}
	}
}
