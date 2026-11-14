package netsec

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const ticketIDSize = 16

// TicketID is the random, single-use identifier authenticated inside a ticket.
type TicketID [ticketIDSize]byte

func (id TicketID) String() string {
	return hex.EncodeToString(id[:])
}

func parseTicketID(s string) (TicketID, error) {
	var id TicketID
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != ticketIDSize {
		return id, fmt.Errorf("%w: malformed ticket ID", ErrTicketInvalid)
	}
	copy(id[:], raw)
	return id, nil
}

func (id TicketID) isZero() bool {
	for _, b := range id {
		if b != 0 {
			return false
		}
	}
	return true
}

// ReplayStore provides the atomic single-use decision for a ticket. Consume
// must succeed only for the first caller presenting id, and must retain the
// record until expiresAt so that a replay remains detectable. Implementations
// must be safe for concurrent use.
type ReplayStore interface {
	Consume(ctx context.Context, id TicketID, now, expiresAt time.Time) error
}

// MemoryReplayStore is an in-process ReplayStore. It is suitable for a single
// AAA instance and for tests. Expired entries are pruned opportunistically on
// every Consume call.
type MemoryReplayStore struct {
	mu   sync.Mutex
	used map[TicketID]time.Time
}

func NewMemoryReplayStore() *MemoryReplayStore {
	return &MemoryReplayStore{used: make(map[TicketID]time.Time)}
}

func (m *MemoryReplayStore) Consume(ctx context.Context, id TicketID, now, expiresAt time.Time) error {
	if m == nil {
		return fmt.Errorf("%w: nil replay store", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if id.isZero() {
		return fmt.Errorf("%w: zero ticket ID", ErrTicketInvalid)
	}
	if !expiresAt.After(now) {
		return ErrTicketExpired
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for usedID, exp := range m.used {
		if !exp.After(now) {
			delete(m.used, usedID)
		}
	}
	if _, ok := m.used[id]; ok {
		return ErrTicketReplayed
	}
	m.used[id] = expiresAt
	return nil
}

// Prune removes consumed-ticket records whose original expiry has passed.
func (m *MemoryReplayStore) Prune(now time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, exp := range m.used {
		if !exp.After(now) {
			delete(m.used, id)
		}
	}
}
