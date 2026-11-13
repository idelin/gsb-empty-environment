package netsec

import "encoding/binary"

// Wire layout constants. Suite IDs and digests are fixed-size fields; the two
// ciphertexts are prefixed with big-endian uint16 lengths.
const (
	suiteIDSize = 8
	digestSize  = 32
	lengthSize  = 2

	// fixedPartSize covers suite ID, both length prefixes and the three
	// digests (client public key, server public key, handshake context).
	fixedPartSize = suiteIDSize + 2*lengthSize + 3*digestSize

	// maxCiphertextLen is the largest value representable by the uint16
	// length prefix.
	maxCiphertextLen = 1<<16 - 1
)

// Request is the strictly-validated hybrid decapsulation input.
//
// It contains the suite ID, the two length-prefixed component ciphertexts,
// the canonical client and server public key digests, and the handshake
// context digest. All digests are 32 bytes.
type Request struct {
	SuiteID              [suiteIDSize]byte
	Ciphertext1          []byte
	Ciphertext2          []byte
	ClientPublicKeyHash  [digestSize]byte
	ServerPublicKeyHash  [digestSize]byte
	HandshakeContextHash [digestSize]byte
}

// Wire layout:
//	suite ID                   8 bytes
//	len(ct1)                   uint16 big-endian
//	ct1                        len(ct1) bytes
//	len(ct2)                   uint16 big-endian
//	ct2                        len(ct2) bytes
//	client public key digest   32 bytes
//	server public key digest   32 bytes
//	handshake context digest   32 bytes
//
// There must be no trailing bytes.

// ParseRequest strictly parses the wire input into a Request.
//
// Any truncation, uint16 length that overruns the buffer, ciphertext longer
// than 65535 bytes (implicit via the prefix), or trailing byte causes
// ErrMalformedInput. Ciphertext contents are copied so later mutation of the
// input buffer cannot affect the returned Request.
func ParseRequest(wire []byte) (Request, error) {
	if len(wire) < fixedPartSize {
		return Request{}, ErrMalformedInput
	}

	var req Request
	off := 0
	copy(req.SuiteID[:], wire[off:off+suiteIDSize])
	off += suiteIDSize

	ct1Len := int(binary.BigEndian.Uint16(wire[off : off+lengthSize]))
	off += lengthSize
	if len(wire)-off < ct1Len {
		return Request{}, ErrMalformedInput
	}
	req.Ciphertext1 = append([]byte(nil), wire[off:off+ct1Len]...)
	off += ct1Len

	if len(wire)-off < lengthSize {
		return Request{}, ErrMalformedInput
	}
	ct2Len := int(binary.BigEndian.Uint16(wire[off : off+lengthSize]))
	off += lengthSize
	if len(wire)-off < ct2Len {
		return Request{}, ErrMalformedInput
	}
	req.Ciphertext2 = append([]byte(nil), wire[off:off+ct2Len]...)
	off += ct2Len

	if len(wire)-off != 3*digestSize {
		return Request{}, ErrMalformedInput
	}
	copy(req.ClientPublicKeyHash[:], wire[off:off+digestSize])
	off += digestSize
	copy(req.ServerPublicKeyHash[:], wire[off:off+digestSize])
	off += digestSize
	copy(req.HandshakeContextHash[:], wire[off:off+digestSize])
	off += digestSize

	if off != len(wire) {
		return Request{}, ErrMalformedInput
	}
	return req, nil
}

// Encode serializes the Request into the wire layout.
//
// It returns ErrMalformedInput if either ciphertext exceeds the uint16 length
// prefix. The returned buffer does not alias any Request field.
func (r Request) Encode() ([]byte, error) {
	if len(r.Ciphertext1) > maxCiphertextLen || len(r.Ciphertext2) > maxCiphertextLen {
		return nil, ErrMalformedInput
	}

	out := make([]byte, 0, fixedPartSize+len(r.Ciphertext1)+len(r.Ciphertext2))
	out = append(out, r.SuiteID[:]...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(r.Ciphertext1)))
	out = append(out, r.Ciphertext1...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(r.Ciphertext2)))
	out = append(out, r.Ciphertext2...)
	out = append(out, r.ClientPublicKeyHash[:]...)
	out = append(out, r.ServerPublicKeyHash[:]...)
	out = append(out, r.HandshakeContextHash[:]...)
	return out, nil
}
