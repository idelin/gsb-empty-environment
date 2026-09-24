package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDataSource is an in-process, injectable data source.
type fakeDataSource struct {
	mu         sync.Mutex
	data       map[string][]byte
	calls      int
	blockUntil chan struct{} // if non-nil, writes wait on it
	closed     bool
	closeCalls int
}

func newFakeDataSource() *fakeDataSource {
	return &fakeDataSource{data: make(map[string][]byte)}
}

func (f *fakeDataSource) write(_ context.Context, key string, value []byte) error {
	f.mu.Lock()
	ch := f.blockUntil
	f.mu.Unlock()
	if ch != nil {
		<-ch
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.data[key] = append([]byte(nil), value...)
	return nil
}

func (f *fakeDataSource) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeDataSource) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.closeCalls++
	return nil
}

// fakeCache records every publish in-process; no remote cache involved.
type fakeCache struct {
	mu        sync.Mutex
	published map[string][]byte
	versions  map[string]int64
	calls     int
}

func newFakeCache() *fakeCache {
	return &fakeCache{published: make(map[string][]byte), versions: make(map[string]int64)}
}

func (f *fakeCache) publish(_ context.Context, key string, value []byte, version int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.published[key] = append([]byte(nil), value...)
	f.versions[key] = version
	return nil
}

func (f *fakeCache) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func mustWrite(t *testing.T, s *Store, id, key, value string) Result {
	t.Helper()
	res, err := s.Write(context.Background(), id, key, []byte(value))
	if err != nil {
		t.Fatalf("Write(%q) failed: %v", id, err)
	}
	return res
}

func TestWriteSuccessPublishesCache(t *testing.T) {
	ds := newFakeDataSource()
	cache := newFakeCache()
	s := New(Options{DataSource: ds.write, Cache: cache.publish})
	defer s.Close()

	res := mustWrite(t, s, "w1", "k", "v1")
	if res.Status != StatusCommitted || res.Version != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	got, ver, ok := s.Read("k")
	if !ok || string(got) != "v1" || ver != 1 {
		t.Fatalf("Read = %q, %d, %v", got, ver, ok)
	}
	if string(cache.published["k"]) != "v1" || cache.versions["k"] != 1 {
		t.Fatalf("cache not published: %v", cache.published)
	}
}

func TestDataSourceFailureKeepsOldValue(t *testing.T) {
	ds := newFakeDataSource()
	cache := newFakeCache()
	injected := errors.New("injected ds failure")
	var failFlag atomic.Bool
	s := New(Options{
		DataSource: ds.write,
		Cache:      cache.publish,
		Fail: func(op string) error {
			if op == OpDataSourceWrite && failFlag.Load() {
				return injected
			}
			return nil
		},
	})
	defer s.Close()

	mustWrite(t, s, "w1", "k", "old")
	failFlag.Store(true)

	_, err := s.Write(context.Background(), "w2", "k", []byte("new"))
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected error, got %v", err)
	}
	got, ver, ok := s.Read("k")
	if !ok || string(got) != "old" || ver != 1 {
		t.Fatalf("old value not kept: %q, %d, %v", got, ver, ok)
	}
	if string(cache.published["k"]) != "old" {
		t.Fatalf("cache must keep old value, got %q", cache.published["k"])
	}
	if ds.callCount() != 1 {
		t.Fatalf("injected failure must not reach the data source, calls=%d", ds.callCount())
	}
}

func TestSlowOldWriteCannotOverwriteNewerVersion(t *testing.T) {
	ds := newFakeDataSource()
	cache := newFakeCache()
	gate := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	write := func(ctx context.Context, key string, value []byte) error {
		if string(value) == "old" {
			once.Do(func() { close(entered) })
			<-gate // the old write is slow inside the data source
		}
		return ds.write(ctx, key, value)
	}
	s := New(Options{DataSource: write, Cache: cache.publish})
	defer s.Close()

	oldDone := make(chan error, 1)
	go func() {
		_, err := s.Write(context.Background(), "w-old", "k", []byte("old"))
		oldDone <- err
	}()
	<-entered // old write (version 1) is in flight

	// A newer write (version 2) overtakes it and commits first.
	res := mustWrite(t, s, "w-new", "k", "new")
	if res.Version != 2 {
		t.Fatalf("new write version = %d, want 2", res.Version)
	}

	// The slow old write finishes late; it must not overwrite version 2.
	close(gate)
	if err := <-oldDone; !errors.Is(err, ErrStaleWrite) {
		t.Fatalf("old write error = %v, want ErrStaleWrite", err)
	}

	got, ver, _ := s.Read("k")
	if string(got) != "new" || ver != 2 {
		t.Fatalf("newer version overwritten: %q @%d", got, ver)
	}
	if string(cache.published["k"]) != "new" || cache.versions["k"] != 2 {
		t.Fatalf("stale value published to cache: %q @%d", cache.published["k"], cache.versions["k"])
	}
	if cache.callCount() != 1 {
		t.Fatalf("cache publishes = %d, want 1 (stale write must not publish)", cache.callCount())
	}
}

func TestReadDoesNotSeeUncommitted(t *testing.T) {
	ds := newFakeDataSource()
	release := make(chan struct{})
	entered := make(chan struct{})
	write := func(ctx context.Context, key string, value []byte) error {
		if string(value) == "v2-uncommitted" {
			close(entered)
			<-release // block while in flight
		}
		return ds.write(ctx, key, value)
	}
	s := New(Options{DataSource: write})
	defer s.Close()
	mustWrite(t, s, "seed", "k", "v1")

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Write(context.Background(), "w2", "k", []byte("v2-uncommitted"))
	}()
	<-entered // write is in flight inside the data source

	if got, ver, _ := s.Read("k"); string(got) != "v1" || ver != 1 {
		t.Fatalf("read observed uncommitted content: %q @%d", got, ver)
	}
	close(release)
	<-done
	if got, ver, _ := s.Read("k"); string(got) != "v2-uncommitted" || ver != 2 {
		t.Fatalf("committed write not visible: %q @%d", got, ver)
	}
}

func TestCachePublishFailureMarksPartialAndRetryCompletes(t *testing.T) {
	ds := newFakeDataSource()
	cache := newFakeCache()
	injected := errors.New("injected cache failure")
	var failFlag atomic.Bool
	s := New(Options{
		DataSource: ds.write,
		Cache:      cache.publish,
		Fail: func(op string) error {
			if op == OpCachePublish && failFlag.Load() {
				return injected
			}
			return nil
		},
	})
	defer s.Close()

	failFlag.Store(true)
	res, err := s.Write(context.Background(), "w1", "k", []byte("v1"))
	var perr *PartialError
	if !errors.As(err, &perr) {
		t.Fatalf("expected PartialError, got %v", err)
	}
	if res.Status != StatusPartial {
		t.Fatalf("status = %v, want partial", res.Status)
	}
	// Partial state does not deny the data source success: readers see it.
	if got, _, ok := s.Read("k"); !ok || string(got) != "v1" {
		t.Fatalf("committed value must be readable despite partial state, got %q", got)
	}

	// Retry with the same ID completes only the cache publish.
	failFlag.Store(false)
	res, err = s.Write(context.Background(), "w1", "k", []byte("v1"))
	if err != nil || res.Status != StatusCommitted {
		t.Fatalf("retry = %+v, %v", res, err)
	}
	if ds.callCount() != 1 {
		t.Fatalf("data source re-executed on retry: calls=%d", ds.callCount())
	}
	if cache.callCount() != 1 || string(cache.published["k"]) != "v1" {
		t.Fatalf("cache publish not completed by retry: calls=%d", cache.callCount())
	}
}

func TestRetrySameIDIsIdempotent(t *testing.T) {
	ds := newFakeDataSource()
	cache := newFakeCache()
	s := New(Options{DataSource: ds.write, Cache: cache.publish})
	defer s.Close()

	first := mustWrite(t, s, "same-id", "k", "v1")
	second := mustWrite(t, s, "same-id", "k", "v1")
	if first != second {
		t.Fatalf("retry returned different result: %+v vs %+v", first, second)
	}
	if ds.callCount() != 1 || cache.callCount() != 1 {
		t.Fatalf("retry re-executed: ds=%d cache=%d", ds.callCount(), cache.callCount())
	}

	// A failed write also replays its recorded outcome for the same ID.
	injected := errors.New("boom")
	var failFlag atomic.Bool
	failFlag.Store(true)
	s2 := New(Options{
		DataSource: ds.write,
		Fail: func(op string) error {
			if op == OpDataSourceWrite && failFlag.Load() {
				return injected
			}
			return nil
		},
	})
	defer s2.Close()
	if _, err := s2.Write(context.Background(), "fail-id", "k", []byte("x")); !errors.Is(err, injected) {
		t.Fatalf("first attempt err = %v", err)
	}
	callsBefore := ds.callCount()
	failFlag.Store(false)
	if _, err := s2.Write(context.Background(), "fail-id", "k", []byte("x")); !errors.Is(err, injected) {
		t.Fatalf("retry must replay recorded error, got %v", err)
	}
	if ds.callCount() != callsBefore {
		t.Fatalf("failed write retried against data source")
	}
}

func TestConcurrentSameIDWritesExecuteOnce(t *testing.T) {
	ds := newFakeDataSource()
	s := New(Options{DataSource: ds.write})
	defer s.Close()

	const n = 8
	results := make([]Result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := s.Write(context.Background(), "dup-id", "k", []byte("v"))
			if err != nil {
				t.Errorf("write %d failed: %v", i, err)
			}
			results[i] = res
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if results[i] != results[0] {
			t.Fatalf("concurrent duplicates disagree: %+v vs %+v", results[0], results[i])
		}
	}
	if ds.callCount() != 1 {
		t.Fatalf("data source calls = %d, want 1", ds.callCount())
	}
}

func TestCloseReleasesResourcesAndWaitsForInflight(t *testing.T) {
	ds := newFakeDataSource()
	release := make(chan struct{})
	entered := make(chan struct{})
	write := func(ctx context.Context, key string, value []byte) error {
		if string(value) == "v2" {
			close(entered)
			<-release
		}
		return ds.write(ctx, key, value)
	}
	s := New(Options{DataSource: write, Closer: ds})
	mustWrite(t, s, "w1", "k", "v1")

	go s.Write(context.Background(), "w2", "k", []byte("v2"))
	<-entered // write in flight

	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while a write was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-closed

	ds.mu.Lock()
	if !ds.closed || ds.closeCalls != 1 {
		t.Fatalf("resource not released exactly once: closed=%v calls=%d", ds.closed, ds.closeCalls)
	}
	ds.mu.Unlock()
	if err := s.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
	ds.mu.Lock()
	if ds.closeCalls != 1 {
		t.Fatalf("Closer released twice: %d", ds.closeCalls)
	}
	ds.mu.Unlock()
	if _, err := s.Write(context.Background(), "w3", "k", []byte("v3")); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close = %v, want ErrClosed", err)
	}
}

func TestClockIsInjectable(t *testing.T) {
	ds := newFakeDataSource()
	fixed := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	s := New(Options{DataSource: ds.write, Clock: func() time.Time { return fixed }})
	defer s.Close()

	res := mustWrite(t, s, "w1", "k", "v1")
	if !res.At.Equal(fixed) {
		t.Fatalf("Result.At = %v, want injected clock %v", res.At, fixed)
	}
}
