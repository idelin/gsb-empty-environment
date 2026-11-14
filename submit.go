package netsec

import (
	"context"
	"crypto/sha256"
)

// SubmitShare validates and records one holder share. It returns true when the
// threshold is reached and the state becomes unsealed. Replay, stale
// generation, duplicate and out-of-order submissions are rejected with a
// sentinel error and an audit event.
func (s *State) SubmitShare(ctx context.Context, share Share) (bool, error) {
	s.mu.Lock()
	var events []AuditEvent
	reject := func(err error) (bool, error) {
		events = append(events, s.emitLocked(EventShareRejected, share.HolderID, len(s.received), err))
		s.mu.Unlock()
		s.deliver(ctx, events)
		return false, err
	}

	if s.phase == phaseUninitialized {
		return reject(ErrNotInitialized)
	}
	// Generation binding: a replayed share from an older generation, or one
	// minted for a generation that does not exist yet, can never be accepted.
	if share.Generation < s.generation {
		return reject(ErrStaleGeneration)
	}
	if share.Generation > s.generation {
		return reject(ErrFutureGeneration)
	}
	// Seal configuration binding.
	if share.SealID != s.seal.SealID {
		return reject(ErrSealConfigMismatch)
	}
	// Nonce binding: every sealing rotates the nonce.
	if !validMAC(share.Nonce, s.nonce) {
		return reject(ErrNonceMismatch)
	}
	// Holder binding.
	index, ok := s.holderIndex[share.HolderID]
	if !ok {
		return reject(ErrUnknownHolder)
	}
	if share.Index != index || share.Index < 1 || share.Index > maxHolders {
		return reject(ErrHolderIndexMismatch)
	}
	if len(share.Fragment) != s.keyLen || len(share.Tag) != sha256.Size {
		return reject(ErrInvalidTag)
	}
	expectedTag := hmacSHA256(s.holderKeys[share.HolderID], shareBindingMessage(share))
	if !validMAC(expectedTag, share.Tag) {
		return reject(ErrInvalidTag)
	}
	// One-time unseal: once unsealed, every further submission is refused.
	if s.phase == phaseUnsealed {
		return reject(ErrAlreadyUnsealed)
	}
	// Duplicate/replay rejection: a holder may contribute only once.
	if s.holderSeen[share.HolderID] {
		return reject(ErrDuplicateShare)
	}

	s.received[index] = sharePart{index: byte(index), fragment: append([]byte(nil), share.Fragment...)}
	s.holderSeen[share.HolderID] = true

	if len(s.received) < s.seal.Threshold {
		events = append(events, s.emitLocked(EventShareAccepted, share.HolderID, len(s.received), nil))
		s.mu.Unlock()
		s.deliver(ctx, events)
		return false, nil
	}

	if err := s.unsealLocked(); err != nil {
		events = append(events, s.emitLocked(EventShareRejected, share.HolderID, len(s.received), err))
		s.mu.Unlock()
		s.deliver(ctx, events)
		return false, err
	}
	events = append(events, s.emitLocked(EventUnsealed, share.HolderID, len(s.received), nil))
	s.mu.Unlock()
	s.deliver(ctx, events)
	return true, nil
}

// unsealLocked reconstructs the secret from the collected shares and verifies
// it against the sealing key commitment before flipping the phase to unsealed.
func (s *State) unsealLocked() error {
	parts := make([]sharePart, 0, len(s.received))
	for _, part := range s.received {
		parts = append(parts, part)
	}
	reconstructed := combineSecret(parts, s.keyLen)
	commitment := sha256.Sum256(reconstructed)
	if !validMAC(commitment[:], s.keyCommitment) {
		return ErrInvalidTag
	}
	s.phase = phaseUnsealed
	s.unsealKey = reconstructed
	return nil
}

// UnsealKey returns the reconstructed key after a successful unseal.
func (s *State) UnsealKey() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.phase != phaseUnsealed || s.unsealKey == nil {
		return nil, ErrNotUnsealed
	}
	return append([]byte(nil), s.unsealKey...), nil
}
