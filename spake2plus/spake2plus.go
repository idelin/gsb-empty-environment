// Package spake2plus implements the verifier (server) side of SPAKE2+
// as specified by RFC 9383, using the
// SPAKE2+-P256-SHA256-HKDF-SHA256-HMAC-SHA256 ciphersuite.
//
// The verifier stores only the registration outputs w0 and L = w1*B and
// never sees the password. A handshake looks like:
//
//	v, _ := spake2plus.NewVerifier(spake2plus.VerifierConfig{...})
//	shareV, _ := v.Start()                    // send shareV to the prover
//	confirmV, key, err := v.Finish(shareP, confirmP) // receive shareP, confirmP
//
// The prover's share is strictly validated (well-formed, on-curve,
// non-identity, and non-degenerate) so that the derived session key always
// depends on the password-derived secrets. The protocol transcript binds
// the application context, both endpoint identities, both shares, the
// secret elements Z and V, and w0. Confirmation MACs use separate keys per
// direction (K_confirmP for prover-to-verifier, K_confirmV for
// verifier-to-prover), which makes MAC reflection attacks infeasible.
package spake2plus

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

const (
	// ScalarLength is the byte length of a P-256 scalar (w0, w1, x, y).
	ScalarLength = 32
	// ShareLength is the byte length of a SEC 1 uncompressed P-256 point,
	// the canonical share encoding of the RFC 9383 P-256 ciphersuites.
	ShareLength = 65
	// ConfirmKeyLength is the byte length of each directional confirmation
	// key. For HMAC-based suites RFC 9383 recommends the Hash output size.
	ConfirmKeyLength = 32
	// ConfirmMACLength is the byte length of a confirmation MAC tag.
	ConfirmMACLength = sha256.Size
	// SharedKeyLength is the byte length of the exported session key.
	SharedKeyLength = sha256.Size
)

// hkdfExtract is HKDF-Extract (RFC 5869) with SHA-256. A nil salt means the
// all-zero salt, as used by RFC 9383.
func hkdfExtract(salt, ikm []byte) []byte {
	if salt == nil {
		salt = make([]byte, sha256.Size)
	}
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

// hkdfExpand is HKDF-Expand (RFC 5869) with SHA-256.
func hkdfExpand(prk, info []byte, length int) []byte {
	var out, t []byte
	for counter := byte(1); len(out) < length; counter++ {
		mac := hmac.New(sha256.New, prk)
		mac.Write(t)
		mac.Write(info)
		mac.Write([]byte{counter})
		t = mac.Sum(nil)
		out = append(out, t...)
	}
	return out[:length]
}

// writeElement appends one transcript element: an 8-byte little-endian
// length prefix followed by the element bytes (RFC 9383 Section 3.3).
func writeElement(h hash.Hash, b []byte) {
	var lenBuf [8]byte
	binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(b)))
	h.Write(lenBuf[:])
	h.Write(b)
}

// deriveKeys computes K_main = Hash(TT) from the full protocol transcript
// and expands it into the directional confirmation keys and the shared
// session key (RFC 9383 Sections 3.3 and 3.4).
func deriveKeys(context, idP, idV, shareP, shareV []byte, zEl, vEl point, w0 []byte) (kConfirmP, kConfirmV, kShared []byte) {
	h := sha256.New()
	writeElement(h, context)
	writeElement(h, idP)
	writeElement(h, idV)
	writeElement(h, encodedM)
	writeElement(h, encodedN)
	writeElement(h, shareP)
	writeElement(h, shareV)
	writeElement(h, encodePoint(zEl))
	writeElement(h, encodePoint(vEl))
	writeElement(h, w0)
	kMain := h.Sum(nil)

	prk := hkdfExtract(nil, kMain)
	confirm := hkdfExpand(prk, []byte("ConfirmationKeys"), 2*ConfirmKeyLength)
	kShared = hkdfExpand(prk, []byte("SharedKey"), SharedKeyLength)
	return confirm[:ConfirmKeyLength], confirm[ConfirmKeyLength:], kShared
}

// macTag computes the confirmation MAC (HMAC-SHA-256).
func macTag(key, msg []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(msg)
	return mac.Sum(nil)
}
