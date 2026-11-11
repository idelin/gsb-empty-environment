package replay

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type lockTrackingStore struct {
	*MemoryStorage
	lockCalls   int
	appendCalls int
}

func (s *lockTrackingStore) Lock(ctx context.Context) error {
	s.lockCalls++
	return s.MemoryStorage.Lock(ctx)
}

func (s *lockTrackingStore) Append(ctx context.Context, rec Record) error {
	s.appendCalls++
	return s.MemoryStorage.Append(ctx, rec)
}

func TestFutureRequestNeverTouchesStorage(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	store := &lockTrackingStore{MemoryStorage: NewMemoryStorage()}
	cache := newTestCache(t, store, clock)

	farFuture := testKey(clock, 1)
	farFuture.ClientTime = clock.Now().Add(2 * time.Hour)
	if err := cache.Accept(ctx, farFuture); !errors.Is(err, ErrFuture) {
		t.Fatalf("Accept = %v, want ErrFuture", err)
	}
	if store.lockCalls != 0 || store.appendCalls != 0 {
		t.Fatalf("storage touched: locks=%d appends=%d, want 0/0", store.lockCalls, store.appendCalls)
	}
}

func TestFileStorageWatermarkMonotonicAcrossReopen(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Unix(1_700_000_000, 0))
	path := filepath.Join(t.TempDir(), "watermark.log")

	first, err := OpenFileStorage(FileConfig{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	cache := newTestCache(t, first, clock)
	high := testKey(clock, 1)
	if err := cache.Accept(ctx, high); err != nil {
		t.Fatalf("high Accept: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen with a rolled-back host clock and attempt an older ticket.
	clock.Rollback(30 * time.Minute)
	second, err := OpenFileStorage(FileConfig{Path: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	restarted := newTestCache(t, second, clock)
	old := testKey(clock, 2)
	if err := restarted.Accept(ctx, old); !errors.Is(err, ErrStale) {
		t.Fatalf("stale after reopen = %v, want ErrStale", err)
	}

	if err := second.Lock(ctx); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	records, err := second.ReadAll(ctx)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	var maxBound int64
	var watermarks int
	for _, rec := range records {
		if rec.Kind == KindWatermark {
			watermarks++
			if rec.Watermark.LowerBoundUnixNano < maxBound {
				t.Fatal("watermark regressed after reopen")
			}
			maxBound = rec.Watermark.LowerBoundUnixNano
		}
	}
	if watermarks != 1 || maxBound != high.ClientTime.UnixNano() {
		t.Fatalf("watermarks=%d maxBound=%d, want 1 at %d",
			watermarks, maxBound, high.ClientTime.UnixNano())
	}
}
