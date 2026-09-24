package versionedcache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"
)

// Status describes how far a write progressed.
type Status string

const (
	StatusCommitted Status = "committed"
	StatusPartial   Status = "partial"
	StatusFailed    Status = "failed"
)

var (
	ErrDataSourceWrite = errors.New("versionedcache: data source write failed")
	ErrCachePublish    = errors.New("versionedcache: cache publish failed")
	ErrClosed          = errors.New("versionedcache: store is closed")
	ErrRetryConflict   = errors.New("versionedcache: retry id was already used with a different value")
)

// SourceRequest is passed to the data-source function.
type SourceRequest[T any] struct {
	Version int64
	RetryID string
	Value   T
}

// SourceFunc performs the durable data-source write.
type SourceFunc[T any] func(ctx context.Context, request SourceRequest[T]) error

// Entry is one published cache value.
type Entry[T any] struct {
	Version     int64
	Value       T
	PublishedAt time.Time
}

// PublishFunc publishes a committed value to the local cache seam.
type PublishFunc[T any] func(ctx context.Context, entry Entry[T]) error

// ClockFunc returns the current time.
type ClockFunc func() time.Time

// Options configures a Store.
type Options[T any] struct {
	DataSource SourceFunc[T]
	Publish    PublishFunc[T]
	Now        ClockFunc
}

// Result is the outcome of a write or coalesced retry.
type Result[T any] struct {
	Version          int64
	Value            T
	RetryID          string
	Status           Status
	StartedAt        time.Time
	SourceFinishedAt time.Time
	CompletedAt      time.Time
	Err              error
}

// Store coordinates data-source writes and ordered cache publication.
type Store[T any] struct {
	dataSource SourceFunc[T]
	publish    PublishFunc[T]
	now        ClockFunc

	mu              sync.Mutex
	cond            *sync.Cond
	closed          bool
	nextVersion     int64
	resolvedVersion int64
	cached          Entry[T]
	cacheOK         bool

	inFlightRetry map[string]*attempt[T]
	retryResults  map[string]Result[T]

	ctx       context.Context
	cancel    context.CancelFunc
	sourceWG  sync.WaitGroup
	closeOnce sync.Once
	closedCh  chan struct{}
}

type attempt[T any] struct {
	version   int64
	value     T
	retryID   string
	startedAt time.Time

	done             chan struct{}
	sourceFinished   bool
	sourceErr        error
	sourceFinishedAt time.Time
	result           Result[T]
}

// New creates a Store with injectable data-source, publication, and clock functions.
func New[T any](options Options[T]) (*Store[T], error) {
	if options.DataSource == nil {
		return nil, errors.New("versionedcache: DataSource is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Publish == nil {
		options.Publish = func(context.Context, Entry[T]) error { return nil }
	}

	ctx, cancel := context.WithCancel(context.Background())
	store := &Store[T]{
		dataSource:    options.DataSource,
		publish:       options.Publish,
		now:           options.Now,
		nextVersion:   1,
		inFlightRetry: make(map[string]*attempt[T]),
		retryResults:  make(map[string]Result[T]),
		ctx:           ctx,
		cancel:        cancel,
		closedCh:      make(chan struct{}),
	}
	store.cond = sync.NewCond(&store.mu)
	return store, nil
}

// Write assigns a version, writes the data source, and publishes in version order.
func (s *Store[T]) Write(ctx context.Context, value T, retryID string) (Result[T], error) {
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Result[T]{}, ErrClosed
	}

	if retryID != "" {
		if result, ok := s.retryResults[retryID]; ok {
			s.mu.Unlock()
			if !reflect.DeepEqual(result.Value, value) {
				return Result[T]{}, fmt.Errorf("%w: %s", ErrRetryConflict, retryID)
			}
			return result, result.Err
		}
		if existing, ok := s.inFlightRetry[retryID]; ok {
			s.mu.Unlock()
			if !reflect.DeepEqual(existing.value, value) {
				return Result[T]{}, fmt.Errorf("%w: %s", ErrRetryConflict, retryID)
			}
			return s.waitResult(ctx, existing)
		}
	}

	current := &attempt[T]{
		version:   s.nextVersion,
		value:     value,
		retryID:   retryID,
		startedAt: s.now(),
		done:      make(chan struct{}),
	}
	s.nextVersion++
	if retryID != "" {
		s.inFlightRetry[retryID] = current
	}

	s.sourceWG.Add(1)
	go s.runSource(current)
	s.mu.Unlock()

	return s.waitResult(ctx, current)
}

// Read returns only the latest fully published cache entry.
func (s *Store[T]) Read(ctx context.Context) (Entry[T], bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return Entry[T]{}, false, ctx.Err()
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Entry[T]{}, false, ErrClosed
	}
	if !s.cacheOK {
		return Entry[T]{}, false, nil
	}
	return s.cached, true, nil
}

// Close cancels inflight source work and waits for internal goroutines to finish.
func (s *Store[T]) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.sourceWG.Wait()

		s.mu.Lock()
		s.closed = true
		close(s.closedCh)
		s.cond.Broadcast()
		s.mu.Unlock()
	})
	return nil
}

func (s *Store[T]) waitResult(ctx context.Context, current *attempt[T]) (Result[T], error) {
	select {
	case <-current.done:
		return current.result, current.result.Err
	case <-ctx.Done():
		return Result[T]{
			Version: current.version,
			Value:   current.value,
			RetryID: current.retryID,
			Status:  StatusFailed,
			Err:     ctx.Err(),
		}, ctx.Err()
	case <-s.closedCh:
		return Result[T]{}, ErrClosed
	}
}

func (s *Store[T]) runSource(current *attempt[T]) {
	defer s.sourceWG.Done()

	err := s.dataSource(s.ctx, SourceRequest[T]{
		Version: current.version,
		RetryID: current.retryID,
		Value:   current.value,
	})
	finishedAt := s.now()

	s.mu.Lock()
	current.sourceFinished = true
	current.sourceErr = err
	current.sourceFinishedAt = finishedAt
	s.mu.Unlock()

	s.waitForTurnAndCommit(current)
}

func (s *Store[T]) waitForTurnAndCommit(current *attempt[T]) {
	s.mu.Lock()
	for s.resolvedVersion < current.version-1 {
		s.cond.Wait()
	}
	sourceErr := current.sourceErr
	s.mu.Unlock()

	if sourceErr != nil {
		result := Result[T]{
			Version:          current.version,
			Value:            current.value,
			RetryID:          current.retryID,
			Status:           StatusFailed,
			StartedAt:        current.startedAt,
			SourceFinishedAt: current.sourceFinishedAt,
			CompletedAt:      s.now(),
			Err: fmt.Errorf(
				"%w: version %d: %w",
				ErrDataSourceWrite,
				current.version,
				sourceErr,
			),
		}
		s.finish(current, result, Entry[T]{}, false)
		return
	}

	entry := Entry[T]{
		Version:     current.version,
		Value:       current.value,
		PublishedAt: s.now(),
	}
	publishErr := s.publish(s.ctx, entry)
	if publishErr != nil {
		result := Result[T]{
			Version:          current.version,
			Value:            current.value,
			RetryID:          current.retryID,
			Status:           StatusPartial,
			StartedAt:        current.startedAt,
			SourceFinishedAt: current.sourceFinishedAt,
			CompletedAt:      s.now(),
			Err: fmt.Errorf(
				"%w: version %d: %w",
				ErrCachePublish,
				current.version,
				publishErr,
			),
		}
		s.finish(current, result, Entry[T]{}, false)
		return
	}

	result := Result[T]{
		Version:          current.version,
		Value:            current.value,
		RetryID:          current.retryID,
		Status:           StatusCommitted,
		StartedAt:        current.startedAt,
		SourceFinishedAt: current.sourceFinishedAt,
		CompletedAt:      s.now(),
	}
	s.finish(current, result, entry, true)
}

func (s *Store[T]) finish(current *attempt[T], result Result[T], entry Entry[T], updateCache bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if updateCache {
		s.cached = entry
		s.cacheOK = true
	}
	s.resolvedVersion = current.version
	current.result = result

	if current.retryID != "" {
		delete(s.inFlightRetry, current.retryID)
		s.retryResults[current.retryID] = result
	}

	close(current.done)
	s.cond.Broadcast()
}
