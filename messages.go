package netsec

import (
	"bytes"
	"encoding/binary"
)

const (
	joinRequestDomain = "LJR1"
	joinAcceptDomain  = "LJA1"

	// JoinNonceMax24 is the largest allocatable 24-bit JoinNonce. Once
	// reached the device is permanently rejected; the counter never wraps.
	JoinNonceMax24 = 1<<24 - 1
)

// JoinRequest is the over-the-air join request.
type JoinRequest struct {
	JoinEUI  [8]byte
	DevEUI   [8]byte
	DevNonce uint16
	MIC      [4]byte
}

// Canonical returns the canonical byte encoding covered by the MIC
// callback: domain || JoinEUI || DevEUI || DevNonce(LE16).
func (r *JoinRequest) Canonical() []byte {
	out := make([]byte, 0, 4+8+8+2)
	out = append(out, joinRequestDomain...)
	out = append(out, r.JoinEUI[:]...)
	out = append(out, r.DevEUI[:]...)
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], r.DevNonce)
	return append(out, tmp[:]...)
}

// Marshal returns the wire encoding: canonical bytes || MIC.
func (r *JoinRequest) Marshal() []byte {
	return append(r.Canonical(), r.MIC[:]...)
}

// ParseJoinRequest decodes a wire JoinRequest.
func ParseJoinRequest(b []byte) (*JoinRequest, error) {
	const canonLen = 4 + 8 + 8 + 2
	if len(b) != canonLen+4 || !bytes.Equal(b[:4], []byte(joinRequestDomain)) {
		return nil, ErrMalformedMessage
	}
	r := &JoinRequest{}
	copy(r.JoinEUI[:], b[4:12])
	copy(r.DevEUI[:], b[12:20])
	r.DevNonce = binary.LittleEndian.Uint16(b[20:22])
	copy(r.MIC[:], b[canonLen:])
	return r, nil
}

// JoinAccept is the encrypted and authenticated join response.
// The plaintext is encrypted first; the MIC then authenticates the
// ciphertext (encrypt-then-MAC).
type JoinAccept struct {
	Ciphertext []byte
	MIC        [4]byte
}

// joinAcceptBody is the plaintext bound into every JoinAccept.
type joinAcceptBody struct {
	JoinNonce  uint32 // 24-bit
	NetID      uint32 // 24-bit
	DevEUI     [8]byte
	DevNonce   uint16
	SessionID  [16]byte
	SessionGen uint64
}

func (b *joinAcceptBody) marshal() []byte {
	out := make([]byte, 0, 4+3+3+8+2+16+8)
	out = append(out, joinAcceptDomain...)
	var tmp [8]byte
	binary.LittleEndian.PutUint32(tmp[:4], b.JoinNonce)
	out = append(out, tmp[:3]...)
	binary.LittleEndian.PutUint32(tmp[:4], b.NetID)
	out = append(out, tmp[:3]...)
	out = append(out, b.DevEUI[:]...)
	binary.LittleEndian.PutUint16(tmp[:2], b.DevNonce)
	out = append(out, tmp[:2]...)
	out = append(out, b.SessionID[:]...)
	binary.LittleEndian.PutUint64(tmp[:8], b.SessionGen)
	return append(out, tmp[:8]...)
}

func parseJoinAcceptBody(b []byte) (*joinAcceptBody, error) {
	const bodyLen = 4 + 3 + 3 + 8 + 2 + 16 + 8
	if len(b) != bodyLen || !bytes.Equal(b[:4], []byte(joinAcceptDomain)) {
		return nil, ErrMalformedMessage
	}
	out := &joinAcceptBody{}
	out.JoinNonce = uint32(b[4]) | uint32(b[5])<<8 | uint32(b[6])<<16
	out.NetID = uint32(b[7]) | uint32(b[8])<<8 | uint32(b[9])<<16
	copy(out.DevEUI[:], b[10:18])
	out.DevNonce = binary.LittleEndian.Uint16(b[18:20])
	copy(out.SessionID[:], b[20:36])
	out.SessionGen = binary.LittleEndian.Uint64(b[36:44])
	return out, nil
}
