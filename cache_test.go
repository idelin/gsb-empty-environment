package versionedcache_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	vc "versionedcache"
)

type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func newManualClock() *manualClock {
	return &manualClock{t: time.Unix(100, 0)}
}

func (c *manualClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
	return c.t
}

type sourceCall struct {
	version int64
	retryID string
	value   string
}

type fakeSource struct {
	mu       sync.Mutex
	calls    []sourceCall
	failNext map[int64]error
	started  chan int64
	release  chan struct{}
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		failNext: make(map[int64]error),
		started:  make(chan int64, 8),
		release:  make(chan struct{}),
	}
}

func (s *fakeSource) write(_ context.Context, request vc.SourceRequest[string]) error {
	s.mu.Lock()
	s.calls = append(s.calls, sourceCall{
		version: request.Version,
		retryID: request.RetryID,
		value:   request.Value,
	})
	fail := s.failNext[request.Version]
	s.mu.Unlock()

	s.started <- request.Version
	<-s.release
	return fail
}

func (s *fakeSource) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *fakeSource) setFailure(version int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext[version] = err
}

type publishEvent struct {
	version int64
	value   string
}

type recordingPublish struct {
	mu       sync.Mutex
	events   []publishEvent
	failNext map[int64]error
}

func newRecordingPublish() *recordingPublish {
	return &recordingPublish{failNext: make(map[int64]error)}
}

func (p *recordingPublish) publish(_ context.Context, entry vc.Entry[string]) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, publishEvent{version: entry.Version, value: entry.Value})
	return p.failNext[entry.Version]
}

func (p *recordingPublish) values() []publishEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]publishEvent(nil), p.events...)
}

func (p *recordingPublish) setFailure(version int64, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failNext[version] = err
}

type writeOutcome struct {
	result vc.Result[string]
	err    error
}

func newTestStore(t *testing.T) (*vc.Store[string], *fakeSource, *recordingPublish, *manualClock) {
	t.Helper()
	clock := newManualClock()
	source := newFakeSource()
	publisher := newRecordingPublish()
	store, err := vc.New(vc.Options[string]{
		DataSource: source.write,
		Publish:    publisher.publish,
		Now:        clock.now,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, source, publisher, clock
}

func waitVersion(t *testing.T, source *fakeSource, want int64) {
	t.Helper()
	select {
	case got := <-source.started:
		if got != want {
			t.Fatalf("started source version = %d, want %d", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for source version %d", want)
	}
}

func startWrite(store *vc.Store[string], value, retryID string) <-chan writeOutcome {
	ch := make(chan writeOutcome, 1)
	go func() {
		result, err := store.Write(context.Background(), value, retryID)
		ch <- writeOutcome{result: result, err: err}
	}()
	return ch
}

func receiveOutcome(t *testing.T, ch <-chan writeOutcome) writeOutcome {
	t.Helper()
	select {
	case outcome := <-ch:
		return outcome
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for write result")
		return writeOutcome{}
	}
}

func assertNoValue(t *testing.T, store *vc.Store[string]) {
	t.Helper()
	_, ok, err := store.Read(context.Background())
	if err != nil || ok {
		t.Fatalf("Read returned ok=%v err=%v, want no cached value", ok, err)
	}
}

func assertRead(t *testing.T, store *vc.Store[string], wantVersion int64, wantValue string) {
	t.Helper()
	entry, ok, err := store.Read(context.Background())
	if err != nil || !ok {
		t.Fatalf("Read returned ok=%v err=%v", ok, err)
	}
	if entry.Version != wantVersion || entry.Value != wantValue {
		t.Fatalf("Read returned version=%d value=%q, want version=%d value=%q",
			entry.Version, entry.Value, wantVersion, wantValue)
	}
}

func TestWriteSuccessPublishesValue(t *testing.T) {
	store, source, publisher, clock := newTestStore(t)
	clock.advance(time.Second)

	outcomeCh := startWrite(store, "v1", "")
	waitVersion(t, source, 1)
	assertNoValue(t, store)

	close(source.release)
	outcome := receiveOutcome(t, outcomeCh)

	if outcome.err != nil || outcome.result.Status != vc.StatusCommitted {
		t.Fatalf("Write returned status=%s err=%v", outcome.result.Status, outcome.err)
	}
	if outcome.result.Version != 1 || outcome.result.Value != "v1" {
		t.Fatalf("result = %+v", outcome.result)
	}
	if !outcome.result.StartedAt.Equal(time.Unix(101, 0)) {
		t.Fatalf("StartedAt = %v", outcome.result.StartedAt)
	}
	assertRead(t, store, 1, "v1")
	if events := publisher.values(); len(events) != 1 || events[0].version != 1 {
		t.Fatalf("publish events = %+v", events)
	}
}

func TestSourceFailureKeepsOldValueAndReturnsError(t *testing.T) {
	store, source, _, _ := newTestStore(t)
	first := startWrite(store, "v1", "")
	waitVersion(t, source, 1)
	close(source.release)
	if outcome := receiveOutcome(t, first); outcome.err != nil {
		t.Fatalf("first Write failed: %v", outcome.err)
	}

	source.release = make(chan struct{})
	boom := errors.New("source unavailable")
	source.setFailure(2, boom)
	second := startWrite(store, "v2", "")
	waitVersion(t, source, 2)
	close(source.release)
	outcome := receiveOutcome(t, second)

	if !errors.Is(outcome.err, vc.ErrDataSourceWrite) || !errors.Is(outcome.err, boom) {
		t.Fatalf("error = %v, want ErrDataSourceWrite wrapping injected failure", outcome.err)
	}
	if outcome.result.Status != vc.StatusFailed {
		t.Fatalf("status = %s, want failed", outcome.result.Status)
	}
	assertRead(t, store, 1, "v1")
}

func TestConcurrentWritesArePublishedInVersionOrder(t *testing.T) {
	store, source, publisher, _ := newTestStore(t)
	first := startWrite(store, "old", "")
	waitVersion(t, source, 1)
	second := startWrite(store, "new", "")
	waitVersion(t, source, 2)

	assertNoValue(t, store)
	close(source.release)

	firstOutcome := receiveOutcome(t, first)
	secondOutcome := receiveOutcome(t, second)
	if firstOutcome.err != nil || secondOutcome.err != nil {
		t.Fatalf("writes returned errors: %v, %v", firstOutcome.err, secondOutcome.err)
	}
	assertRead(t, store, 2, "new")

	events := publisher.values()
	want := []publishEvent{{version: 1, value: "old"}, {version: 2, value: "new"}}
	if len(events) != len(want) {
		t.Fatalf("publish events = %+v, want %+v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("publish events = %+v, want %+v", events, want)
		}
	}
}

func TestSlowOldWriteCannotOverwriteNewVersion(t *testing.T) {
	store, source, publisher, _ := newTestStore(t)
	first := startWrite(store, "old", "")
	waitVersion(t, source, 1)

	close(source.release)
	if outcome := receiveOutcome(t, first); outcome.err != nil {
		t.Fatalf("first Write failed: %v", outcome.err)
	}

	source.release = make(chan struct{})
	second := startWrite(store, "new", "")
	waitVersion(t, source, 2)
	assertRead(t, store, 1, "old")

	close(source.release)
	if outcome := receiveOutcome(t, second); outcome.err != nil {
		t.Fatalf("second Write failed: %v", outcome.err)
	}
	assertRead(t, store, 2, "new")
	if events := publisher.values(); events[len(events)-1].value != "new" {
		t.Fatalf("last published event = %+v", events[len(events)-1])
	}
}

func TestCachePublishFailureIsPartialAndKeepsOldValue(t *testing.T) {
	store, source, publisher, _ := newTestStore(t)
	first := startWrite(store, "v1", "")
	waitVersion(t, source, 1)
	close(source.release)
	if outcome := receiveOutcome(t, first); outcome.err != nil {
		t.Fatalf("first Write failed: %v", outcome.err)
	}

	source.release = make(chan struct{})
	boom := errors.New("publisher unavailable")
	publisher.setFailure(2, boom)
	second := startWrite(store, "v2", "")
	waitVersion(t, source, 2)
	close(source.release)
	outcome := receiveOutcome(t, second)

	if !errors.Is(outcome.err, vc.ErrCachePublish) || !errors.Is(outcome.err, boom) {
		t.Fatalf("error = %v, want ErrCachePublish wrapping injected failure", outcome.err)
	}
	if outcome.result.Status != vc.StatusPartial {
		t.Fatalf("status = %s, want partial", outcome.result.Status)
	}
	assertRead(t, store, 1, "v1")
}

func TestRetryIDIsIdempotentForConcurrentAndLaterRequests(t *testing.T) {
	store, source, _, _ := newTestStore(t)
	first := startWrite(store, "same", "request-123")
	waitVersion(t, source, 1)

	retry := startWrite(store, "same", "request-123")
	time.Sleep(20 * time.Millisecond)
	close(source.release)

	firstOutcome := receiveOutcome(t, first)
	retryOutcome := receiveOutcome(t, retry)
	if firstOutcome.err != nil || retryOutcome.err != nil {
		t.Fatalf("writes returned errors: %v, %v", firstOutcome.err, retryOutcome.err)
	}
	if firstOutcome.result != retryOutcome.result {
		t.Fatalf("retry result = %+v, want %+v", retryOutcome.result, firstOutcome.result)
	}
	if source.count() != 1 {
		t.Fatalf("source call count = %d, want 1", source.count())
	}

	later := startWrite(store, "same", "request-123")
	laterOutcome := receiveOutcome(t, later)
	if laterOutcome.err != nil || laterOutcome.result.Version != 1 {
		t.Fatalf("later retry returned %+v, %v", laterOutcome.result, laterOutcome.err)
	}
	if source.count() != 1 {
		t.Fatalf("source call count = %d, want 1", source.count())
	}
}

func TestRetryIDConflictWithDifferentValue(t *testing.T) {
	store, source, _, _ := newTestStore(t)
	first := startWrite(store, "one", "request-123")
	waitVersion(t, source, 1)

	conflict := startWrite(store, "two", "request-123")
	outcome := receiveOutcome(t, conflict)
	if !errors.Is(outcome.err, vc.ErrRetryConflict) {
		t.Fatalf("error = %v, want ErrRetryConflict", outcome.err)
	}

	close(source.release)
	if outcome := receiveOutcome(t, first); outcome.err != nil {
		t.Fatalf("original Write failed: %v", outcome.err)
	}
	if source.count() != 1 {
		t.Fatalf("source call count = %d, want 1", source.count())
	}
}

func TestCloseCancelsInflightSourceAndReleasesResources(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	store, err := vc.New(vc.Options[string]{
		DataSource: func(ctx context.Context, _ vc.SourceRequest[string]) error {
			close(started)
			<-ctx.Done()
			close(finished)
			return ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	outcomes := make(chan writeOutcome, 1)
	go func() {
		result, err := store.Write(context.Background(), "v1", "")
		outcomes <- writeOutcome{result: result, err: err}
	}()
	<-started

	closeErr := make(chan error, 1)
	go func() { closeErr <- store.Close() }()

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel inflight data-source operation")
	}
	select {
	case err := <-closeErr:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after resource cleanup")
	}

	outcome := receiveOutcome(t, outcomes)
	if !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("Write error = %v, want context.Canceled", outcome.err)
	}
	if outcome.result.Status != vc.StatusFailed {
		t.Fatalf("status = %s, want failed", outcome.result.Status)
	}

	if _, _, err := store.Read(context.Background()); !errors.Is(err, vc.ErrClosed) {
		t.Fatalf("Read after Close error = %v, want ErrClosed", err)
	}
	if _, err := store.Write(context.Background(), "v2", ""); !errors.Is(err, vc.ErrClosed) {
		t.Fatalf("Write after Close error = %v, want ErrClosed", err)
	}
}

func TestNewRequiresDataSource(t *testing.T) {
	if _, err := vc.New(vc.Options[string]{}); err == nil {
		t.Fatal("New with nil DataSource returned nil error")
	}
}

type blockingVersionedSource struct {
	started       chan int64
	sourceDone    chan int64
	beforeRelease chan struct{}
	release       map[int64]chan struct{}
	mu            sync.Mutex
	calls         []sourceCall
}

func (s *blockingVersionedSource) write(_ context.Context, request vc.SourceRequest[string]) error {
	s.mu.Lock()
	s.calls = append(s.calls, sourceCall{
		version: request.Version,
		retryID: request.RetryID,
		value:   request.Value,
	})
	s.mu.Unlock()

	s.started <- request.Version
	if s.beforeRelease != nil {
		s.beforeRelease <- struct{}{}
	}
	<-s.release[request.Version]
	s.sourceDone <- request.Version
	return nil
}

func waitInt64(t *testing.T, ch <-chan int64, want int64, name string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("%s = %d, want %d", name, got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s %d", name, want)
	}
}

func TestSlowOldVersionThatFinishesLateCannotReplaceNewerVersion(t *testing.T) {
	releaseV1 := make(chan struct{})
	releaseV2 := make(chan struct{})
	source := &blockingVersionedSource{
		started:    make(chan int64, 2),
		sourceDone: make(chan int64, 2),
		release: map[int64]chan struct{}{
			1: releaseV1,
			2: releaseV2,
		},
	}
	publisher := newRecordingPublish()
	store, err := vc.New(vc.Options[string]{
		DataSource: source.write,
		Publish:    publisher.publish,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	beforeRelease := make(chan struct{}, 2)
	source.beforeRelease = beforeRelease
	first := startWrite(store, "old", "")
	<-source.started
	<-beforeRelease
	second := startWrite(store, "new", "")
	waitInt64(t, source.started, 2, "started version")
	<-beforeRelease

	close(releaseV2)
	waitInt64(t, source.sourceDone, 2, "completed source version")
	assertNoValue(t, store)

	close(releaseV1)
	firstOutcome := receiveOutcome(t, first)
	secondOutcome := receiveOutcome(t, second)
	if firstOutcome.err != nil || secondOutcome.err != nil {
		t.Fatalf("writes returned errors: %v, %v", firstOutcome.err, secondOutcome.err)
	}

	events := publisher.values()
	assertRead(t, store, 2, "new")
	if len(events) != 2 || events[0].version != 1 || events[1].version != 2 {
		t.Fatalf("publish events = %+v, want versions [1 2]", events)
	}
}
