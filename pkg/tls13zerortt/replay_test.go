package tls13zerortt

import (
	"errors"
	"testing"
	"time"
)

func TestExactReplayFilterSingleUse(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	f, err := NewExactReplayFilter(time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if v := f.CheckAndRegister("ticket-a", base); v != ReplayFirstUse {
		t.Fatalf("first use = %v, want FirstUse", v)
	}
	if v := f.CheckAndRegister("ticket-a", base.Add(10*time.Second)); v != ReplayDuplicate {
		t.Fatalf("second use = %v, want Duplicate", v)
	}
	// A distinct identity remains first-use.
	if v := f.CheckAndRegister("ticket-b", base.Add(11*time.Second)); v != ReplayFirstUse {
		t.Fatalf("other ticket = %v, want FirstUse", v)
	}
}

func TestExactReplayFilterBucketRotation(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	window := time.Minute
	f, _ := NewExactReplayFilter(window)

	// Accept near the end of bucket N.
	late := base.Truncate(window).Add(window - time.Second)
	if v := f.CheckAndRegister("k", late); v != ReplayFirstUse {
		t.Fatalf("late first use = %v", v)
	}
	// Replay at the start of bucket N+1 must still be detected (previous
	// bucket retained).
	early := late.Add(2 * time.Second)
	if v := f.CheckAndRegister("k", early); v != ReplayDuplicate {
		t.Fatalf("boundary replay = %v, want Duplicate", v)
	}

	// After rotating two windows forward, old state is dropped: identity is
	// fresh again (bounded memory window), and Rotate does not panic.
	f.Rotate(early.Add(2 * window))
	if v := f.CheckAndRegister("k", early.Add(2*window)); v != ReplayFirstUse {
		t.Fatalf("post-expiry use = %v, want FirstUse", v)
	}
}

func TestBoundedReplayFilterSaturationFailsClosed(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	f, err := NewBoundedReplayFilter(time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}

	if v := f.CheckAndRegister("a", base); v != ReplayFirstUse {
		t.Fatalf("a = %v", v)
	}
	if v := f.CheckAndRegister("b", base); v != ReplayFirstUse {
		t.Fatalf("b = %v", v)
	}
	// Capacity reached: a brand-new identity must fail closed, not be admitted.
	if v := f.CheckAndRegister("c", base); v != ReplaySaturated {
		t.Fatalf("c = %v, want Saturated", v)
	}
	// Known identities are still reliably detected as replays.
	if v := f.CheckAndRegister("a", base); v != ReplayDuplicate {
		t.Fatalf("a replay = %v, want Duplicate", v)
	}

	// Rotating into the next bucket keeps the saturated previous bucket
	// retained for one window: an unseen identity still cannot be proven
	// fresh and must fail closed.
	if v := f.CheckAndRegister("c", base.Add(time.Minute)); v != ReplaySaturated {
		t.Fatalf("c next window = %v, want Saturated", v)
	}
	// Once the saturated bucket is retired (two windows on), capacity returns.
	if v := f.CheckAndRegister("c", base.Add(2*time.Minute)); v != ReplayFirstUse {
		t.Fatalf("c after retirement = %v, want FirstUse", v)
	}
}

func TestReplayFilterConfigValidation(t *testing.T) {
	if _, err := NewExactReplayFilter(0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("exact zero window = %v", err)
	}
	if _, err := NewBoundedReplayFilter(time.Minute, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("bounded zero capacity = %v", err)
	}
}

func TestReplayFilterConcurrent(t *testing.T) {
	f, _ := NewExactReplayFilter(time.Minute)
	now := time.Unix(1_700_000_000, 0)
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 200; i++ {
				f.CheckAndRegister("shared", now)
			}
		}()
	}
	for g := 0; g < 8; g++ {
		<-done
	}
	// Race detector validates locking; the identity must remain registered.
	if v := f.CheckAndRegister("shared", now); v != ReplayDuplicate {
		t.Fatalf("post-concurrency = %v, want Duplicate", v)
	}
}

func TestBoundedRegistryRotationClearsSaturation(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	f, _ := NewBoundedReplayFilter(time.Minute, 1)
	f.CheckAndRegister("a", base)
	if v := f.CheckAndRegister("b", base); v != ReplaySaturated {
		t.Fatalf("saturated bucket = %v", v)
	}
	// Jump multiple windows: all retained state is dropped and capacity
	// returns, proving bounded memory is reclaimed on rotation.
	far := base.Add(3 * time.Minute)
	if v := f.CheckAndRegister("a", far); v != ReplayFirstUse {
		t.Fatalf("post multi-window jump = %v, want FirstUse", v)
	}
}

func TestExactRegistryMultiWindowJump(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	f, _ := NewExactReplayFilter(time.Minute)
	f.CheckAndRegister("k", base)
	f.Rotate(base.Add(5 * time.Minute))
	if v := f.CheckAndRegister("k", base.Add(5*time.Minute)); v != ReplayFirstUse {
		t.Fatalf("after 5-window jump = %v, want FirstUse", v)
	}
}

func TestBoundedSaturatedPreviousBucketFailsClosed(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	f, _ := NewBoundedReplayFilter(time.Minute, 1)

	// Fill bucket N to capacity, then overflow it to mark saturation.
	f.CheckAndRegister("a", base)
	if v := f.CheckAndRegister("overflow", base); v != ReplaySaturated {
		t.Fatalf("overflow = %v, want Saturated", v)
	}

	// In bucket N+1, an identity absent from the saturated previous bucket
	// cannot be proven fresh: must fail closed, not register first use.
	next := base.Add(time.Minute)
	if v := f.CheckAndRegister("brand-new", next); v != ReplaySaturated {
		t.Fatalf("previous-saturation miss = %v, want Saturated", v)
	}

	// A known identity from the previous bucket is still a definite replay.
	if v := f.CheckAndRegister("a", next); v != ReplayDuplicate {
		t.Fatalf("known identity across boundary = %v, want Duplicate", v)
	}

	// Two windows later the saturated bucket is fully retired; freshness can
	// again be established.
	if v := f.CheckAndRegister("brand-new", next.Add(time.Minute)); v != ReplayFirstUse {
		t.Fatalf("after retiring saturated bucket = %v, want FirstUse", v)
	}
}
