package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

// digestSize is the length in bytes of a keyed record digest (SHA-256).
const digestSize = sha256.Size

// KeyedDigest deterministically maps a record to its ranking key using an
// HMAC-SHA-256 keyed by key. The digest covers every field that identifies the
// record's content, so two records with the same EventID always hash the same
// bytes while two different records cannot collide in the key space beyond the
// negligible HMAC collision probability.
//
// Fields are length-prefixed during encoding to make the byte layout
// unambiguous (no field can borrow bytes from an adjacent field).
func KeyedDigest(key []byte, r Record) [digestSize]byte {
	mac := hmac.New(sha256.New, key)

	var lenBuf [binary.MaxVarintLen64]byte
	putString := func(s string) {
		n := binary.PutUvarint(lenBuf[:], uint64(len(s)))
		mac.Write(lenBuf[:n])
		mac.Write([]byte(s))
	}
	putBytes := func(b []byte) {
		n := binary.PutUvarint(lenBuf[:], uint64(len(b)))
		mac.Write(lenBuf[:n])
		mac.Write(b)
	}
	putString(r.EventID)
	putString(r.UserID)
	n := binary.PutVarint(lenBuf[:], r.Window)
	mac.Write(lenBuf[:n])
	putString(r.Group)
	putBytes(r.Payload)

	var out [digestSize]byte
	copy(out[:], mac.Sum(nil))
	return out
}
