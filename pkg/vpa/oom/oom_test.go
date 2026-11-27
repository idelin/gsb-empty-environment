package oom

import (
	"testing"
	"time"
)

func TestBumpMemoryTakesMax(t *testing.T) {
	b := NewBumper(DefaultBumpUpRatio, DefaultMinBumpUp, DefaultWindow)

	// Large limit: multiplicative branch dominates.
	large := int64(10 * 1024 * 1024 * 1024)
	if got := b.BumpMemory(large); got != int64(float64(large)*1.2+0.5) {
		t.Fatalf("large bump = %d", got)
	}

	// Small limit: additive branch dominates (limit + 100Mi > limit*1.2).
	small := int64(10 * 1024 * 1024)
	if got := b.BumpMemory(small); got != small+DefaultMinBumpUp {
		t.Fatalf("small bump = %d, want %d", got, small+DefaultMinBumpUp)
	}
}

func TestSlidingWindowExpiry(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	window := time.Hour
	b := NewBumper(DefaultBumpUpRatio, DefaultMinBumpUp, window)

	first := int64(200 * 1024 * 1024)
	second := int64(500 * 1024 * 1024)
	b.Record(Event{Memory: first, At: base})
	b.Record(Event{Memory: second, At: base.Add(30 * time.Minute)})

	floor, ok := b.BumpFloor(base.Add(45 * time.Minute))
	if !ok {
		t.Fatal("expected active OOM floor")
	}
	wantFloor := b.BumpMemory(second)
	if floor != wantFloor {
		t.Fatalf("floor = %d, want highest bump %d", floor, wantFloor)
	}
	if len(b.ActiveEvents(base.Add(45*time.Minute))) != 2 {
		t.Fatal("both events should still be active")
	}

	// After the window passes the first event expires, but the second remains.
	floor, ok = b.BumpFloor(base.Add(75 * time.Minute))
	if !ok || floor != wantFloor {
		t.Fatalf("floor after first expiry = %d (ok=%v)", floor, ok)
	}

	// Once every event expires the floor disappears entirely.
	if _, ok := b.BumpFloor(base.Add(2 * window)); ok {
		t.Fatal("expected no active OOM events after window")
	}
}

func TestBumpFloorMonotonicPeak(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	b := NewBumper(DefaultBumpUpRatio, DefaultMinBumpUp, DefaultWindow)

	b.Record(Event{Memory: 1 << 30, At: base})
	b.Record(Event{Memory: 256 << 20, At: base.Add(time.Minute)})

	// Even though the second OOM was smaller, the floor stays at the highest
	// bumped peak seen inside the window.
	floor, ok := b.BumpFloor(base.Add(2 * time.Minute))
	if !ok || floor != b.BumpMemory(1<<30) {
		t.Fatalf("peak floor = %d, ok=%v", floor, ok)
	}
}
