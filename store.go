package tracevault

import "sync"

// Record is a single encrypted trajectory point as stored at rest. It carries
// no plaintext entity ID and no plaintext coordinates.
type Record struct {
	Pseudonym  string
	Epoch      int64
	CellHash   string
	Ciphertext []byte
}

// Store is an in-memory encrypted record store with pseudonym and salted-cell
// indexes, safe for concurrent use.
type Store struct {
	mu      sync.RWMutex
	records []Record
	byPseud map[string][]int
	byCell  map[string][]int
}

func NewStore() *Store {
	return &Store{
		byPseud: make(map[string][]int),
		byCell:  make(map[string][]int),
	}
}

func (s *Store) Add(r Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := len(s.records)
	s.records = append(s.records, r)
	s.byPseud[r.Pseudonym] = append(s.byPseud[r.Pseudonym], idx)
	s.byCell[r.CellHash] = append(s.byCell[r.CellHash], idx)
}

func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

// ByPseudonym returns all records for a pseudonym within [fromEpoch, toEpoch].
func (s *Store) ByPseudonym(pseudonym string, fromEpoch, toEpoch int64) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Record
	for _, idx := range s.byPseud[pseudonym] {
		r := s.records[idx]
		if r.Epoch >= fromEpoch && r.Epoch <= toEpoch {
			out = append(out, r)
		}
	}
	return out
}

// Heatmap aggregates record counts per salted cell hash over an epoch range.
// It exposes no entity information and no plaintext coordinates.
func (s *Store) Heatmap(fromEpoch, toEpoch int64) map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int)
	for cell, idxs := range s.byCell {
		for _, idx := range idxs {
			r := s.records[idx]
			if r.Epoch >= fromEpoch && r.Epoch <= toEpoch {
				out[cell]++
			}
		}
	}
	return out
}
