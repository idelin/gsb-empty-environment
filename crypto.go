package netsec

import (
	"crypto/ed25519"
	"fmt"
)

// SignedDeviceList is an account state together with the account master
// signature over its canonical representation.
type SignedDeviceList struct {
	List      AccountState `json:"list"`
	Signature []byte       `json:"signature"`
}

// SignedList is an alias for SignedDeviceList.
type SignedList = SignedDeviceList

// SignDeviceList signs the complete canonical device list, including its
// account ID, version, previous digest, and every device entry.
func SignDeviceList(masterPrivateKey ed25519.PrivateKey, list AccountState) (*SignedDeviceList, error) {
	if len(masterPrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: master private key has %d bytes", ErrInvalidIdentityKey, len(masterPrivateKey))
	}
	canonical, err := list.CanonicalBytes()
	if err != nil {
		return nil, err
	}
	clone := list.Clone()
	signature := ed25519.Sign(masterPrivateKey, canonical)
	return &SignedDeviceList{
		List:      clone,
		Signature: append([]byte(nil), signature...),
	}, nil
}

// SignList is an alias for SignDeviceList.
func SignList(masterPrivateKey ed25519.PrivateKey, list AccountState) (*SignedDeviceList, error) {
	return SignDeviceList(masterPrivateKey, list)
}

// NewSignedDeviceList is an alias for SignDeviceList.
func NewSignedDeviceList(masterPrivateKey ed25519.PrivateKey, list AccountState) (*SignedDeviceList, error) {
	return SignDeviceList(masterPrivateKey, list)
}

// Verify checks the account master signature over the list.
func (s *SignedDeviceList) Verify(masterPublicKey ed25519.PublicKey) error {
	if s == nil {
		return fmt.Errorf("%w: nil signed list", ErrInvalidSignature)
	}
	if len(masterPublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: master public key has %d bytes", ErrInvalidIdentityKey, len(masterPublicKey))
	}
	canonical, err := s.List.CanonicalBytes()
	if err != nil {
		return err
	}
	if len(s.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature has %d bytes", ErrInvalidSignature, len(s.Signature))
	}
	if !ed25519.Verify(masterPublicKey, canonical, s.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

// VerifyForAccount verifies the signature and ensures the list belongs to the
// expected account.
func (s *SignedDeviceList) VerifyForAccount(accountID AccountID, masterPublicKey ed25519.PublicKey) error {
	if s == nil {
		return fmt.Errorf("%w: nil signed list", ErrInvalidSignature)
	}
	if s.List.AccountID != accountID {
		return ErrAccountMismatch
	}
	return s.Verify(masterPublicKey)
}

// VerifySignedDeviceList is a convenience function for nil-safe verification.
func VerifySignedDeviceList(s *SignedDeviceList, accountID AccountID, masterPublicKey ed25519.PublicKey) error {
	if s == nil {
		return fmt.Errorf("%w: nil signed list", ErrInvalidSignature)
	}
	return s.VerifyForAccount(accountID, masterPublicKey)
}

// Digest returns the canonical list digest.
func (s *SignedDeviceList) Digest() (Digest, error) {
	if s == nil {
		return Digest{}, fmt.Errorf("%w: nil signed list", ErrInvalidSignature)
	}
	return s.List.Digest()
}

// Clone returns a deep copy of the signed list.
func (s *SignedDeviceList) Clone() *SignedDeviceList {
	if s == nil {
		return nil
	}
	return &SignedDeviceList{
		List:      s.List.Clone(),
		Signature: append([]byte(nil), s.Signature...),
	}
}

// DeviceListDigest returns the digest of an account list. It exists as a
// package-level helper for callers that prefer not to use a method.
func DeviceListDigest(list AccountState) (Digest, error) {
	return list.Digest()
}
