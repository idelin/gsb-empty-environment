package tls13sm

import (
	"crypto/hmac"
	"hash"
)

// hkdfExtract implements RFC 5869 HKDF-Extract: HMAC-Hash(salt, IKM). A nil or
// zero-length salt is replaced by a string of Hash.length zeros, matching the
// use of the zero PSK for the Early Secret.
func hkdfExtract(hash func() hash.Hash, salt, ikm []byte) []byte {
	if len(salt) == 0 {
		salt = make([]byte, hash().Size())
	}
	mac := hmac.New(hash, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

// hkdfExpand implements RFC 5869 HKDF-Expand with an explicit info string and
// output length L. It enforces the 255*HashLen ceiling from RFC 5869.
func hkdfExpand(hash func() hash.Hash, prk, info []byte, length int) []byte {
	hashLen := hash().Size()
	if length <= 0 {
		return nil
	}
	if length > 255*hashLen {
		panic("tls13sm: hkdfExpand length exceeds 255*HashLen")
	}
	out := make([]byte, 0, length)
	var previous []byte
	counter := byte(1)
	for len(out) < length {
		mac := hmac.New(hash, prk)
		mac.Write(previous)
		mac.Write(info)
		mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		out = append(out, previous...)
		counter++
	}
	return out[:length]
}

// hkdfExpandLabel implements HKDF-Expand-Label from RFC 8446 section 7.1.
//
//	struct {
//	    uint16 length = Length;
//	    opaque label<7..255> = "tls13 " + Label;
//	    opaque context<0..255> = Context;
//	} HkdfLabel;
//
// The wire label therefore always carries the seven-byte "tls13 " prefix.
func hkdfExpandLabel(hash func() hash.Hash, secret []byte, label string, context []byte, length int) []byte {
	fullLabel := "tls13 " + label
	if len(fullLabel) > 255 {
		panic("tls13sm: HKDF label longer than 255 bytes")
	}
	if len(context) > 255 {
		panic("tls13sm: HKDF context longer than 255 bytes")
	}
	info := make([]byte, 0, 2+1+len(fullLabel)+1+len(context))
	info = appendU16(info, uint16(length))
	info = appendU8(info, byte(len(fullLabel)))
	info = append(info, fullLabel...)
	info = appendU8(info, byte(len(context)))
	info = append(info, context...)
	return hkdfExpand(hash, secret, info, length)
}

// deriveSecret implements Derive-Secret from RFC 8446 section 7.1:
//
//	Derive-Secret(Secret, Label, Messages) =
//	    HKDF-Expand-Label(Secret, Label, Transcript-Hash(Messages), Hash.length)
//
// The transcript hash is always taken from the state machine's current
// running hash (which already includes any HRR synthetic message_hash).
func deriveSecret(hash func() hash.Hash, secret []byte, label string, transcriptHash []byte) []byte {
	return hkdfExpandLabel(hash, secret, label, transcriptHash, hash().Size())
}

// finishedKey derives the per-direction Finished key:
// HKDF-Expand-Label(BaseKey, "finished", "", Hash.length).
func finishedKey(hash func() hash.Hash, baseKey []byte) []byte {
	return hkdfExpandLabel(hash, baseKey, "finished", nil, hash().Size())
}

// finishedVerifyData computes the verify_data of a Finished message:
// HMAC(finished_key, Transcript-Hash(Handshake Context, Certificate*,
// CertificateVerify*, Finished)).
func finishedVerifyData(hash func() hash.Hash, key, transcriptHash []byte) []byte {
	mac := hmac.New(hash, key)
	mac.Write(transcriptHash)
	return mac.Sum(nil)
}
