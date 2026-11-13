package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

// CommitmentKeyLabel is the fixed domain-separation label used to derive the
// HMAC key for record commitments. Changing this label invalidates existing
// records.
const CommitmentKeyLabel = "example.org/netsec/q0063/key-commit/v1"

func deriveCommitmentKey(key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(CommitmentKeyLabel))
	return mac.Sum(nil)
}

// computeCommitment streams the committed input directly into HMAC to avoid
// duplicating a potentially large ciphertext in memory:
//
//	version || u32(len(keyID)) || keyID ||
//	u32(len(nonce)) || nonce ||
//	u32(len(aad)) || aad ||
//	u32(len(ciphertext)) || ciphertext
func computeCommitment(commitmentKey []byte, version uint8, keyID string, nonce, aad, ciphertext []byte) [CommitmentSize]byte {
	mac := hmac.New(sha256.New, commitmentKey)
	mac.Write([]byte{version})
	writeLengthPrefixed(mac, []byte(keyID))
	writeLengthPrefixed(mac, nonce)
	writeLengthPrefixed(mac, aad)
	writeLengthPrefixed(mac, ciphertext)
	sum := mac.Sum(nil)

	var commitment [CommitmentSize]byte
	copy(commitment[:], sum[:CommitmentSize])
	clear(sum)
	return commitment
}

func writeLengthPrefixed(dst hash.Hash, value []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	dst.Write(length[:])
	dst.Write(value)
}
