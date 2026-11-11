package replay

import (
	"context"
	"errors"
	"sync"
)

var errAlreadyLocked = errors.New("replay: storage lock is already held")

// RecordKind discriminates the two durable record types.
type RecordKind uint8

const (
	// KindEntry is a ticket acceptance record. It is always appended before
	// the accompanying watermark record.
	KindEntry RecordKind = 1

	// KindWatermark is a persisted monotonic clock lower bound.
	KindWatermark RecordKind = 2
)

// EntryRecord is the durable proof that a ticket key was accepted.
type EntryRecord struct {
	ID                 string
	ClientPrincipal    string
	ServicePrincipal   string
	TicketDigest       []byte
	ClientTimeUnixNano int64
	Sequence           uint32
	AcceptedAtUnixNano int64
}

// WatermarkRecord persists the highest accepted client time. The value only
// moves forward across the whole lifetime of the store.
type WatermarkRecord struct {
	LowerBoundUnixNano int64
	UpdatedAtUnixNano  int64
}

// Record is one append-only log entry. Exactly one of Entry or Watermark is
// non-nil according to Kind.
type Record struct {
	Kind      RecordKind
	Entry     *EntryRecord
	Watermark *WatermarkRecord
}

// Storage is the durability boundary of the replay cache. Implementations must
// provide mutual exclusion across processes sharing the same backing store and
// must guarantee that a successful Append is durable before returning.
//
// The call sequence from Cache for a candidate ticket is:
//
//	Lock -> ReadAll -> Append(entry) -> Append(watermark) -> Unlock
//
// Rejections (replay, stale, future) never reach Append. This makes the two
// Append calls the only durable write points and therefore the only places
// where a power-loss fault needs to be injected.
type Storage interface {
	// Lock acquires the cross-process acceptance lock.
	Lock(ctx context.Context) error

	// Unlock releases the cross-process acceptance lock.
	Unlock(ctx context.Context) error

	// ReadAll returns every durable record in append order.
	ReadAll(ctx context.Context) ([]Record, error)

	// Append durably appends one record and returns only after it is persisted.
	Append(ctx context.Context, rec Record) error

	// Close releases any backing resources.
	Close() error
}

// MemoryStorage is an in-process Storage implementation useful for unit tests
// and for embedders that provide their own external durability. Its records
// are copied on read and write so callers cannot mutate internal state.
type MemoryStorage struct {
	mu      sync.Mutex
	locked  bool
	closed  bool
	records []Record
}

// NewMemoryStorage constructs an empty in-memory store.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{}
}

// Lock implements Storage.
func (s *MemoryStorage) Lock(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.locked {
		return errAlreadyLocked
	}
	s.locked = true
	return nil
}

// Unlock implements Storage.
func (s *MemoryStorage) Unlock(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locked = false
	return nil
}

// ReadAll implements Storage.
func (s *MemoryStorage) ReadAll(context.Context) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	return cloneRecords(s.records), nil
}

// Append implements Storage.
func (s *MemoryStorage) Append(_ context.Context, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.records = append(s.records, cloneRecord(rec))
	return nil
}

// Close implements Storage.
func (s *MemoryStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.locked = false
	return nil
}

// Reopen clears the closed/locked state while retaining all records. It models
// restarting the process around an in-memory store in unit tests; real
// cross-process durability is exercised against FileStorage.
func (s *MemoryStorage) Reopen() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = false
	s.locked = false
}

// Snapshot returns a copy of the current durable records without taking the
// acceptance lock; it is intended for test assertions.
func (s *MemoryStorage) Snapshot() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneRecords(s.records)
}

// Fault describes when an injected fault should fire during Append.
type Fault int

const (
	// FaultBeforeWrite fires before the underlying write happens. Nothing is
	// persisted, modelling power loss immediately before a write.
	FaultBeforeWrite Fault = iota

	// FaultAfterWrite fires after the underlying write succeeded. The record
	// is persisted while the caller observes a crash, modelling power loss
	// between durability and the process learning about it.
	FaultAfterWrite

	// FaultTornWrite persists only part of the record and then reports a
	// crash. Only backing stores that expose partial writes (FileStorage)
	// support this mode.
	FaultTornWrite
)

// FaultHook decides whether a given append should fail. ordinal is the
// one-based position of this Append among all durable appends observed by the
// backing store across its whole lifetime (including previous "processes"
// represented by reopened stores).
type FaultHook func(ordinal int, rec Record) (fault Fault, fire bool)

// FaultyStorage wraps any Storage and injects power-loss faults at append
// points. Before-write faults leave the backing store untouched; after-write
// faults deliver the record first and then return ErrPowerLoss.
type FaultyStorage struct {
	inner Storage
	hook  FaultHook

	mu      sync.Mutex
	ordinal int
}

// NewFaultyStorage wraps inner. hook may be nil, in which case the wrapper is
// transparent.
func NewFaultyStorage(inner Storage, hook FaultHook) *FaultyStorage {
	return &FaultyStorage{inner: inner, hook: hook}
}

// Lock implements Storage.
func (s *FaultyStorage) Lock(ctx context.Context) error { return s.inner.Lock(ctx) }

// Unlock implements Storage.
func (s *FaultyStorage) Unlock(ctx context.Context) error { return s.inner.Unlock(ctx) }

// ReadAll implements Storage.
func (s *FaultyStorage) ReadAll(ctx context.Context) ([]Record, error) {
	return s.inner.ReadAll(ctx)
}

// Append implements Storage with fault injection.
func (s *FaultyStorage) Append(ctx context.Context, rec Record) error {
	s.mu.Lock()
	s.ordinal++
	ordinal := s.ordinal
	hook := s.hook
	s.mu.Unlock()

	if hook != nil {
		if fault, fire := hook(ordinal, rec); fire {
			switch fault {
			case FaultBeforeWrite:
				return ErrPowerLoss
			case FaultTornWrite:
				if torn, ok := s.inner.(interface {
					appendTorn(context.Context, Record) error
				}); ok {
					return torn.appendTorn(ctx, rec)
				}
				return ErrPowerLoss
			case FaultAfterWrite:
			}
		}
	}

	if err := s.inner.Append(ctx, rec); err != nil {
		return err
	}

	if hook != nil {
		if fault, fire := hook(ordinal, rec); fire && fault == FaultAfterWrite {
			return ErrPowerLoss
		}
	}
	return nil
}

// Close implements Storage.
func (s *FaultyStorage) Close() error { return s.inner.Close() }

// Ordinal reports how many appends have been attempted through the wrapper.
func (s *FaultyStorage) Ordinal() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ordinal
}

func cloneRecords(in []Record) []Record {
	out := make([]Record, len(in))
	for i, rec := range in {
		out[i] = cloneRecord(rec)
	}
	return out
}

func cloneRecord(rec Record) Record {
	out := Record{Kind: rec.Kind}
	if rec.Entry != nil {
		entry := *rec.Entry
		entry.TicketDigest = append([]byte(nil), rec.Entry.TicketDigest...)
		out.Entry = &entry
	}
	if rec.Watermark != nil {
		watermark := *rec.Watermark
		out.Watermark = &watermark
	}
	return out
}
