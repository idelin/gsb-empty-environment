package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
)

// The one-byte inputs are the domain separators used by the Double Ratchet
// symmetric-key ratchet: 0x01 derives a message key, 0x02 derives the next
// chain key.
var (
	messageKeyInfo = []byte{0x01}
	chainKeyInfo   = []byte{0x02}
)

// DeriveMessageKey returns the AEAD message key for the supplied chain key.
// The chain key itself is not modified.
func DeriveMessageKey(chainKey []byte) []byte {
	return chainKDF(chainKey, messageKeyInfo)
}

// DeriveNextChainKey returns the chain key for the next message.
func DeriveNextChainKey(chainKey []byte) []byte {
	return chainKDF(chainKey, chainKeyInfo)
}

// StepChainKey returns both the current message key and the next chain key.
// It is equivalent to calling DeriveMessageKey and DeriveNextChainKey
// separately, but keeps the two outputs together.
func StepChainKey(chainKey []byte) (messageKey, nextChainKey []byte) {
	return DeriveMessageKey(chainKey), DeriveNextChainKey(chainKey)
}

// ValidateChainKey reports whether chainKey has the expected size.
func ValidateChainKey(chainKey []byte) error {
	if len(chainKey) != ChainKeySize {
		return ErrInvalidChainKey
	}
	return nil
}

func chainKDF(chainKey, info []byte) []byte {
	mac := hmac.New(sha256.New, chainKey)
	_, _ = mac.Write(info)
	return mac.Sum(nil)
}
