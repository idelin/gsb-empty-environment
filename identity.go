package netsec

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"math"
)

const (
	// ProtocolDomain is the fixed domain label mixed into every response MAC.
	// It deliberately differs from the request-digest label below.
	ProtocolDomain = "q0130/dnp3-challenge-auth/v1"

	// RequestDigestDomain is the fixed domain label used when freezing the
	// digest of a fully assembled application request.
	RequestDigestDomain = "q0130/dnp3-request-digest/v1"

	// IdentityDomain is the fixed domain label used by MarshalIdentity.
	IdentityDomain = "q0130/dnp3-request-identity/v1"
)

// RequestIdentity is the complete identity of a high-risk control request.
//
// RequestDigest must be the digest returned by ComputeRequestDigest for the
// same fields (with RequestDigest itself excluded).  Submit and the fragment
// reassembler freeze the digest again and reject a caller-supplied digest that
// does not match.  OperationValue is copied on the way in and on the way out;
// it is never aliased with the caller's memory after submission.
type RequestIdentity struct {
	MasterAddress        uint16
	OutstationAddress    uint16
	ConnectionGeneration uint64
	ApplicationSequence  uint32
	FunctionCode         uint8
	ObjectGroup          uint8
	ObjectVariation      uint8
	ObjectIndex          uint32
	OperationValue       []byte
	RequestDigest        [32]byte
}

// ConnectionID identifies one connection generation between a master and an
// outstation.
type ConnectionID struct {
	MasterAddress        uint16
	OutstationAddress    uint16
	ConnectionGeneration uint64
}

// Connection returns the connection identity embedded in the request.
func (id RequestIdentity) Connection() ConnectionID {
	return ConnectionID{
		MasterAddress:        id.MasterAddress,
		OutstationAddress:    id.OutstationAddress,
		ConnectionGeneration: id.ConnectionGeneration,
	}
}

// Clone returns a deep copy of the identity.
func (id RequestIdentity) Clone() RequestIdentity {
	out := id
	out.OperationValue = cloneBytes(id.OperationValue)
	return out
}

// ChallengeID is a 128-bit random value that names one challenge inside a
// connection.  The gateway also checks global uniqueness as a safety measure.
type ChallengeID [16]byte

// String returns the lower-case hexadecimal form of the challenge id.
func (id ChallengeID) String() string {
	return hex.EncodeToString(id[:])
}

// ParseChallengeID parses a 32-character hexadecimal challenge id.
func ParseChallengeID(s string) (ChallengeID, error) {
	var id ChallengeID
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != len(id) {
		return id, ErrInvalidChallengeID
	}
	copy(id[:], raw)
	return id, nil
}

// ComputeRequestDigest returns SHA-256 over the fixed request-digest domain
// and every identity field except RequestDigest itself.
func ComputeRequestDigest(id RequestIdentity) [32]byte {
	h := sha256.New()
	writeLengthPrefixed(h, []byte(RequestDigestDomain))
	writeIdentity(h, id, false)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// MarshalIdentity returns the canonical byte encoding of the complete identity,
// including its frozen request digest.  It is useful for audit records that
// must be compared byte-for-byte with the identity protected by the MAC.
func MarshalIdentity(id RequestIdentity) []byte {
	buf := make([]byte, 0, 160+len(id.OperationValue))
	buf = appendLengthPrefixed(buf, []byte(IdentityDomain))
	buf = appendIdentity(buf, id, true)
	return buf
}

// MACInput returns the exact byte string covered by an HMAC-SHA256 challenge
// response: the fixed protocol domain label, the complete request identity,
// the nonce, the challenge id, and the key id.
func MACInput(id RequestIdentity, nonce [32]byte, challengeID ChallengeID, keyID KeyID) []byte {
	buf := make([]byte, 0, 256+len(id.OperationValue)+len(keyID))
	buf = appendLengthPrefixed(buf, []byte(ProtocolDomain))
	buf = appendIdentity(buf, id, true)
	buf = append(buf, nonce[:]...)
	buf = append(buf, challengeID[:]...)
	buf = appendLengthPrefixed(buf, []byte(keyID))
	return buf
}

func freezeIdentity(id RequestIdentity) (RequestIdentity, error) {
	if uint64(len(id.OperationValue)) > math.MaxUint32 {
		return RequestIdentity{}, ErrInvalidRequest
	}
	frozen := id.Clone()
	digest := ComputeRequestDigest(frozen)
	if frozen.RequestDigest != ([32]byte{}) &&
		subtle.ConstantTimeCompare(frozen.RequestDigest[:], digest[:]) != 1 {
		return RequestIdentity{}, ErrRequestDigestMismatch
	}
	frozen.RequestDigest = digest
	return frozen, nil
}

// FreezeRequest returns a deep copy of id with its request digest frozen.
// A non-zero caller-supplied digest must already be correct.
func FreezeRequest(id RequestIdentity) (RequestIdentity, error) {
	return freezeIdentity(id)
}

func appendIdentity(dst []byte, id RequestIdentity, includeDigest bool) []byte {
	var scratch [8]byte
	binary.BigEndian.PutUint16(scratch[0:2], id.MasterAddress)
	dst = append(dst, scratch[0:2]...)
	binary.BigEndian.PutUint16(scratch[0:2], id.OutstationAddress)
	dst = append(dst, scratch[0:2]...)
	binary.BigEndian.PutUint64(scratch[0:8], id.ConnectionGeneration)
	dst = append(dst, scratch[0:8]...)
	binary.BigEndian.PutUint32(scratch[0:4], id.ApplicationSequence)
	dst = append(dst, scratch[0:4]...)
	dst = append(dst, id.FunctionCode, id.ObjectGroup, id.ObjectVariation)
	binary.BigEndian.PutUint32(scratch[0:4], id.ObjectIndex)
	dst = append(dst, scratch[0:4]...)
	dst = appendLengthPrefixed(dst, id.OperationValue)
	if includeDigest {
		dst = append(dst, id.RequestDigest[:]...)
	}
	return dst
}

func writeIdentity(h hash.Hash, id RequestIdentity, includeDigest bool) {
	var scratch [8]byte
	binary.BigEndian.PutUint16(scratch[0:2], id.MasterAddress)
	_, _ = h.Write(scratch[0:2])
	binary.BigEndian.PutUint16(scratch[0:2], id.OutstationAddress)
	_, _ = h.Write(scratch[0:2])
	binary.BigEndian.PutUint64(scratch[0:8], id.ConnectionGeneration)
	_, _ = h.Write(scratch[0:8])
	binary.BigEndian.PutUint32(scratch[0:4], id.ApplicationSequence)
	_, _ = h.Write(scratch[0:4])
	_, _ = h.Write([]byte{id.FunctionCode, id.ObjectGroup, id.ObjectVariation})
	binary.BigEndian.PutUint32(scratch[0:4], id.ObjectIndex)
	_, _ = h.Write(scratch[0:4])
	writeLengthPrefixed(h, id.OperationValue)
	if includeDigest {
		_, _ = h.Write(id.RequestDigest[:])
	}
}

func appendLengthPrefixed(dst, value []byte) []byte {
	var scratch [4]byte
	binary.BigEndian.PutUint32(scratch[0:4], uint32(len(value)))
	dst = append(dst, scratch[0:4]...)
	return append(dst, value...)
}

func writeLengthPrefixed(h hash.Hash, value []byte) {
	var scratch [4]byte
	binary.BigEndian.PutUint32(scratch[0:4], uint32(len(value)))
	_, _ = h.Write(scratch[0:4])
	_, _ = h.Write(value)
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
