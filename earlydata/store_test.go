package earlydata

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStore_CheckAndMark(t *testing.T) {
	clock := &atomicClock{t: testNow}
	store := NewMemoryStore()
	store.now = clock.get
	ctx := context.Background()

	if _, err := store.CheckAndMark(ctx, nil, time.Second); !errors.Is(err, ErrEmptyBinderKey) {
		t.Fatalf("empty key: err = %v", err)
	}
	if _, err := store.CheckAndMark(ctx, []byte("k"), 0); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("zero ttl: err = %v", err)
	}

	first, err := store.CheckAndMark(ctx, []byte("binder-1"), time.Minute)
	if err != nil || !first {
		t.Fatalf("first mark: first=%v err=%v", first, err)
	}
	again, err := store.CheckAndMark(ctx, []byte("binder-1"), time.Minute)
	if err != nil || again {
		t.Fatalf("second mark: first=%v err=%v", again, err)
	}

	// 不同 binder 互不影响。
	other, err := store.CheckAndMark(ctx, []byte("binder-2"), time.Minute)
	if err != nil || !other {
		t.Fatalf("other binder: first=%v err=%v", other, err)
	}

	// 超过 TTL 后旧 binder 视为首次出现。
	clock.advance(61 * time.Second)
	reborn, err := store.CheckAndMark(ctx, []byte("binder-1"), time.Minute)
	if err != nil || !reborn {
		t.Fatalf("expired binder: first=%v err=%v", reborn, err)
	}
}

func TestMemoryStore_LazySweep(t *testing.T) {
	clock := &atomicClock{t: testNow}
	store := NewMemoryStore()
	store.now = clock.get
	ctx := context.Background()

	for i := 0; i < memorySweepThreshold; i++ {
		if _, err := store.CheckAndMark(ctx, []byte{byte(i), byte(i >> 8)}, time.Minute); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	clock.advance(2 * time.Minute)
	// 下一次写入触发惰性清扫；新键存活、过期键全部移除。
	if _, err := store.CheckAndMark(ctx, []byte("fresh"), time.Minute); err != nil {
		t.Fatalf("fresh insert: %v", err)
	}
	if got := store.Len(); got != 1 {
		t.Fatalf("Len after sweep = %d, want 1", got)
	}
}
