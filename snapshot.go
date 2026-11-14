package netsec

import (
	"context"
	"encoding/json"
)

// storedPart is the persisted form of a collected share fragment.
type storedPart struct {
	Index    int    `json:"index"`
	Fragment []byte `json:"fragment"`
}

// persistedState is the serializable representation of a State snapshot.
type persistedState struct {
	Version               int               `json:"version"`
	Phase                 string            `json:"phase"`
	Generation            uint64            `json:"generation"`
	MaxObservedGeneration uint64            `json:"max_observed_generation"`
	Seal                  SealConfig        `json:"seal"`
	SealDigest            []byte            `json:"seal_digest"`
	Nonce                 []byte            `json:"nonce"`
	KeyLen                int               `json:"key_len"`
	KeyCommitment         []byte            `json:"key_commitment"`
	HolderIndex           map[string]int    `json:"holder_index"`
	HolderKeys            map[string][]byte `json:"holder_keys"`
	Received              []storedPart      `json:"received"`
	HolderSeen            map[string]bool   `json:"holder_seen"`
	UnsealKey             []byte            `json:"unseal_key,omitempty"`
	AuditSeq              uint64            `json:"audit_seq"`
	Events                []AuditEvent      `json:"events"`
}

// snapshot is the authenticated envelope written to durable storage.
type snapshot struct {
	Data []byte `json:"data"`
	Tag  []byte `json:"tag"`
}

// ExportSnapshot serializes and authenticates the current state. The output is
// safe to write to durable storage and can only be re-imported by an instance
// configured with the same StateKey.
func (s *State) ExportSnapshot() ([]byte, error) {
	s.mu.Lock()
	if len(s.stateKey) == 0 {
		s.mu.Unlock()
		return nil, ErrNoStateKey
	}
	if s.phase == phaseUninitialized {
		s.mu.Unlock()
		return nil, ErrNotInitialized
	}

	received := make([]storedPart, 0, len(s.received))
	for index, part := range s.received {
		received = append(received, storedPart{Index: index, Fragment: append([]byte(nil), part.fragment...)})
	}

	persisted := persistedState{
		Version:               snapshotVersion,
		Phase:                 s.phase,
		Generation:            s.generation,
		MaxObservedGeneration: s.maxObservedGeneration,
		Seal:                  cloneSealConfig(s.seal),
		SealDigest:            append([]byte(nil), s.sealDigest...),
		Nonce:                 append([]byte(nil), s.nonce...),
		KeyLen:                s.keyLen,
		KeyCommitment:         append([]byte(nil), s.keyCommitment...),
		HolderIndex:           cloneHolderIndex(s.holderIndex),
		HolderKeys:            cloneHolderKeys(s.holderKeys),
		Received:              received,
		HolderSeen:            cloneHolderSeen(s.holderSeen),
		UnsealKey:             append([]byte(nil), s.unsealKey...),
		AuditSeq:              s.auditSeq,
		Events:                s.AuditEventsLocked(),
	}
	s.mu.Unlock()

	data, err := json.Marshal(persisted)
	if err != nil {
		return nil, err
	}
	envelope := snapshot{
		Data: data,
		Tag:  hmacSHA256(s.stateKey, append([]byte(snapshotDomain), data...)),
	}
	return json.Marshal(envelope)
}

// ImportSnapshot restores an authenticated snapshot, simulating a service
// restart. It rejects tampered envelopes and any snapshot whose generation is
// behind the generation this instance has already observed.
func (s *State) ImportSnapshot(ctx context.Context, raw []byte) error {
	s.mu.Lock()
	if len(s.stateKey) == 0 {
		s.mu.Unlock()
		return ErrNoStateKey
	}

	var envelope snapshot
	if err := json.Unmarshal(raw, &envelope); err != nil {
		s.mu.Unlock()
		return ErrSnapshotIntegrity
	}
	expectedTag := hmacSHA256(s.stateKey, append([]byte(snapshotDomain), envelope.Data...))
	if !validMAC(expectedTag, envelope.Tag) {
		s.mu.Unlock()
		return ErrSnapshotIntegrity
	}

	var persisted persistedState
	if err := json.Unmarshal(envelope.Data, &persisted); err != nil {
		s.mu.Unlock()
		return ErrSnapshotIntegrity
	}
	if persisted.Version != snapshotVersion || persisted.Phase == "" {
		s.mu.Unlock()
		return ErrSnapshotIntegrity
	}
	if persisted.MaxObservedGeneration < s.maxObservedGeneration ||
		persisted.Generation < s.maxObservedGeneration {
		events := []AuditEvent{s.emitLocked(EventShareRejected, "", 0, ErrGenerationRollback)}
		s.mu.Unlock()
		s.deliver(ctx, events)
		return ErrGenerationRollback
	}

	received := make(map[int]sharePart, len(persisted.Received))
	for _, part := range persisted.Received {
		if part.Index < 1 || part.Index > maxHolders || len(part.Fragment) != persisted.KeyLen {
			s.mu.Unlock()
			return ErrSnapshotIntegrity
		}
		received[part.Index] = sharePart{index: byte(part.Index), fragment: append([]byte(nil), part.Fragment...)}
	}

	s.phase = persisted.Phase
	s.generation = persisted.Generation
	s.maxObservedGeneration = persisted.MaxObservedGeneration
	s.seal = cloneSealConfig(persisted.Seal)
	s.sealDigest = append([]byte(nil), persisted.SealDigest...)
	s.nonce = append([]byte(nil), persisted.Nonce...)
	s.keyLen = persisted.KeyLen
	s.keyCommitment = append([]byte(nil), persisted.KeyCommitment...)
	s.holderIndex = cloneHolderIndex(persisted.HolderIndex)
	s.holderKeys = cloneHolderKeys(persisted.HolderKeys)
	s.received = received
	s.holderSeen = cloneHolderSeen(persisted.HolderSeen)
	s.unsealKey = append([]byte(nil), persisted.UnsealKey...)
	s.auditSeq = persisted.AuditSeq
	s.events = append([]AuditEvent(nil), persisted.Events...)

	events := []AuditEvent{s.emitLocked(EventImported, "", len(s.received), nil)}
	s.mu.Unlock()
	s.deliver(ctx, events)
	return nil
}
