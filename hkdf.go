package noise

import (
	"crypto/hmac"
	"crypto/sha256"
)

func hmacSHA256(key, data []byte) [hashSize]byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	var out [hashSize]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// hkdf2 implements Noise's two-output HKDF with SHA-256. ck is the salt and
// ikm is the input keying material.
func hkdf2(ck, ikm []byte) (first, second [hashSize]byte) {
	prk := hmacSHA256(ck, ikm)
	first = hmacSHA256(prk[:], []byte{0x01})
	var input [hashSize + 1]byte
	copy(input[:hashSize], first[:])
	input[hashSize] = 0x02
	second = hmacSHA256(prk[:], input[:])
	zero(prk[:])
	zero(input[:])
	return first, second
}
