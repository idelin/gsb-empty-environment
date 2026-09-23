package sim

import "sync"

// StableStorage models a node's durable disk. Appends are atomic and survive
// process crashes (Fail/Recover); only WipeStorage destroys them.
//
// Records are opaque byte strings. The twopc layer stores length-prefixed,
// checksummed WAL records in them.
type StableStorage struct {
	mu   sync.Mutex
	data [][]byte
}

func NewStableStorage() *StableStorage { return &StableStorage{} }

// Append durably stores rec (modelling a single sector-group write: either
// the whole record is present after recovery, or none of it is).
func (s *StableStorage) Append(rec []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(rec))
	copy(cp, rec)
	s.data = append(s.data, cp)
}

// Read returns all durable records in append order.
func (s *StableStorage) Read() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.data))
	for i, rec := range s.data {
		cp := make([]byte, len(rec))
		copy(cp, rec)
		out[i] = cp
	}
	return out
}

// Len reports how many records are stored.
func (s *StableStorage) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data)
}
