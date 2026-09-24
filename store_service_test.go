package canonicalkey

import (
	"bytes"
	"context"
	"errors"
	"hash"
	"io"
	"testing"
	"time"
)

type constantHash struct{}

func (constantHash) Write(value []byte) (int, error) { return len(value), nil }
func (constantHash) Sum(value []byte) []byte {
	return append(append([]byte(nil), value...), bytes.Repeat([]byte{1}, 32)...)
}
func (constantHash) Reset()         {}
func (constantHash) Size() int      { return 32 }
func (constantHash) BlockSize() int { return 1 }
func constantHashNew() hash.Hash    { return constantHash{} }

func TestCatalogForcedDigestCollisionRechecksCanonicalBytes(t *testing.T) {
	catalog := NewMemoryCatalog(constantHashNew)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	first, created, err := catalog.GetOrInsert(context.Background(), []byte("A"), now)
	if err != nil || !created {
		t.Fatalf("first insert = (%v, %v, %v)", first, created, err)
	}
	again, created, err := catalog.GetOrInsert(context.Background(), []byte("A"), now.Add(time.Second))
	if err != nil || created || again.Key != first.Key {
		t.Fatalf("same bytes were not reused: (%v, %v, %v)", again, created, err)
	}
	second, created, err := catalog.GetOrInsert(context.Background(), []byte("B"), now.Add(2*time.Second))
	if err != nil || !created {
		t.Fatalf("colliding insert = (%v, %v, %v)", second, created, err)
	}
	if second.Key == first.Key {
		t.Fatalf("distinct canonical bytes were assigned the same key: %s", first.Key)
	}
}

type trackedReader struct {
	body    io.Reader
	closed  bool
	readErr error
}

func newTrackedReader(value string, readErr error) *trackedReader {
	return &trackedReader{body: bytes.NewReader([]byte(value)), readErr: readErr}
}

func (reader *trackedReader) Read(value []byte) (int, error) {
	if reader.readErr != nil {
		return 0, reader.readErr
	}
	return reader.body.Read(value)
}

func (reader *trackedReader) Close() error {
	reader.closed = true
	return nil
}

func validServiceJSON() string {
	return `{"tenant":"tenant-a","parameter_type":"search","config_version":"cfg-3","params":1}`
}

func TestServiceInjectableSourceClockAndDeterministicKey(t *testing.T) {
	clockCalls := 0
	fixedNow := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	clock := func() time.Time {
		clockCalls++
		return fixedNow
	}
	catalog := NewMemoryCatalog(DefaultHash)
	service := NewService(catalog, clock, DefaultLimits())

	reader := newTrackedReader(validServiceJSON(), nil)
	result, err := service.Resolve(context.Background(), SourceFunc(func(context.Context) (io.ReadCloser, error) {
		return reader, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || clockCalls != 1 || !result.At.Equal(fixedNow) || !reader.closed {
		t.Fatalf("unexpected first result: created=%v clock=%d at=%v closed=%v", result.Created, clockCalls, result.At, reader.closed)
	}

	secondReader := newTrackedReader(validServiceJSON(), nil)
	again, err := service.Resolve(context.Background(), SourceFunc(func(context.Context) (io.ReadCloser, error) {
		return secondReader, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if again.Created || again.Key != result.Key || string(again.Canonical) != string(result.Canonical) || !secondReader.closed {
		t.Fatalf("same request did not resolve deterministically: %#v", again)
	}
}

func TestServiceFailuresStillReleaseReader(t *testing.T) {
	sentinel := errors.New("injected failure")
	service := NewService(NewMemoryCatalog(DefaultHash), func() time.Time { return time.Unix(0, 0).UTC() }, DefaultLimits())

	readFailure := newTrackedReader(validServiceJSON(), sentinel)
	_, err := service.Resolve(context.Background(), SourceFunc(func(context.Context) (io.ReadCloser, error) {
		return readFailure, nil
	}))
	if !errors.Is(err, sentinel) || !readFailure.closed {
		t.Fatalf("read failure: err=%v closed=%v", err, readFailure.closed)
	}

	canonicalFailure := newTrackedReader(validServiceJSON(), nil)
	service.Failure = func(step FailureStep) error {
		if step == FailureCanonical {
			return sentinel
		}
		return nil
	}
	_, err = service.Resolve(context.Background(), SourceFunc(func(context.Context) (io.ReadCloser, error) {
		return canonicalFailure, nil
	}))
	if !errors.Is(err, sentinel) || !canonicalFailure.closed {
		t.Fatalf("canonical failure: err=%v closed=%v", err, canonicalFailure.closed)
	}
}

func TestServiceBudgetAndContextCancelReleaseReader(t *testing.T) {
	service := NewService(NewMemoryCatalog(DefaultHash), func() time.Time { return time.Unix(0, 0).UTC() }, Limits{
		MaxInputBytes:     4,
		MaxCanonicalBytes: 100,
		MaxDepth:          2,
	})
	reader := newTrackedReader(validServiceJSON(), nil)
	_, err := service.Resolve(context.Background(), SourceFunc(func(context.Context) (io.ReadCloser, error) {
		return reader, nil
	}))
	if !errors.Is(err, ErrBudgetExceeded) || !reader.closed {
		t.Fatalf("budget failure: err=%v closed=%v", err, reader.closed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	canceledReader := newTrackedReader(validServiceJSON(), nil)
	_, err = service.Resolve(ctx, SourceFunc(func(context.Context) (io.ReadCloser, error) {
		cancel()
		return canceledReader, nil
	}))
	if !errors.Is(err, context.Canceled) || !canceledReader.closed {
		t.Fatalf("canceled context: err=%v closed=%v", err, canceledReader.closed)
	}
}
