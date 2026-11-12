package netsec

import (
	"crypto/sha256"
	"crypto/subtle"
)

// ConfirmationCodeHash computes
//
//	SHA256(SHA256(confirmationCode) || transactionId)
//
// where transactionId is the UTF-8/ASCII representation returned by
// InitiateAuthentication.
func ConfirmationCodeHash(confirmationCode, transactionID string) []byte {
	inner := sha256.Sum256([]byte(confirmationCode))
	return confirmationCodeHashFromInner(inner, transactionID)
}

func confirmationCodeHashFromInner(inner [sha256.Size]byte, transactionID string) []byte {
	input := make([]byte, 0, sha256.Size+len(transactionID))
	input = append(input, inner[:]...)
	input = append(input, transactionID...)
	outer := sha256.Sum256(input)
	hash := make([]byte, sha256.Size)
	copy(hash, outer[:])
	return hash
}

func confirmationHashesEqual(first, second []byte) bool {
	return len(first) == len(second) && subtle.ConstantTimeCompare(first, second) == 1
}
