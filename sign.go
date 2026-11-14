package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// HMACSize is the size in bytes of the SHA-256 HMAC used by the helpers in
// this package.
const HMACSize = sha256.Size

// HMACSign returns the SHA-256 HMAC of canonical bytes. Callers should pass
// the exact output of Canonicalize, CanonicalizeQuery, or another canonical
// source; HMACSign itself does not reinterpret the bytes.
func HMACSign(secret, canonical []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(canonical)
	return mac.Sum(nil)
}

// HMACVerify compares a SHA-256 HMAC in constant time.
func HMACVerify(secret, canonical, signature []byte) bool {
	expected := HMACSign(secret, canonical)
	return hmac.Equal(expected, signature)
}

// HMACSigner signs canonical data with a fixed secret.
type HMACSigner struct {
	secret []byte
}

// NewHMACSigner returns a signer with a private copy of secret.
func NewHMACSigner(secret []byte) *HMACSigner {
	copied := append([]byte(nil), secret...)
	return &HMACSigner{secret: copied}
}

// Sign canonicalizes structured input, then signs the canonical bytes. String
// and []byte input must already be canonical; an ambiguous or re-encoded
// representation is rejected rather than silently normalized.
func (signer *HMACSigner) Sign(input any) ([]byte, error) {
	canonical, err := canonicalForVerification(input)
	if err != nil {
		return nil, err
	}
	return signer.SignCanonical(canonical), nil
}

// Verify recomputes the canonical bytes and compares the signature in
// constant time. String and []byte input must already be canonical.
func (signer *HMACSigner) Verify(input any, signature []byte) (bool, error) {
	canonical, err := canonicalForVerification(input)
	if err != nil {
		return false, err
	}
	return signer.VerifyCanonical(canonical, signature), nil
}

// SignCanonical signs bytes that are already canonical.
func (signer *HMACSigner) SignCanonical(canonical []byte) []byte {
	return HMACSign(signer.secret, canonical)
}

// VerifyCanonical verifies a signature over bytes that are already canonical.
func (signer *HMACSigner) VerifyCanonical(canonical, signature []byte) bool {
	return HMACVerify(signer.secret, canonical, signature)
}

// SignQuery canonicalizes a raw query exactly once and signs the result.
func (signer *HMACSigner) SignQuery(raw string) ([]byte, error) {
	canonical, err := CanonicalizeQuery(raw)
	if err != nil {
		return nil, err
	}
	return signer.SignCanonical(canonical), nil
}

// VerifyQuery canonicalizes a raw query exactly once and verifies the result.
// Use VerifyCanonicalQuery when the incoming bytes must already be canonical.
func (signer *HMACSigner) VerifyQuery(raw string, signature []byte) (bool, error) {
	canonical, err := CanonicalizeQuery(raw)
	if err != nil {
		return false, err
	}
	return signer.VerifyCanonical(canonical, signature), nil
}

// VerifyCanonicalQuery rejects any non-canonical raw query before verifying.
// This is the strict path: changing the spelling of a valid payload (for
// example %2F to %2f or to "/") makes verification fail.
func (signer *HMACSigner) VerifyCanonicalQuery(raw string, signature []byte) (bool, error) {
	if err := ValidateCanonicalQuery(raw); err != nil {
		return false, err
	}
	return signer.VerifyCanonical([]byte(raw), signature), nil
}

// Sign canonicalizes input and signs it with secret.
func Sign(secret []byte, input any) ([]byte, error) {
	return NewHMACSigner(secret).Sign(input)
}

// Verify canonicalizes input and verifies signature with secret.
func Verify(secret []byte, input any, signature []byte) (bool, error) {
	return NewHMACSigner(secret).Verify(input, signature)
}

// VerifySignature is Verify with any canonicalization or encoding error
// reported as a failed verification.
func VerifySignature(secret []byte, input any, signature []byte) bool {
	ok, err := Verify(secret, input, signature)
	return err == nil && ok
}

// SignHex is Sign returning a lowercase hexadecimal signature.
func SignHex(secret []byte, input any) (string, error) {
	signature, err := Sign(secret, input)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(signature), nil
}

// VerifyHex is Verify accepting a hexadecimal signature.
func VerifyHex(secret []byte, input any, signature string) (bool, error) {
	decoded, err := hex.DecodeString(signature)
	if err != nil {
		return false, fmt.Errorf("netsec: invalid hexadecimal signature: %w", err)
	}
	return Verify(secret, input, decoded)
}

func canonicalForVerification(input any) ([]byte, error) {
	return CanonicalizeStrict(input)
}
