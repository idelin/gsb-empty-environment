package tcpao

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"hash"
)

// MACInput returns the canonical bytes authenticated for a TCP-AO option. The
// two key identifiers are prepended to macData so that changing KeyID or
// RNextKeyID invalidates the MAC. macData must contain the rest of the
// transport's authenticated input, such as the TCP pseudo-header, TCP header,
// and payload.
func MACInput(keyID, rNextKeyID KeyID, macData []byte) []byte {
	out := make([]byte, 2+len(macData))
	out[0] = byte(keyID)
	out[1] = byte(rNextKeyID)
	copy(out[2:], macData)
	return out
}

// ComputeMAC computes a TCP-AO MAC over keyID, rNextKeyID, and macData with the
// supplied directional key. An empty algorithm selects HMAC-SHA1-96.
func ComputeMAC(algorithm Algorithm, key []byte, keyID, rNextKeyID KeyID, macData []byte) ([]byte, error) {
	alg, err := algorithm.normalized()
	if err != nil {
		return nil, err
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("%w: empty MAC key", ErrInvalidKey)
	}

	var newHash func() hash.Hash
	var size int
	switch alg {
	case HMACSHA1:
		newHash = sha1.New
		size = 12
	case HMACSHA256:
		newHash = sha256.New
		size = 16
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}

	mac := hmac.New(newHash, key)
	mac.Write(MACInput(keyID, rNextKeyID, macData))
	sum := mac.Sum(nil)
	return sum[:size], nil
}

// Sign computes a MAC using this key's outbound key material.
func (k Key) Sign(keyID, rNextKeyID KeyID, macData []byte) ([]byte, error) {
	return ComputeMAC(k.Algorithm, k.SendKey, keyID, rNextKeyID, macData)
}

func verifyMAC(algorithm Algorithm, key []byte, keyID, rNextKeyID KeyID, macData, got []byte) bool {
	want, err := ComputeMAC(algorithm, key, keyID, rNextKeyID, macData)
	if err != nil || len(got) != len(want) {
		return false
	}
	return hmac.Equal(want, got)
}
