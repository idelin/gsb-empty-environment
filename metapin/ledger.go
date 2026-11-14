package metapin

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// EventKind identifies the lifecycle record written to the chain.
type EventKind string

const (
	EventMint     EventKind = "mint"
	EventTransfer EventKind = "transfer"
	EventRoyalty  EventKind = "royalty"
)

// Event is a chain record. Every event for a token binds the same content
// commitment, so mint, transfer and royalty history all anchor to the
// original metadata digest.
type Event struct {
	Kind       EventKind
	TokenID    string
	Commitment Digest
	From       string
	To         string
	RoyaltyBps int
	At         time.Time
}

// ErrCommitmentConflict is returned when an event tries to bind a token to
// a digest different from its mint commitment.
var ErrCommitmentConflict = errors.New("metapin: event digest conflicts with mint commitment")

// Ledger is the chain commitment store the component relies on.
type Ledger interface {
	// Commit appends an event. Non-mint events must carry the token's
	// existing mint commitment or ErrCommitmentConflict is returned.
	Commit(ev Event) error
	// CommitmentOf returns the mint commitment for a token.
	CommitmentOf(tokenID string) (Digest, bool)
	// History returns all events for a token in commit order.
	History(tokenID string) []Event
}

// InMemoryLedger is a reference Ledger implementation for services and tests.
type InMemoryLedger struct {
	mu          sync.Mutex
	commitments map[string]Digest
	events      map[string][]Event
}

func NewInMemoryLedger() *InMemoryLedger {
	return &InMemoryLedger{
		commitments: make(map[string]Digest),
		events:      make(map[string][]Event),
	}
}

func (l *InMemoryLedger) Commit(ev Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ev.Kind == EventMint {
		if _, exists := l.commitments[ev.TokenID]; exists {
			return fmt.Errorf("metapin: token %q already minted", ev.TokenID)
		}
		l.commitments[ev.TokenID] = ev.Commitment
	} else {
		want, exists := l.commitments[ev.TokenID]
		if !exists {
			return fmt.Errorf("metapin: token %q not minted", ev.TokenID)
		}
		if ev.Commitment != want {
			return ErrCommitmentConflict
		}
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	l.events[ev.TokenID] = append(l.events[ev.TokenID], ev)
	return nil
}

func (l *InMemoryLedger) CommitmentOf(tokenID string) (Digest, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d, ok := l.commitments[tokenID]
	return d, ok
}

func (l *InMemoryLedger) History(tokenID string) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.events[tokenID]))
	copy(out, l.events[tokenID])
	return out
}
