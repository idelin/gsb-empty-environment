package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrNilStore is returned when Rotate is called without a mapping store.
	ErrNilStore = errors.New("netsec: mapping store is required")
	// ErrSameGeneration is returned when a batch does not change generation.
	ErrSameGeneration = errors.New("netsec: source and target generations must differ")
	// ErrInvalidMapping is returned when a mapping row is not a pair of tokens.
	ErrInvalidMapping = errors.New("netsec: invalid mapping")
	// ErrMappingConflict is returned when an old token is already mapped to a
	// different new token.
	ErrMappingConflict = errors.New("netsec: conflicting pseudonym mapping")
	// ErrUnresolvedKey is returned when an event's primary key cannot be
	// resolved to the target generation.
	ErrUnresolvedKey = errors.New("netsec: event key cannot be resolved")
)

// Mapping is a table of old token to new token. Rows contain only opaque
// pseudonyms; raw tenant values are never stored here.
type Mapping struct {
	Entries map[Token]Token
}

// NewMapping returns an empty mapping.
func NewMapping() Mapping {
	return Mapping{Entries: make(map[Token]Token)}
}

// Len returns the number of rows in the mapping.
func (m Mapping) Len() int {
	return len(m.Entries)
}

// Clone returns a deep copy of the mapping.
func (m Mapping) Clone() Mapping {
	return Mapping{Entries: cloneEntries(m.Entries)}
}

// Validate checks that every source and destination is a well-formed token.
func (m Mapping) Validate() error {
	for from, to := range m.Entries {
		if _, _, err := ParseToken(string(from)); err != nil {
			return fmt.Errorf("%w: source token", ErrInvalidMapping)
		}
		if _, _, err := ParseToken(string(to)); err != nil {
			return fmt.Errorf("%w: destination token", ErrInvalidMapping)
		}
	}
	return nil
}

func cloneEntries(entries map[Token]Token) map[Token]Token {
	copied := make(map[Token]Token, len(entries))
	for from, to := range entries {
		copied[from] = to
	}
	return copied
}

// MappingStore loads and atomically publishes token mappings.
type MappingStore interface {
	// Load returns a consistent snapshot of the current mapping.
	Load(context.Context) (Mapping, error)
	// Publish replaces the mapping atomically. If an error is returned, the
	// previously published mapping must remain visible.
	Publish(context.Context, Mapping) error
}

// MemoryStore is an in-process MappingStore. Publish swaps a fully built
// snapshot under a lock, so readers never observe a partially written batch.
type MemoryStore struct {
	mu      sync.RWMutex
	entries map[Token]Token
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: make(map[Token]Token)}
}

// Load returns a copy of the current mapping.
func (s *MemoryStore) Load(ctx context.Context) (Mapping, error) {
	if err := ctx.Err(); err != nil {
		return Mapping{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Mapping{Entries: cloneEntries(s.entries)}, nil
}

// Publish validates and atomically replaces the mapping.
func (s *MemoryStore) Publish(ctx context.Context, mapping Mapping) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := mapping.Validate(); err != nil {
		return err
	}
	staged := cloneEntries(mapping.Entries)
	s.mu.Lock()
	s.entries = staged
	s.mu.Unlock()
	return nil
}

// Subject is an identity that needs a new-generation pseudonym. Value is the
// raw secret and is used only for in-memory HMAC computation. Rotate never
// places it in a mapping, result, token, or error.
type Subject struct {
	Tenant    TenantID
	FieldType FieldType
	Value     string
}

// Event is a stored event key together with references to other event keys.
// Both keys and references are old-generation tokens before rotation.
type Event struct {
	ID         string
	Key        Token
	References []Token
}

// Reference is a rewritten event reference. Original is always the source
// token. When Missing is false, Token is the resolved target-generation
// token. When Missing is true, Token is empty and the reference remains
// explicitly unresolved rather than being dropped.
type Reference struct {
	Original Token
	Token    Token
	Missing  bool
}

// RewrittenEvent is an event after key rotation.
type RewrittenEvent struct {
	ID         string
	Key        Token
	References []Reference
}

// Batch describes one key-generation rotation. Subjects supply the raw values
// needed to derive new tokens; Events describe the stored keys and references
// that must be rewritten in the same publication.
type Batch struct {
	From     Generation
	To       Generation
	Subjects []Subject
	Events   []Event
}

// Result is the validated, published result of a rotation. Mapping is the
// complete mapping table visible after publication.
type Result struct {
	From    Generation
	To      Generation
	Events  []RewrittenEvent
	Mapping Mapping
}

// Rotate validates an entire batch, rewrites event keys and references, and
// then publishes one combined mapping. If validation or publication fails, no
// mapping is published. References that cannot be resolved to the target
// generation are retained in the result with Missing set to true.
func (p *Pseudonymizer) Rotate(ctx context.Context, store MappingStore, batch Batch) (Result, error) {
	if p == nil || p.ring == nil {
		return Result{}, ErrNilKeyring
	}
	if store == nil {
		return Result{}, ErrNilStore
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if batch.From == batch.To {
		return Result{}, ErrSameGeneration
	}

	current, err := store.Load(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("load mapping: %w", err)
	}
	if err := current.Validate(); err != nil {
		return Result{}, err
	}
	entries := cloneEntries(current.Entries)

	for i, subject := range batch.Subjects {
		oldToken, err := p.Pseudonymize(subject.Tenant, subject.FieldType, subject.Value, batch.From)
		if err != nil {
			return Result{}, fmt.Errorf("subject %d: %w", i, err)
		}
		newToken, err := p.Pseudonymize(subject.Tenant, subject.FieldType, subject.Value, batch.To)
		if err != nil {
			return Result{}, fmt.Errorf("subject %d: %w", i, err)
		}
		if previous, ok := entries[oldToken]; ok && previous != newToken {
			return Result{}, fmt.Errorf("%w at subject %d", ErrMappingConflict, i)
		}
		entries[oldToken] = newToken
	}

	rewritten := make([]RewrittenEvent, len(batch.Events))
	for i, event := range batch.Events {
		if _, _, err := ParseToken(string(event.Key)); err != nil {
			return Result{}, fmt.Errorf("event %d key: %w", i, ErrInvalidToken)
		}
		key, ok := resolveToken(event.Key, batch.To, entries)
		if !ok {
			return Result{}, fmt.Errorf("%w at event %d", ErrUnresolvedKey, i)
		}

		references := make([]Reference, len(event.References))
		for j, reference := range event.References {
			if _, _, err := ParseToken(string(reference)); err != nil {
				return Result{}, fmt.Errorf("event %d reference %d: %w", i, j, ErrInvalidToken)
			}
			resolved, ok := resolveToken(reference, batch.To, entries)
			if !ok {
				references[j] = Reference{Original: reference, Missing: true}
				continue
			}
			references[j] = Reference{Original: reference, Token: resolved}
		}
		rewritten[i] = RewrittenEvent{ID: event.ID, Key: key, References: references}
	}

	if err := store.Publish(ctx, Mapping{Entries: entries}); err != nil {
		return Result{}, fmt.Errorf("publish mapping: %w", err)
	}

	return Result{
		From:    batch.From,
		To:      batch.To,
		Events:  rewritten,
		Mapping: Mapping{Entries: cloneEntries(entries)},
	}, nil
}

// resolveToken follows mapping rows until it reaches target. It is cycle-safe:
// a corrupt or circular mapping chain resolves as missing instead of looping
// forever.
func resolveToken(token Token, target Generation, entries map[Token]Token) (Token, bool) {
	current := token
	seen := make(map[Token]struct{}, 4)
	for {
		gen, ok := current.Generation()
		if !ok {
			return "", false
		}
		if gen == target {
			return current, true
		}
		if _, duplicate := seen[current]; duplicate {
			return "", false
		}
		seen[current] = struct{}{}

		next, ok := entries[current]
		if !ok {
			return "", false
		}
		current = next
	}
}
