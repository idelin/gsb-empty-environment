package netsec

import (
	"math"
	"sync"
)

// SenderChain is a small helper for producing ciphertexts compatible with a
// ReceiveChain. It uses the same symmetric-key ratchet KDF and AES-256-GCM
// construction as ReceiveChain.Decrypt.
type SenderChain struct {
	mu sync.Mutex

	chainKey     []byte
	nextSequence uint64
}

// NewSenderChain creates a sender chain at sequence number zero.
func NewSenderChain(chainKey []byte) (*SenderChain, error) {
	if err := ValidateChainKey(chainKey); err != nil {
		return nil, err
	}
	return &SenderChain{
		chainKey: cloneBytes(chainKey),
	}, nil
}

// MustNewSenderChain is like NewSenderChain but panics on invalid input.
func MustNewSenderChain(chainKey []byte) *SenderChain {
	chain, err := NewSenderChain(chainKey)
	if err != nil {
		panic(err)
	}
	return chain
}

// NewSendingChain is an alias for NewSenderChain.
func NewSendingChain(chainKey []byte) (*SenderChain, error) {
	return NewSenderChain(chainKey)
}

// SendingChain is an alias for SenderChain.
type SendingChain = SenderChain

// NextSequence returns the sequence number that Seal will use next.
func (s *SenderChain) NextSequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextSequence
}

// MessageKeyAt derives the message key for a specific sequence number
// without changing sender state.
func (s *SenderChain) MessageKeyAt(sequence any) ([]byte, error) {
	normalized, err := NormalizeSequence(sequence)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return deriveMessageKeyAt(s.chainKey, normalized)
}

// SealAt encrypts plaintext for a specific sequence number without changing
// sender state. This is useful for exercising out-of-order delivery.
func (s *SenderChain) SealAt(
	sequence any,
	nonce, plaintext, additionalData []byte,
) ([]byte, error) {
	normalized, err := NormalizeSequence(sequence)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	messageKey, err := deriveMessageKeyAt(s.chainKey, normalized)
	if err != nil {
		return nil, err
	}
	defer clearBytes(messageKey)
	return SealAESGCM(messageKey, nonce, plaintext, additionalData)
}

// EncryptAt is an alias for SealAt.
func (s *SenderChain) EncryptAt(
	sequence any,
	nonce, plaintext, additionalData []byte,
) ([]byte, error) {
	return s.SealAt(sequence, nonce, plaintext, additionalData)
}

// Seal encrypts the next message in the chain and advances the sender.
func (s *SenderChain) Seal(nonce, plaintext, additionalData []byte) (uint64, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.nextSequence == math.MaxUint64 {
		return 0, nil, ErrSequenceOverflow
	}

	messageKey, err := deriveMessageKeyAt(s.chainKey, s.nextSequence)
	if err != nil {
		return 0, nil, err
	}
	defer clearBytes(messageKey)
	ciphertext, err := SealAESGCM(messageKey, nonce, plaintext, additionalData)
	if err != nil {
		return 0, nil, err
	}

	nextChainKey := DeriveNextChainKey(s.chainKey)
	clearBytes(s.chainKey)
	s.chainKey = nextChainKey
	sequence := s.nextSequence
	s.nextSequence++
	return sequence, ciphertext, nil
}

// Encrypt is an alias for Seal.
func (s *SenderChain) Encrypt(nonce, plaintext, additionalData []byte) (uint64, []byte, error) {
	return s.Seal(nonce, plaintext, additionalData)
}

func deriveMessageKeyAt(chainKey []byte, sequence uint64) ([]byte, error) {
	deriveChainKey := cloneBytes(chainKey)
	for next := uint64(0); next < sequence; next++ {
		_, nextChainKey := StepChainKey(deriveChainKey)
		clearBytes(deriveChainKey)
		deriveChainKey = nextChainKey
	}
	messageKey, nextChainKey := StepChainKey(deriveChainKey)
	clearBytes(deriveChainKey)
	clearBytes(nextChainKey)
	return messageKey, nil
}
