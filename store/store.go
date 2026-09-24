// Package store implements a versioned write-through cache coordinator.
//
// Semantics:
//   - A write first lands in the data source; only then is the value
//     committed and published to the cache. If the data source write fails,
//     the previously committed value is kept and the error is returned.
//   - Concurrent writes are linearized by a monotonically increasing
//     version. A slow write that finishes after a newer version committed
//     is rejected as stale and never overwrites the newer value.
//   - Readers only observe committed state, never in-flight writes.
//   - If the cache publish fails after the data source write succeeded,
//     the write is marked partial (PartialError) instead of being reported
//     as a plain failure; retrying with the same ID completes the publish
//     without re-writing the data source.
//   - Writes carry a caller-supplied ID; retrying with the same ID is
//     idempotent and replays the recorded outcome.
//
// The data source, cache, clock and failure injection are all provided as
// functions, so tests (and the demo) run fully in-process with no remote
// cache.
package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Operation names passed to FailFunc.
const (
	OpDataSourceWrite = "ds.write"
	OpCachePublish    = "cache.publish"
)

// WriteFunc writes a value to the data source.
type WriteFunc func(ctx context.Context, key string, value []byte) error

// PublishFunc publishes a committed value to the cache.
type PublishFunc func(ctx context.Context, key string, value []byte, version int64) error

// ClockFunc supplies the current time.
type ClockFunc func() time.Time

// FailFunc may inject an error before the named operation runs.
// Returning nil lets the operation proceed.
type FailFunc func(op string) error

// Status describes the outcome of a write.
type Status int

const (
	// StatusCommitted means the data source write and the cache publish
	// both succeeded.
	StatusCommitted Status = iota
	// StatusPartial means the data source write succeeded but the cache
	// publish failed; the committed value is visible to readers.
	StatusPartial
)

func (s Status) String() string {
	switch s {
	case StatusCommitted:
		return "committed"
	case StatusPartial:
		return "partial"
	default:
		return "unknown"
	}
}

var (
	// ErrClosed is returned by operations on a closed Store.
	ErrClosed = errors.New("store: closed")
	// ErrStaleWrite is returned when a slow write finishes after a newer
	// version of the same key has already committed.
	ErrStaleWrite = errors.New("store: write superseded by a newer version")
)

// PartialError reports that the data source write succeeded but the cache
// publish failed. It does not deny the data source success.
type PartialError struct {
	Key     string
	Version int64
	Err     error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("store: data source write for %q committed at version %d, cache publish failed: %v",
		e.Key, e.Version, e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

// Result is the recorded outcome of a write.
type Result struct {
	ID      string
	Version int64
	Status  Status
	At      time.Time
}

// Options configures a Store. DataSource is required; Cache defaults to a
// no-op, Clock defaults to time.Now, Fail and Closer are optional.
type Options struct {
	DataSource WriteFunc
	Cache      PublishFunc
	Clock      ClockFunc
	Fail       FailFunc
	Closer     io.Closer
}

type entry struct {
	value   []byte
	version int64
	at      time.Time
}

// idemRecord tracks the outcome of a write ID for idempotent retries.
// key, value and version are immutable once dsDone is set.
type idemRecord struct {
	done    chan struct{} // closed when the data source phase terminates
	dsDone  bool          // data source write applied
	failed  bool          // data source write failed terminally
	stale   bool          // superseded by a newer version before commit
	err     error         // terminal data source error or partial publish error
	key     string
	value   []byte
	version int64
	status  Status
	at      time.Time
}

// Store coordinates versioned writes to a data source and a cache.
type Store struct {
	ds      WriteFunc
	publish PublishFunc
	now     ClockFunc
	fail    FailFunc
	closer  io.Closer

	mu        sync.Mutex
	closed    bool
	nextVer   int64
	committed map[string]entry
	ids       map[string]*idemRecord

	wg sync.WaitGroup // in-flight writes; Close waits for them
}

// New creates a Store. It panics if Options.DataSource is nil.
func New(opts Options) *Store {
	if opts.DataSource == nil {
		panic("store: Options.DataSource is required")
	}
	publish := opts.Cache
	if publish == nil {
		publish = func(context.Context, string, []byte, int64) error { return nil }
	}
	now := opts.Clock
	if now == nil {
		now = time.Now
	}
	return &Store{
		ds:        opts.DataSource,
		publish:   publish,
		now:       now,
		fail:      opts.Fail,
		closer:    opts.Closer,
		committed: make(map[string]entry),
		ids:       make(map[string]*idemRecord),
	}
}

// Write applies a write identified by id. Retrying with the same id never
// re-executes the data source write; if the first attempt ended partial,
// the retry completes only the cache publish.
func (s *Store) Write(ctx context.Context, id, key string, value []byte) (Result, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Result{}, ErrClosed
	}
	if rec, ok := s.ids[id]; ok {
		done := rec.done
		s.mu.Unlock()
		<-done // wait for the in-flight first attempt to terminate
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return Result{}, ErrClosed
		}
		switch {
		case rec.failed:
			err := rec.err
			s.mu.Unlock()
			return Result{ID: id}, err
		case rec.stale:
			version := rec.version
			s.mu.Unlock()
			return Result{ID: id, Version: version}, ErrStaleWrite
		case rec.status == StatusCommitted:
			res := Result{ID: id, Version: rec.version, Status: rec.status, At: rec.at}
			s.mu.Unlock()
			return res, nil
		default: // partial: retry only the cache publish
			s.wg.Add(1)
			s.mu.Unlock()
			defer s.wg.Done()
			return s.publishAndRecord(ctx, id, rec)
		}
	}
	s.nextVer++
	version := s.nextVer
	rec := &idemRecord{done: make(chan struct{})}
	s.ids[id] = rec
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()

	if err := s.failAt(OpDataSourceWrite); err != nil {
		return s.finishDSFailure(id, rec, err)
	}
	if err := s.ds(ctx, key, value); err != nil {
		return s.finishDSFailure(id, rec, err)
	}

	s.mu.Lock()
	rec.dsDone = true
	rec.key = key
	rec.value = clone(value)
	rec.version = version
	if cur, ok := s.committed[key]; ok && version <= cur.version {
		// A newer version committed while this write was in flight:
		// do not overwrite it and do not publish to the cache.
		rec.stale = true
		rec.err = ErrStaleWrite
		close(rec.done)
		s.mu.Unlock()
		return Result{ID: id, Version: version}, ErrStaleWrite
	}
	rec.at = s.now()
	s.committed[key] = entry{value: clone(value), version: version, at: rec.at}
	close(rec.done)
	s.mu.Unlock()

	return s.publishAndRecord(ctx, id, rec)
}

// Read returns the latest committed value for key. It never observes
// in-flight (uncommitted) writes.
func (s *Store) Read(key string) (value []byte, version int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.committed[key]
	if !ok {
		return nil, 0, false
	}
	return clone(e.value), e.version, true
}

// Close marks the store closed, waits for in-flight writes to finish and
// releases the configured Closer exactly once.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.wg.Wait()
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}

func (s *Store) finishDSFailure(id string, rec *idemRecord, cause error) (Result, error) {
	s.mu.Lock()
	rec.failed = true
	rec.err = cause
	close(rec.done)
	s.mu.Unlock()
	return Result{ID: id}, cause
}

func (s *Store) publishAndRecord(ctx context.Context, id string, rec *idemRecord) (Result, error) {
	err := s.failAt(OpCachePublish)
	if err == nil {
		err = s.publish(ctx, rec.key, rec.value, rec.version)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		rec.status = StatusPartial
		rec.err = &PartialError{Key: rec.key, Version: rec.version, Err: err}
		return Result{ID: id, Version: rec.version, Status: StatusPartial, At: rec.at}, rec.err
	}
	rec.status = StatusCommitted
	rec.err = nil
	return Result{ID: id, Version: rec.version, Status: StatusCommitted, At: rec.at}, nil
}

func (s *Store) failAt(op string) error {
	if s.fail == nil {
		return nil
	}
	return s.fail(op)
}

func clone(b []byte) []byte {
	return append([]byte(nil), b...)
}
