package netsec

import (
	"fmt"
	"sync"
	"time"
)

// Compare applies the record-replacement rules used by DHT record storage:
//
//   - a larger sequence number always wins;
//   - on equal sequence, the record with the later EOL wins;
//   - if both sequence and EOL are equal, the incoming record is discarded.
//
// Both records must have been produced by Validator.Validate, so their EOL
// values are already parsed.
func Compare(stored, incoming *Record) error {
	switch {
	case incoming.Sequence > stored.Sequence:
		return nil
	case incoming.Sequence < stored.Sequence:
		return fmt.Errorf("%w: incoming %d stored %d", ErrOldSequence, incoming.Sequence, stored.Sequence)
	}

	switch {
	case incoming.EOL().After(stored.EOL()):
		return nil
	case incoming.EOL().Before(stored.EOL()):
		return fmt.Errorf("%w: equal sequence %d with earlier EOL", ErrEqualSequence, incoming.Sequence)
	default:
		return fmt.Errorf("%w: sequence %d EOL %s", ErrEqualRecord, incoming.Sequence, incoming.EOL().Format(time.RFC3339Nano))
	}
}

// Store is an in-memory DHT record store keyed by record key (typically the
// IPNS key path). Every Put is validated through the embedded Validator and
// accepted only when it beats the currently stored record.
type Store struct {
	validator *Validator
	mu        sync.RWMutex
	records   map[string]*Record
}

// NewStore builds a Store using the supplied Validator.
func NewStore(v *Validator) *Store {
	return &Store{validator: v, records: make(map[string]*Record)}
}

// Put validates data and stores it under key when it beats any existing
// record for that key. The validated record is returned on success.
func (s *Store) Put(key string, data []byte) (*Record, error) {
	rec, err := s.validator.Validate(data)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if stored, ok := s.records[key]; ok {
		if err := Compare(stored, rec); err != nil {
			return nil, err
		}
	}
	s.records[key] = rec
	return rec, nil
}

// Get returns the stored record for key.
func (s *Store) Get(key string) (*Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, ok := s.records[key]
	if !ok {
		return nil, fmt.Errorf("ipns: no record stored for key %q", key)
	}
	return rec, nil
}
