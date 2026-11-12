package netsec

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"
)

// Deriver derives a password hash. Implementations are expected to be
// expensive KDFs (for example Argon2id); the cost is injected so that the
// history package itself stays algorithm agnostic.
//
// Every invocation must use exactly the supplied parameters. A historical
// record never borrows another record's algorithm, cost or salt.
type Deriver interface {
	Derive(password string, params Parameters) (hash []byte, err error)
}

// Parameters holds the derivation parameters fixed for a single record.
type Parameters struct {
	Algorithm string
	Cost      int
	Salt      []byte
}

// Clone returns an independent copy of the parameters.
func (p Parameters) Clone() Parameters {
	return Parameters{
		Algorithm: p.Algorithm,
		Cost:      p.Cost,
		Salt:      append([]byte(nil), p.Salt...),
	}
}

func (p Parameters) validate() error {
	switch {
	case p.Algorithm == "":
		return fmt.Errorf("%w: empty algorithm", ErrCorruptRecord)
	case p.Cost <= 0:
		return fmt.Errorf("%w: non-positive cost %d", ErrCorruptRecord, p.Cost)
	case len(p.Salt) == 0:
		return fmt.Errorf("%w: empty salt", ErrCorruptRecord)
	default:
		return nil
	}
}

// Record is one immutable password history entry. Its algorithm, cost, salt
// and version are fixed at append time and never rewritten.
type Record struct {
	Version   int
	Algorithm string
	Cost      int
	Salt      []byte
	Hash      []byte
}

// Parameters returns the record's own derivation parameters as an
// independent copy, so callers cannot mutate the stored salt.
func (r Record) Parameters() Parameters {
	return Parameters{
		Algorithm: r.Algorithm,
		Cost:      r.Cost,
		Salt:      append([]byte(nil), r.Salt...),
	}
}

func (r Record) clone() Record {
	return Record{
		Version:   r.Version,
		Algorithm: r.Algorithm,
		Cost:      r.Cost,
		Salt:      append([]byte(nil), r.Salt...),
		Hash:      append([]byte(nil), r.Hash...),
	}
}

func (r Record) validate() error {
	if r.Version <= 0 {
		return fmt.Errorf("%w: non-positive version %d", ErrCorruptRecord, r.Version)
	}
	if err := r.Parameters().validate(); err != nil {
		return err
	}
	if len(r.Hash) == 0 {
		return fmt.Errorf("%w: empty hash", ErrCorruptRecord)
	}
	return nil
}

var (
	// ErrVersionConflict means the caller's expected version no longer
	// matches the store; another password change committed first.
	ErrVersionConflict = errors.New("netsec: password history version conflict")
	// ErrReusedPassword means the candidate password matches a historical
	// record under that record's own parameters.
	ErrReusedPassword = errors.New("netsec: password matches a historical record")
	// ErrCorruptRecord means persisted history data is structurally invalid.
	ErrCorruptRecord = errors.New("netsec: corrupt password history record")
)

// Store is an append-only, concurrency-safe password history. Each stored
// record keeps the exact algorithm, cost and salt used to produce its hash,
// and every comparison re-derives the candidate password with that record's
// own parameters.
type Store struct {
	mu      sync.RWMutex
	deriver Deriver
	limit   int
	version int
	records []Record
}

// NewStore creates an empty history holding at most limit records.
func NewStore(deriver Deriver, limit int) *Store {
	if deriver == nil {
		panic("netsec: nil deriver")
	}
	if limit < 1 {
		panic("netsec: history limit must be positive")
	}
	return &Store{deriver: deriver, limit: limit}
}

// Load hydrates a store from persisted records, for example records owned by
// an identity host. Every record is validated before it enters the store and
// the store version is reconstructed from the records.
func Load(deriver Deriver, limit int, records []Record) (*Store, error) {
	store := NewStore(deriver, limit)
	for index, record := range records {
		if err := record.validate(); err != nil {
			return nil, fmt.Errorf("load history record %d: %w", index, err)
		}
		if record.Version > store.version {
			store.version = record.Version
		}
		store.records = append(store.records, record.clone())
	}
	return store, nil
}

// Version returns the current monotonically increasing history version.
func (s *Store) Version() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Len returns the number of retained history records.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

// Records returns a defensive snapshot of the history, oldest first.
func (s *Store) Records() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := make([]Record, len(s.records))
	for index, record := range s.records {
		snapshot[index] = record.clone()
	}
	return snapshot
}

// Current returns the newest record and true, or a zero Record and false for
// an empty history.
func (s *Store) Current() (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.records) == 0 {
		return Record{}, false
	}
	return s.records[len(s.records)-1].clone(), true
}

// IsHistorical reports whether password matches any retained record. The
// candidate is derived once per record using that record's own parameters;
// parameters are never shared across records.
func (s *Store) IsHistorical(password string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	records := make([]Record, len(s.records))
	for index, record := range s.records {
		records[index] = record.clone()
	}

	for _, record := range records {
		matched, err := matches(record, password, s.deriver)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// ChangePassword conditionally appends a new history record.
//
// The append is atomic with respect to concurrent callers: expectedVersion
// must equal the current store version or ErrVersionConflict is returned and
// the old history is left untouched. A password matching any historical
// record under that record's own parameters is rejected with
// ErrReusedPassword, again without mutating history. On success the new
// record becomes the current record at version expectedVersion+1 and, if the
// configured limit is exceeded, only the oldest records are trimmed.
func (s *Store) ChangePassword(expectedVersion int, newPassword string, params Parameters) (Record, error) {
	if newPassword == "" {
		return Record{}, errors.New("netsec: empty password")
	}
	params = params.Clone()
	if err := params.validate(); err != nil {
		return Record{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if expectedVersion != s.version {
		return Record{}, fmt.Errorf("%w: expected version %d, current version %d",
			ErrVersionConflict, expectedVersion, s.version)
	}

	for _, record := range s.records {
		matched, err := matches(record, newPassword, s.deriver)
		if err != nil {
			return Record{}, err
		}
		if matched {
			return Record{}, fmt.Errorf("%w: matches version %d", ErrReusedPassword, record.Version)
		}
	}

	hash, err := s.deriver.Derive(newPassword, params)
	if err != nil {
		return Record{}, fmt.Errorf("netsec: derive new password: %w", err)
	}

	record := Record{
		Version:   s.version + 1,
		Algorithm: params.Algorithm,
		Cost:      params.Cost,
		Salt:      params.Salt,
		Hash:      hash,
	}
	if err := record.validate(); err != nil {
		return Record{}, err
	}

	s.records = append(s.records, record)
	s.version = record.Version
	s.trimLocked()
	return record.clone(), nil
}

// trimLocked drops the oldest records beyond the limit. Appends happen one at
// a time under the store lock, and limit is at least one, so the current
// (newest) record can never be trimmed.
func (s *Store) trimLocked() {
	if len(s.records) <= s.limit {
		return
	}
	overflow := len(s.records) - s.limit
	trimmed := make([]Record, 0, s.limit)
	trimmed = append(trimmed, s.records[overflow:]...)
	s.records = trimmed
}

// matches re-derives password with the record's own fixed parameters and
// performs a constant-time equality comparison.
func matches(record Record, password string, deriver Deriver) (bool, error) {
	hash, err := deriver.Derive(password, record.Parameters())
	if err != nil {
		return false, fmt.Errorf("netsec: derive against history version %d: %w", record.Version, err)
	}
	return len(hash) == len(record.Hash) && subtle.ConstantTimeCompare(hash, record.Hash) == 1, nil
}
