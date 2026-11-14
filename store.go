package netsec

import (
	"context"
	"errors"
	"sync"
	"time"
)

const consumedRecordRetention = 24 * time.Hour

// ErrStoreUnavailable is returned when the one-time consumption store cannot
// be read or updated. Authentication must not proceed when this happens.
var ErrStoreUnavailable = errors.New("magiclink: consumption store unavailable")

// Store persists one-time token consumption state.
//
// MarkConsumed must be atomic: it reports true only for the first caller that
// successfully records tokenID. Implementations used by more than one process
// should back this with a unique constraint or a compare-and-set operation,
// not a read followed by a write.
//
// IsConsumed is only a lookup. It is safe to call from a GET preflight and must
// not create a consumption record.
type Store interface {
	IsConsumed(ctx context.Context, tokenID string) (bool, error)
	MarkConsumed(ctx context.Context, tokenID string, expiresAt time.Time) (bool, error)
}

// MemoryStore is an in-memory Store suitable for a single process and tests.
// Its zero value is ready for use.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]time.Time
	now     func() time.Time
}

// NewMemoryStore returns an empty in-memory consumption store.
func NewMemoryStore() *MemoryStore {
	return newMemoryStore(time.Now)
}

func newMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{
		records: make(map[string]time.Time),
		now:     now,
	}
}

// IsConsumed reports whether tokenID has already been consumed. Records are
// retained briefly past token expiry so small clock adjustments cannot make a
// consumed token live again; token expiry is still enforced by the token
// itself, and this store is not a substitute for validating exp.
func (s *MemoryStore) IsConsumed(ctx context.Context, tokenID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if tokenID == "" {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	expiresAt, ok := s.records[tokenID]
	if !ok {
		return false, nil
	}
	if !s.now().Before(expiresAt) {
		delete(s.records, tokenID)
		return false, nil
	}
	return true, nil
}

// MarkConsumed atomically records tokenID as consumed. It returns true only
// when this call created the record.
func (s *MemoryStore) MarkConsumed(ctx context.Context, tokenID string, expiresAt time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if tokenID == "" || expiresAt.IsZero() {
		return false, ErrInvalidToken
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	now := s.now()
	s.purgeLocked(now)
	if existing, ok := s.records[tokenID]; ok && now.Before(existing) {
		return false, nil
	}
	s.records[tokenID] = expiresAt.Add(consumedRecordRetention)
	return true, nil
}

func (s *MemoryStore) initLocked() {
	if s.records == nil {
		s.records = make(map[string]time.Time)
	}
	if s.now == nil {
		s.now = time.Now
	}
}

func (s *MemoryStore) purgeLocked(now time.Time) {
	for tokenID, expiresAt := range s.records {
		if !now.Before(expiresAt) {
			delete(s.records, tokenID)
		}
	}
}
