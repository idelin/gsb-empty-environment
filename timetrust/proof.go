package timetrust

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"time"
)

// ProofVersion is the wire version of the time-proof canonical encoding.
const ProofVersion byte = 1

// TimeProof is a signed assertion that at Timestamp the signing server saw
// the supplied device challenge. It is the only input allowed to raise the
// trusted time lower bound.
type TimeProof struct {
	// Timestamp is the signed wall-clock instant in Unix nanoseconds.
	Timestamp time.Time
	// Nonce is the single-use challenge issued by this device.
	Nonce []byte
	// Signature covers canonicalProofBytes(Version, Timestamp, Nonce).
	Signature []byte
}

// ProofVerifier authenticates a time proof. A verifier also owns the rules
// for well-formedness so that Trust never reasons over malformed input.
type ProofVerifier interface {
	Verify(proof TimeProof) error
}

// ErrMalformedProof means a proof is missing fields required by the wire
// format before any signature check is possible.
var ErrMalformedProof = errors.New("timetrust: malformed time proof")

// ErrBadSignature means a proof failed authentication.
var ErrBadSignature = errors.New("timetrust: invalid proof signature")

// canonicalProofBytes builds the byte string that gets signed. It is
// length-prefixed throughout, so distinct (timestamp, nonce) pairs can never
// share an encoding.
func canonicalProofBytes(version byte, timestamp time.Time, nonce []byte) []byte {
	nonceLen := len(nonce)
	buf := make([]byte, 0, 1+8+4+nonceLen)
	buf = append(buf, version)
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(timestamp.UnixNano()))
	buf = append(buf, stamp[:]...)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(nonceLen))
	buf = append(buf, length[:]...)
	buf = append(buf, nonce...)
	return buf
}

// Ed25519Verifier verifies proofs signed with Ed25519.
type Ed25519Verifier struct {
	PublicKey ed25519.PublicKey
}

// Verify implements ProofVerifier.
func (v Ed25519Verifier) Verify(proof TimeProof) error {
	if len(v.PublicKey) != ed25519.PublicKeySize ||
		proof.Timestamp.IsZero() ||
		len(proof.Nonce) == 0 ||
		len(proof.Signature) != ed25519.SignatureSize {
		return ErrMalformedProof
	}
	message := canonicalProofBytes(ProofVersion, proof.Timestamp, proof.Nonce)
	if !ed25519.Verify(v.PublicKey, message, proof.Signature) {
		return ErrBadSignature
	}
	return nil
}

// SignProof issues a proof for a device challenge. It is provided so update
// hosts (and tests) can produce proofs with the exact canonical encoding
// Ed25519Verifier expects; production signing happens on the server.
func SignProof(private ed25519.PrivateKey, timestamp time.Time, nonce []byte) TimeProof {
	message := canonicalProofBytes(ProofVersion, timestamp, nonce)
	return TimeProof{
		Timestamp: timestamp,
		Nonce:     append([]byte(nil), nonce...),
		Signature: ed25519.Sign(private, message),
	}
}
