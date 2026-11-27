package tls13zerortt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// MaxHKDFExpandLength bounds HKDF-Expand output per RFC 5869 Section 2.3:
// L <= 255 * HashLen. For SHA-256 this is 8160 bytes.
const MaxHKDFExpandLength = 255 * HashLength

// tls13LabelPrefix is the literal prefix required by the HKDFLabel structure
// in RFC 8446 Section 7.1.
const tls13LabelPrefix = "tls13 "

var errHKDFLength = errors.New("tls13zerortt: HKDF-Expand length out of range")

// hkdfExtract implements HKDF-Extract (RFC 5869 Section 2.2):
//
//	HKDF-Extract(salt, IKM) = HMAC-Hash(salt, IKM)
//
// A nil salt is treated as a string of HashLen zero bytes, as mandated by the
// RFC; HMAC with a zero-length key is not the same thing, so the caller passes
// an explicitly zeroed slice when extracting the early secret.
func hkdfExtract(salt, ikm []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

// hkdfExpand implements HKDF-Expand (RFC 5869 Section 2.3) for SHA-256.
func hkdfExpand(prk, info []byte, length int) ([]byte, error) {
	if length <= 0 || length > MaxHKDFExpandLength {
		return nil, errHKDFLength
	}
	mac := hmac.New(sha256.New, prk)
	okm := make([]byte, 0, length)
	var previous []byte
	for counter := byte(1); len(okm) < length; counter++ {
		mac.Reset()
		mac.Write(previous)
		mac.Write(info)
		mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		okm = append(okm, previous...)
		if counter == 0 {
			// Defensive: loop naturally terminates via the length bound, but
			// a wrapped counter would violate the RFC.
			return nil, errHKDFLength
		}
	}
	return okm[:length], nil
}

// hkdfExpandLabel implements HKDF-Expand-Label from RFC 8446 Section 7.1:
//
//	HKDF-Expand-Label(Secret, Label, Context, Length) =
//	    HKDF-Expand(Secret, HkdfLabel, Length)
//
//	struct {
//	    uint16 Length = Length;
//	    opaque label<7..255> = "tls13 " + Label;
//	    opaque context<0..255> = Context;
//	} HkdfLabel;
//
// The label and context use one-byte length prefixes (opaque vector); the
// output length is encoded as a two-byte big-endian integer.
func hkdfExpandLabel(secret []byte, label string, context []byte, length int) ([]byte, error) {
	fullLabel := tls13LabelPrefix + label
	if len(fullLabel) > 255 || len(context) > 255 {
		return nil, errHKDFLength
	}
	if length > 65535 {
		return nil, errHKDFLength
	}
	info := make([]byte, 0, 2+1+len(fullLabel)+1+len(context))
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(length))
	info = append(info, lenBuf[:]...)
	info = append(info, byte(len(fullLabel)))
	info = append(info, fullLabel...)
	info = append(info, byte(len(context)))
	info = append(info, context...)
	return hkdfExpand(secret, info, length)
}

// transcriptHash returns SHA-256(data). It is a tiny named helper so the
// derivation code mirrors the RFC notation directly.
func transcriptHash(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

// deriveSecret implements Derive-Secret (RFC 8446 Section 7.1):
//
//	Derive-Secret(Secret, Label, Messages) =
//	    HKDF-Expand-Label(Secret, Label,
//	                      Transcript-Hash(Messages), Hash.length)
//
// A nil messages slice hashes the empty string (the zero-length transcript
// used when deriving the early binder and finished keys).
func deriveSecret(secret []byte, label string, messages []byte) ([]byte, error) {
	return hkdfExpandLabel(secret, label, transcriptHash(messages), HashLength)
}
