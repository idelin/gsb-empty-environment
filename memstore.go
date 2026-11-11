package netsec

import (
	"errors"
	"sync"
)

// ErrInsufficientCapacity is returned when a reservation cannot be satisfied
// because the remaining capacity of the target pool is too small.
var ErrInsufficientCapacity = errors.New("netsec: insufficient capacity")

// MemCapacityStore is an in-memory Reserver useful for tests and for hosts
// that track hardware capacity inside the scheduling process.
type MemCapacityStore struct {
	mu        sync.Mutex
	remaining map[storeKey]int
}

type storeKey struct {
	domainID string
	purpose  string
}

// NewMemCapacityStore seeds every (domain, purpose) pool from domains.
func NewMemCapacityStore(domains []Domain) *MemCapacityStore {
	remaining := make(map[storeKey]int)
	for _, d := range domains {
		for purpose, capacity := range d.Purposes {
			remaining[storeKey{domainID: d.ID, purpose: purpose}] = capacity
		}
	}
	return &MemCapacityStore{remaining: remaining}
}

// Reserve claims r atomically or fails without changing the pool.
func (m *MemCapacityStore) Reserve(r Reservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := storeKey{domainID: r.DomainID, purpose: r.Purpose}
	available, ok := m.remaining[key]
	if !ok || r.Amount > available {
		return ErrInsufficientCapacity
	}
	m.remaining[key] = available - r.Amount
	return nil
}

// Release returns a previously reserved amount to the pool.
func (m *MemCapacityStore) Release(r Reservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := storeKey{domainID: r.DomainID, purpose: r.Purpose}
	m.remaining[key] += r.Amount
	return nil
}

// Remaining reports the unclaimed capacity of a pool. Unknown pools return 0.
func (m *MemCapacityStore) Remaining(domainID, purpose string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.remaining[storeKey{domainID: domainID, purpose: purpose}]
}
