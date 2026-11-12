package netsec

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
)

// SignatureVerifier verifies an authentication-response signature over the
// exact payload passed to Verify. The guard builds that payload from the
// transactionId and the server challenge issued for the session.
type SignatureVerifier interface {
	Verify(payload, signature []byte) error
}

// VerifierFunc adapts a function to SignatureVerifier.
type VerifierFunc func(payload, signature []byte) error

// Verify calls f. A nil f rejects the signature.
func (verifier VerifierFunc) Verify(payload, signature []byte) error {
	if verifier == nil {
		return ErrSignatureInvalid
	}
	return verifier(payload, signature)
}

// AuthenticationPayload is the canonical bytes that an eUICC authentication
// response signature must cover. It is the transactionId followed by the
// server challenge.
func AuthenticationPayload(transactionID string, serverChallenge []byte) []byte {
	payload := make([]byte, 0, len(transactionID)+len(serverChallenge))
	payload = append(payload, transactionID...)
	payload = append(payload, serverChallenge...)
	return payload
}

// HMACVerifier verifies HMAC-SHA256 signatures. It is useful for tests and
// for deployments that use a shared symmetric key instead of an eUICC public
// key.
type HMACVerifier struct {
	key []byte
}

// NewHMACVerifier returns an HMAC-SHA256 verifier. The key is copied.
func NewHMACVerifier(key []byte) *HMACVerifier {
	return &HMACVerifier{key: append([]byte(nil), key...)}
}

// Verify checks signature against HMAC-SHA256(key, payload).
func (verifier *HMACVerifier) Verify(payload, signature []byte) error {
	if verifier == nil {
		return ErrSignatureInvalid
	}
	mac := hmac.New(sha256.New, verifier.key)
	_, _ = mac.Write(payload)
	expected := mac.Sum(nil)
	if len(signature) != len(expected) ||
		subtle.ConstantTimeCompare(expected, signature) != 1 {
		return ErrSignatureInvalid
	}
	return nil
}

// SignHMAC creates a signature compatible with NewHMACVerifier.
func SignHMAC(key, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

// ECDSAVerifier verifies ASN.1 ECDSA signatures over SHA-256(payload).
type ECDSAVerifier struct {
	publicKey ecdsa.PublicKey
}

// NewECDSAVerifier returns a verifier for publicKey. It returns nil when the
// key is missing or is not a valid point on its curve.
func NewECDSAVerifier(publicKey *ecdsa.PublicKey) *ECDSAVerifier {
	if publicKey == nil || publicKey.Curve == nil ||
		publicKey.X == nil || publicKey.Y == nil ||
		!publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
		return nil
	}
	copyOfKey := *publicKey
	return &ECDSAVerifier{publicKey: copyOfKey}
}

// Verify checks an ASN.1 ECDSA signature.
func (verifier *ECDSAVerifier) Verify(payload, signature []byte) error {
	if verifier == nil {
		return ErrSignatureInvalid
	}
	digest := sha256.Sum256(payload)
	if !ecdsa.VerifyASN1(&verifier.publicKey, digest[:], signature) {
		return ErrSignatureInvalid
	}
	return nil
}
