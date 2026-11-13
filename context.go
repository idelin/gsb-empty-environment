package netsec

import (
	"crypto/subtle"
	"encoding/binary"
)

// PartyInfo describes one participant of a key-agreement session. It is the
// COSE_KDF_Context PartyU/PartyV analogue: identity, nonce and ephemeral
// public key committed to by that party.
type PartyInfo struct {
	Identity  []byte `json:"id"`
	Nonce     []byte `json:"nonce"`
	PublicKey []byte `json:"pub"`
}

// Context is the key-agreement context a recipient is expected to possess
// for a given session. A COSE_Encrypt recipient is only trustworthy when the
// context carried by the message matches the context the caller established
// for the session.
type Context struct {
	SessionID []byte    `json:"sid"`
	PartyU    PartyInfo `json:"u"`
	PartyV    PartyInfo `json:"v"`
}

// encodeFields deterministically encodes length-prefixed byte strings.
func encodeFields(fields ...[]byte) []byte {
	var out []byte
	var size [8]byte
	for _, f := range fields {
		binary.BigEndian.PutUint64(size[:], uint64(len(f)))
		out = append(out, size[:]...)
		out = append(out, f...)
	}
	return out
}

func (p PartyInfo) encode() []byte {
	return encodeFields(p.Identity, p.Nonce, p.PublicKey)
}

// encode returns the deterministic encoding of the context. It feeds both
// the key-derivation info string and the equality check, so any context
// difference changes derived keys and fails the comparison.
func (c *Context) encode() []byte {
	return encodeFields(c.SessionID, c.PartyU.encode(), c.PartyV.encode())
}

// contextEqual reports whether two contexts are identical, in constant time
// with respect to the encoded content.
func contextEqual(a, b *Context) bool {
	if a == nil || b == nil {
		return false
	}
	return subtle.ConstantTimeCompare(a.encode(), b.encode()) == 1
}
