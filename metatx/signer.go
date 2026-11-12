package metatx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

// Signer re-issues an attribute value bound to the final content digest.
type Signer interface {
	Sign(digest []byte, namespace, key string, value []byte) ([]byte, error)
}

// HMACSigner is a Signer that emits HMAC-SHA256 over a domain-separated
// payload covering the content digest, namespace, key, and original value.
type HMACSigner struct {
	Key []byte
}

// Sign implements Signer.
func (s HMACSigner) Sign(digest []byte, namespace, key string, value []byte) ([]byte, error) {
	mac := hmac.New(sha256.New, s.Key)
	mac.Write([]byte("metatx/resign\x00"))
	writeField := func(b []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(b)))
		mac.Write(n[:])
		mac.Write(b)
	}
	writeField(digest)
	writeField([]byte(namespace))
	writeField([]byte(key))
	writeField(value)
	return mac.Sum(nil), nil
}
