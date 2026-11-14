package netsec

import (
	"context"
	"crypto/sha256"
	"io"
)

// Initialize performs the first sealing. It returns the per-holder shares to
// distribute.
func (s *State) Initialize(ctx context.Context, cfg SealConfig) (*SealedState, error) {
	if err := validateSealConfig(cfg); err != nil {
		return nil, err
	}
	s.mu.Lock()
	var events []AuditEvent
	if s.phase != phaseUninitialized {
		events = append(events, s.emitLocked(EventShareRejected, "", 0, ErrAlreadyInitialized))
		s.mu.Unlock()
		s.deliver(ctx, events)
		return nil, ErrAlreadyInitialized
	}
	sealed, err := s.sealLocked(cfg, 1)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.maxObservedGeneration = 1
	events = append(events, s.emitLocked(EventInitialized, "", 0, nil))
	s.mu.Unlock()
	s.deliver(ctx, events)
	return sealed, nil
}

// Rotate starts a fresh sealing with a new generation and nonce. All shares
// from previous generations become invalid and the state returns to sealed.
func (s *State) Rotate(ctx context.Context, cfg SealConfig) (*SealedState, error) {
	if err := validateSealConfig(cfg); err != nil {
		return nil, err
	}
	s.mu.Lock()
	var events []AuditEvent
	if s.phase == phaseUninitialized {
		events = append(events, s.emitLocked(EventShareRejected, "", 0, ErrNotInitialized))
		s.mu.Unlock()
		s.deliver(ctx, events)
		return nil, ErrNotInitialized
	}
	next := s.generation + 1
	sealed, err := s.sealLocked(cfg, next)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if next > s.maxObservedGeneration {
		s.maxObservedGeneration = next
	}
	events = append(events, s.emitLocked(EventRotated, "", 0, nil))
	s.mu.Unlock()
	s.deliver(ctx, events)
	return sealed, nil
}

// sealLocked creates a new sealing context for generation, generates a fresh
// nonce and unseal key, splits the key into holder shares and resets all
// per-generation bookkeeping.
func (s *State) sealLocked(cfg SealConfig, generation uint64) (*SealedState, error) {
	nonce := make([]byte, nonceLength)
	if _, err := io.ReadFull(s.rng, nonce); err != nil {
		return nil, err
	}
	secret := make([]byte, unsealKeyLength)
	if _, err := io.ReadFull(s.rng, secret); err != nil {
		return nil, err
	}
	fragments, err := splitSecret(secret, cfg.Threshold, len(cfg.Holders), s.rng)
	if err != nil {
		return nil, err
	}

	holderIndex := make(map[string]int, len(cfg.Holders))
	holderKeys := make(map[string][]byte, len(cfg.Holders))
	shares := make(map[string]Share, len(cfg.Holders))
	for i, holder := range cfg.Holders {
		index := i + 1
		holderIndex[holder.ID] = index
		holderKeys[holder.ID] = append([]byte(nil), holder.Key...)
		share := Share{
			Generation: generation,
			SealID:     cfg.SealID,
			Nonce:      append([]byte(nil), nonce...),
			HolderID:   holder.ID,
			Index:      index,
			Fragment:   append([]byte(nil), fragments[i]...),
		}
		share.Tag = hmacSHA256(holder.Key, shareBindingMessage(share))
		shares[holder.ID] = share
	}

	commitment := sha256.Sum256(secret)
	s.phase = phaseSealed
	s.generation = generation
	s.seal = cloneSealConfig(cfg)
	s.sealDigest = sealConfigDigest(cfg)
	s.nonce = nonce
	s.keyLen = len(secret)
	s.keyCommitment = commitment[:]
	s.holderIndex = holderIndex
	s.holderKeys = holderKeys
	s.received = make(map[int]sharePart)
	s.holderSeen = make(map[string]bool)
	s.unsealKey = nil

	return &SealedState{
		Generation: generation,
		Seal:       cloneSealConfig(cfg),
		Nonce:      append([]byte(nil), nonce...),
		Shares:     shares,
	}, nil
}

// validateSealConfig rejects malformed seal configurations.
func validateSealConfig(cfg SealConfig) error {
	if cfg.SealID == "" {
		return ErrInvalidConfig
	}
	n := len(cfg.Holders)
	if n == 0 || n > maxHolders || cfg.Threshold < 1 || cfg.Threshold > n {
		return ErrInvalidConfig
	}
	seen := make(map[string]bool, n)
	for _, holder := range cfg.Holders {
		if holder.ID == "" || seen[holder.ID] {
			return ErrInvalidConfig
		}
		if len(holder.Key) < minHolderKeyLength {
			return ErrInvalidConfig
		}
		seen[holder.ID] = true
	}
	return nil
}

// cloneSealConfig returns a deep copy so callers cannot mutate internal state.
func cloneSealConfig(cfg SealConfig) SealConfig {
	holders := make([]Holder, len(cfg.Holders))
	for i, holder := range cfg.Holders {
		holders[i] = Holder{
			ID:  holder.ID,
			Key: append([]byte(nil), holder.Key...),
		}
	}
	return SealConfig{
		SealID:    cfg.SealID,
		Threshold: cfg.Threshold,
		Holders:   holders,
	}
}
