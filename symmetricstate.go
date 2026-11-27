package noise

import (
	"crypto/sha256"
	"fmt"
)

// SymmetricState maintains the chaining key, handshake hash, and current
// CipherState used by a Noise handshake.
type SymmetricState struct {
	ck          [hashSize]byte
	h           [hashSize]byte
	cipher      *CipherState
	initialized bool
	destroyed   bool
}

// NewSymmetricState creates and initializes a SymmetricState.
func NewSymmetricState(protocolName string) (*SymmetricState, error) {
	s := &SymmetricState{}
	if err := s.InitializeSymmetric(protocolName); err != nil {
		return nil, err
	}
	return s, nil
}

// InitializeSymmetric initializes h and ck according to Noise revision 34. A
// protocol name at most 32 bytes long is zero padded; longer names are hashed.
func (s *SymmetricState) InitializeSymmetric(protocolName string) error {
	if s == nil || s.destroyed {
		return ErrStateDestroyed
	}
	if protocolName == "" {
		return fmt.Errorf("%w: empty protocol name", ErrInvalidConfig)
	}
	if len(protocolName) > 255 {
		return fmt.Errorf("%w: protocol name exceeds 255 bytes", ErrInvalidConfig)
	}
	var nameHash [hashSize]byte
	if len(protocolName) <= hashSize {
		copy(nameHash[:], protocolName)
	} else {
		nameHash = sha256.Sum256([]byte(protocolName))
	}
	s.ck = nameHash
	s.h = nameHash
	cipher, err := NewCipherState(nil)
	if err != nil {
		return err
	}
	if s.cipher != nil {
		s.cipher.Destroy()
	}
	s.cipher = cipher
	s.initialized = true
	s.destroyed = false
	return nil
}

// MixKey performs HKDF(ck, inputKeyMaterial, 2), replaces ck with the first
// output, installs the second output as the current cipher key, and resets the
// cipher nonce.
func (s *SymmetricState) MixKey(inputKeyMaterial []byte) error {
	if err := s.check(); err != nil {
		return err
	}
	first, second := hkdf2(s.ck[:], inputKeyMaterial)
	if err := s.cipher.InitializeKey(second[:]); err != nil {
		zero(first[:])
		zero(second[:])
		return err
	}
	s.ck = first
	zero(first[:])
	zero(second[:])
	return nil
}

// MixHash sets h = HASH(h || data).
func (s *SymmetricState) MixHash(data []byte) error {
	if err := s.check(); err != nil {
		return err
	}
	hasher := sha256.New()
	_, _ = hasher.Write(s.h[:])
	_, _ = hasher.Write(data)
	sum := hasher.Sum(nil)
	copy(s.h[:], sum)
	zero(sum)
	return nil
}

// EncryptAndHash encrypts plaintext with h as associated data and mixes the
// ciphertext into h. With no key installed, plaintext is copied and mixed
// directly.
func (s *SymmetricState) EncryptAndHash(plaintext []byte) ([]byte, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	ciphertext, err := s.cipher.EncryptWithAd(s.h[:], plaintext)
	if err != nil {
		return nil, err
	}
	if err := s.MixHash(ciphertext); err != nil {
		zero(ciphertext)
		return nil, err
	}
	return ciphertext, nil
}

// DecryptAndHash authenticates and decrypts ciphertext with h as associated
// data and, only on success, mixes the ciphertext into h. With no key
// installed, ciphertext is copied and mixed directly.
func (s *SymmetricState) DecryptAndHash(ciphertext []byte) ([]byte, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	plaintext, err := s.cipher.DecryptWithAd(s.h[:], ciphertext)
	if err != nil {
		return nil, err
	}
	if err := s.MixHash(ciphertext); err != nil {
		zero(plaintext)
		return nil, err
	}
	return plaintext, nil
}

// Split derives the two transport CipherStates from ck using HKDF(ck, empty,
// 2), then destroys all handshake symmetric state.
func (s *SymmetricState) Split() (first, second *CipherState, err error) {
	if err := s.check(); err != nil {
		return nil, nil, err
	}
	k1, k2 := hkdf2(s.ck[:], nil)
	first, err = NewCipherState(k1[:])
	zero(k1[:])
	if err != nil {
		zero(k2[:])
		s.Destroy()
		return nil, nil, err
	}
	second, err = NewCipherState(k2[:])
	zero(k2[:])
	if err != nil {
		first.Destroy()
		s.Destroy()
		return nil, nil, err
	}
	s.Destroy()
	return first, second, nil
}

// HandshakeHash returns a copy of the current transcript hash h.
func (s *SymmetricState) HandshakeHash() []byte {
	if s == nil || !s.initialized || s.destroyed {
		return nil
	}
	out := make([]byte, hashSize)
	copy(out, s.h[:])
	return out
}

// HasKey reports whether the current cipher has a key.
func (s *SymmetricState) HasKey() bool {
	return s != nil && s.cipher != nil && s.cipher.HasKey()
}

// CipherState returns the internal CipherState for testing and inspection.
// The returned pointer must not be retained across a handshake state change.
func (s *SymmetricState) CipherState() *CipherState {
	if s == nil || s.destroyed {
		return nil
	}
	return s.cipher
}

// Destroy clears the chaining key, transcript hash, and cipher state.
func (s *SymmetricState) Destroy() {
	if s == nil {
		return
	}
	zero(s.ck[:])
	zero(s.h[:])
	if s.cipher != nil {
		s.cipher.Destroy()
	}
	s.cipher = nil
	s.initialized = false
	s.destroyed = true
}

func (s *SymmetricState) check() error {
	if s == nil || s.destroyed {
		return ErrStateDestroyed
	}
	if !s.initialized || s.cipher == nil {
		return ErrInvalidConfig
	}
	return nil
}
