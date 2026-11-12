package netsec

import (
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// ComputeMAC calculates the SOPS file MAC over the values in document order.
// The caller must include every value covered by the file's mac_only_encrypted
// setting and must not reorder the values.
func ComputeMAC(values []string) []byte {
	hash := sha512.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(value))
	}
	sum := hash.Sum(nil)
	return sum
}

// ComputeMACHex calculates the SOPS file MAC and returns its hexadecimal form.
func ComputeMACHex(values []string) string {
	return hex.EncodeToString(ComputeMAC(values))
}

// VerifyMAC recomputes the MAC over all decrypted values and compares the
// digest with the decrypted metadata MAC in constant time.
func VerifyMAC(decryptedMAC string, values []string) error {
	expected, err := hex.DecodeString(strings.TrimSpace(decryptedMAC))
	if err != nil || len(expected) != sha512.Size {
		return fmt.Errorf("%w: decrypted mac is not a SHA-512 digest", ErrMACMismatch)
	}
	actual := ComputeMAC(values)
	if subtle.ConstantTimeCompare(expected, actual) != 1 {
		return ErrMACMismatch
	}
	return nil
}

// VerifyMACBytes is the []byte equivalent of VerifyMAC.
func VerifyMACBytes(decryptedMAC []byte, values []string) error {
	actual := ComputeMAC(values)
	if subtle.ConstantTimeCompare(decryptedMAC, actual) != 1 {
		return ErrMACMismatch
	}
	return nil
}

// VerifyMAC is a convenience method for validators.
func (v *Validator) VerifyMAC(decryptedMAC string, values []string) error {
	return VerifyMAC(decryptedMAC, values)
}
