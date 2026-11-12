package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
)

// ComputeProof builds the client delivery confirmation.
//
// The proof binds the proof key delivered at Claim time, the lease nonce and
// the exact payload bytes received, so a confirmation cannot be forged without
// holding the lease and cannot be replayed against different content.
//
//	proof = HMAC-SHA256(proofKey, nonce || SHA-256(payload))
func ComputeProof(proofKey, nonce, payload []byte) []byte {
	mac := hmac.New(sha256.New, proofKey)
	mac.Write(nonce)
	digest := sha256.Sum256(payload)
	mac.Write(digest[:])
	return mac.Sum(nil)
}
