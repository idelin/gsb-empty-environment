package netsec

import "sync"

// Store is an in-memory DHT record store keyed by DHT record key (typically
// an /ipns/<peer-id> string). Every write passes through the injected
// [V2Verifier] and the full IPNS validation pipeline, so the store can only
// ever contain records that were fresh, well-formed and correctly signed at
// insertion time.
type Store struct {
	validator *Validator

	mu      sync.RWMutex
	records map[string]*Record
}

// NewStore creates a [Store] backed by the given [Validator].
func NewStore(v *Validator) *Store {
	return &Store{
		validator: v,
		records:   make(map[string]*Record),
	}
}

// Put validates raw and stores it under key. The replacement policy is:
//
//   - a record with a strictly larger Sequence replaces the stored one;
//   - on equal Sequence, the record with the later EOL replaces it;
//   - on equal Sequence and EOL, the incoming record is discarded and
//     [ErrRecordSuperseded] is returned while the stored copy is kept;
//   - a record with a smaller Sequence is a replay and is discarded with
//     [ErrRecordSuperseded].
//
// Any other validation error is returned without touching the stored record.
func (s *Store) Put(key string, raw []byte) (*Record, error) {
	record, err := s.validator.Validate(key, raw)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.records[key]
	if exists && !ShouldReplace(current, record) {
		return current, ErrRecordSuperseded
	}
	s.records[key] = record
	return record, nil
}

// Get returns the raw protobuf bytes of the record stored under key, or nil
// when no record exists.
func (s *Store) Get(key string) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.records[key]
	if !ok {
		return nil
	}
	return append([]byte(nil), record.raw...)
}

// Current returns the parsed record stored under key, or nil.
func (s *Store) Current(key string) *Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.records[key]
}

// ShouldReplace reports whether incoming may replace stored according to the
// IPNS ordering: higher sequence wins; equal sequence requires a strictly
// later EOL; identical ordering never replaces. Both records are expected to
// carry parsed EOL values from [Validator.Validate].
func ShouldReplace(stored, incoming *Record) bool {
	if incoming.Sequence != stored.Sequence {
		return incoming.Sequence > stored.Sequence
	}
	return incoming.eol.After(stored.eol)
}

// Select implements the libp2p record validator selection contract: among
// the candidate raw records for key it returns the index of the one that
// passes full validation and wins the sequence/EOL ordering. Candidates that
// fail validation (bad signature, expired, inconsistent fields, ...) are
// skipped. [ErrNoValidRecord] is returned when every candidate is invalid.
func (v *Validator) Select(key string, candidates [][]byte) (int, error) {
	best := -1
	var bestRecord *Record

	for i, raw := range candidates {
		record, err := v.Validate(key, raw)
		if err != nil {
			continue
		}
		if best == -1 || ShouldReplace(bestRecord, record) {
			best = i
			bestRecord = record
		}
	}

	if best == -1 {
		return -1, ErrNoValidRecord
	}
	return best, nil
}

// Select runs [Validator.Select] against the store's validator.
func (s *Store) Select(key string, candidates [][]byte) (int, error) {
	return s.validator.Select(key, candidates)
}
