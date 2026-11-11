package netsec

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestNewLayerValidation(t *testing.T) {
	t.Run("zero length", func(t *testing.T) {
		_, err := NewLayer(Entry{ContainerStart: 1, ParentStart: 2, Length: 0})
		if !errors.Is(err, ErrZeroLength) {
			t.Fatalf("expected ErrZeroLength, got %v", err)
		}
	})

	t.Run("container overflow", func(t *testing.T) {
		_, err := NewLayer(Entry{ContainerStart: math.MaxUint32, ParentStart: 0, Length: 2})
		if !errors.Is(err, ErrIntervalOverflow) {
			t.Fatalf("expected ErrIntervalOverflow, got %v", err)
		}
	})

	t.Run("parent overflow", func(t *testing.T) {
		_, err := NewLayer(Entry{ContainerStart: 0, ParentStart: math.MaxUint32, Length: 2})
		if !errors.Is(err, ErrIntervalOverflow) {
			t.Fatalf("expected ErrIntervalOverflow, got %v", err)
		}
	})

	t.Run("last valid interval touches the ceiling", func(t *testing.T) {
		if _, err := NewLayer(Entry{
			ContainerStart: math.MaxUint32,
			ParentStart:    math.MaxUint32,
			Length:         1,
		}); err != nil {
			t.Fatalf("interval ending at 2^32-1 must be valid: %v", err)
		}
	})

	t.Run("container overlap rejected", func(t *testing.T) {
		_, err := NewLayer(
			Entry{ContainerStart: 0, ParentStart: 0, Length: 5},
			Entry{ContainerStart: 3, ParentStart: 10, Length: 5},
		)
		if !errors.Is(err, ErrOverlappingEntries) {
			t.Fatalf("expected ErrOverlappingEntries, got %v", err)
		}
	})

	t.Run("parent overlap rejected even when containers are disjoint", func(t *testing.T) {
		_, err := NewLayer(
			Entry{ContainerStart: 0, ParentStart: 0, Length: 5},
			Entry{ContainerStart: 10, ParentStart: 3, Length: 5},
		)
		if !errors.Is(err, ErrOverlappingEntries) {
			t.Fatalf("expected ErrOverlappingEntries, got %v", err)
		}
	})

	t.Run("touching intervals are disjoint in both directions", func(t *testing.T) {
		if _, err := NewLayer(
			Entry{ContainerStart: 0, ParentStart: 0, Length: 5},
			Entry{ContainerStart: 5, ParentStart: 10, Length: 5},
		); err != nil {
			t.Fatalf("touching half-open intervals must be valid: %v", err)
		}
	})
}

func TestSingleLayer(t *testing.T) {
	layer, err := NewLayer(
		Entry{ContainerStart: 0, ParentStart: 100, Length: 5},
		Entry{ContainerStart: 8, ParentStart: 200, Length: 2},
	)
	if err != nil {
		t.Fatalf("NewLayer: %v", err)
	}

	v, err := NewChain().Publish(layer)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	res, err := v.MapInterval(0, 10)
	if err != nil {
		t.Fatalf("MapInterval: %v", err)
	}

	wantShards := []Shard{
		{ContainerStart: 0, HostStart: 100, Length: 5},
		{ContainerStart: 8, HostStart: 200, Length: 2},
	}
	wantHoles := []Hole{{ContainerStart: 5, Length: 3}}
	if !reflect.DeepEqual(res.Shards, wantShards) {
		t.Fatalf("shards = %+v, want %+v", res.Shards, wantShards)
	}
	if !reflect.DeepEqual(res.Holes, wantHoles) {
		t.Fatalf("holes = %+v, want %+v", res.Holes, wantHoles)
	}

	if host, mapped, err := v.MapID(3); err != nil || !mapped || host != 103 {
		t.Fatalf("MapID(3) = (%d,%v,%v), want (103,true,nil)", host, mapped, err)
	}
	if _, mapped, err := v.MapID(6); err != nil || mapped {
		t.Fatalf("MapID(6) = (%v,%v), want false mapped", mapped, err)
	}
}

func TestMultiLayerComposition(t *testing.T) {
	inner, err := NewLayer(
		Entry{ContainerStart: 0, ParentStart: 10, Length: 5},  // c 0-4 -> 10-14
		Entry{ContainerStart: 5, ParentStart: 20, Length: 4},  // c 5-8 -> 20-23
		Entry{ContainerStart: 10, ParentStart: 30, Length: 3}, // c 10-12 -> 30-32
	)
	if err != nil {
		t.Fatalf("inner layer: %v", err)
	}
	outer, err := NewLayer(
		Entry{ContainerStart: 10, ParentStart: 100, Length: 5}, // 10-14 -> 100-104
		Entry{ContainerStart: 21, ParentStart: 200, Length: 3}, // 21-23 -> 200-202; 20 is a hole
		Entry{ContainerStart: 30, ParentStart: 0, Length: 3},   // 30-32 -> host 0-2, a legitimate zero
	)
	if err != nil {
		t.Fatalf("outer layer: %v", err)
	}

	v, err := NewChain().Publish(inner, outer)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	res, err := v.MapInterval(0, 13)
	if err != nil {
		t.Fatalf("MapInterval: %v", err)
	}

	wantShards := []Shard{
		{ContainerStart: 0, HostStart: 100, Length: 5},
		{ContainerStart: 6, HostStart: 200, Length: 3},
		{ContainerStart: 10, HostStart: 0, Length: 3},
	}
	wantHoles := []Hole{
		{ContainerStart: 5, Length: 1},
		{ContainerStart: 9, Length: 1},
	}
	if !reflect.DeepEqual(res.Shards, wantShards) {
		t.Fatalf("shards = %+v, want %+v", res.Shards, wantShards)
	}
	if !reflect.DeepEqual(res.Holes, wantHoles) {
		t.Fatalf("holes = %+v, want %+v", res.Holes, wantHoles)
	}

	// A real mapping may legitimately target host ID 0.
	if host, mapped, err := v.MapID(10); err != nil || !mapped || host != 0 {
		t.Fatalf("MapID(10) = (%d,%v,%v), want (0,true,nil)", host, mapped, err)
	}
	// A hole must never be reported as mapped to zero (or to anything else).
	for _, holeID := range []uint32{5, 9} {
		if host, mapped, err := v.MapID(holeID); err != nil || mapped {
			t.Fatalf("MapID(%d) = (%d,%v,%v), want unmapped hole", holeID, host, mapped, err)
		}
	}
}
