package quicguard

import (
	"crypto/hmac"
	"crypto/sha256"
)

// hkdfExtract implements HKDF-Extract (RFC 5869) with HMAC-SHA256.
func hkdfExtract(salt, ikm []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

// hkdfExpandLabel implements HKDF-Expand-Label from RFC 8446 section 7.1 and
// RFC 9001 section 5.2, using HMAC-SHA256 and an empty context.
func hkdfExpandLabel(secret []byte, label string, length int) []byte {
	fullLabel := "tls13 " + label
	if len(fullLabel) > 255 || length > 255 {
		panic("quicguard: hkdfExpandLabel input out of range")
	}
	info := make([]byte, 0, 2+1+len(fullLabel)+1)
	info = append(info, byte(length>>8), byte(length))
	info = append(info, byte(len(fullLabel)))
	info = append(info, fullLabel...)
	info = append(info, 0) // zero-length context

	okm := make([]byte, 0, length)
	var previous []byte
	for counter := byte(1); len(okm) < length; counter++ {
		mac := hmac.New(sha256.New, secret)
		mac.Write(previous)
		mac.Write(info)
		mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		okm = append(okm, previous...)
	}
	return okm[:length]
}
