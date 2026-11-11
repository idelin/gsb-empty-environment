package netsec

import (
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
)

func TestEmptyChainIsAllHoles(t *testing.T) {
	v, err := NewChain().Publish()
	if err != nil {
		t.Fatalf("Publish empty chain: %v", err)
	}
	res, err := v.MapInterval(10, 5)
	if err != nil {
		t.Fatalf("MapInterval: %v", err)
	}
	if len(res.Shards) != 0 {
		t.Fatalf("empty chain must produce no shards, got %+v", res.Shards)
	}
	wantHoles := []Hole{{ContainerStart: 10, Length: 5}}
	if !reflect.DeepEqual(res.Holes, wantHoles) {
		t.Fatalf("holes = %+v, want %+v", res.Holes, wantHoles)
	}

	var nilVersion *Version
	if _, err := nilVersion.MapInterval(0, 1); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("nil version query: expected ErrNoVersion, got %v", err)
	}
	if c := NewChain().Current(); c != nil {
		t.Fatalf("fresh chain Current = %v, want nil", c)
	}
	if _, err := NewChain().Publish(nil); !errors.Is(err, ErrNilLayer) {
		t.Fatalf("nil layer publish: expected ErrNilLayer, got %v", err)
	}
	if _, err := v.MapInterval(0, 0); !errors.Is(err, ErrEmptyInterval) {
		t.Fatalf("zero-length query: expected ErrEmptyInterval, got %v", err)
	}
}

func TestAtomicPublishPinsVersions(t *testing.T) {
	layerA, err := NewLayer(Entry{ContainerStart: 0, ParentStart: 100, Length: 10})
	if err != nil {
		t.Fatalf("layerA: %v", err)
	}
	layerB, err := NewLayer(Entry{ContainerStart: 0, ParentStart: 200, Length: 10})
	if err != nil {
		t.Fatalf("layerB: %v", err)
	}

	chain := NewChain()
	v1, err := chain.Publish(layerA)
	if err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	v2, err := chain.Publish(layerB)
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}

	if v1.Sequence() != 1 || v2.Sequence() != 2 {
		t.Fatalf("sequences = %d,%d, want 1,2", v1.Sequence(), v2.Sequence())
	}

	// Pinned versions keep their own view after a newer publish.
	if host, mapped, _ := v1.MapID(0); !mapped || host != 100 {
		t.Fatalf("pinned v1 changed: host=%d mapped=%v", host, mapped)
	}
	if host, mapped, _ := v2.MapID(0); !mapped || host != 200 {
		t.Fatalf("v2 host=%d mapped=%v, want 200", host, mapped)
	}
	if chain.Current().Sequence() != 2 {
		t.Fatalf("Current sequence = %d, want 2", chain.Current().Sequence())
	}

	// Concurrent publishers and pinned readers: every observed sequence is a
	// fully published version, never a half-updated chain.
	const goroutines = 8
	const iterations = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				v, err := chain.Publish(layerA)
				if err != nil {
					t.Errorf("concurrent publish: %v", err)
					return
				}
				snap := chain.Current()
				if snap == nil || snap.Sequence() == 0 {
					t.Error("observed unpublished or zero sequence")
					return
				}
				host, mapped, err := v.MapID(0)
				if err != nil || !mapped || host != 100 {
					t.Errorf("pinned publish result inconsistent: %d %v %v", host, mapped, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	final := chain.Current()
	if final.Sequence() != 2+goroutines*iterations {
		t.Fatalf("final sequence = %d, want %d", final.Sequence(), 2+goroutines*iterations)
	}
	// Old snapshots remain valid and immutable after many later publishes.
	host, mapped, err := v1.MapID(0)
	if err != nil || !mapped || host != 100 {
		t.Fatalf("v1 no longer pinned: host=%d mapped=%v err=%v", host, mapped, err)
	}
}

func TestCeilingArithmeticDoesNotWrap(t *testing.T) {
	// Intervals ending exactly at the 2^32 ceiling and shards landing at the
	// highest host IDs must work without any uint32 wrap-around.
	inner, err := NewLayer(Entry{
		ContainerStart: math.MaxUint32 - 9,
		ParentStart:    math.MaxUint32 - 4,
		Length:         5, // c [..-9, ..-5] -> p [..-4, 2^32)
	})
	if err != nil {
		t.Fatalf("inner: %v", err)
	}
	outer, err := NewLayer(Entry{
		ContainerStart: math.MaxUint32 - 4,
		ParentStart:    math.MaxUint32 - 9,
		Length:         5, // -> host [..-9, ..-5]
	})
	if err != nil {
		t.Fatalf("outer: %v", err)
	}

	v, err := NewChain().Publish(inner, outer)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	res, err := v.MapInterval(math.MaxUint32-9, 5)
	if err != nil {
		t.Fatalf("MapInterval: %v", err)
	}
	wantShards := []Shard{{
		ContainerStart: math.MaxUint32 - 9,
		HostStart:      math.MaxUint32 - 9,
		Length:         5,
	}}
	if !reflect.DeepEqual(res.Shards, wantShards) {
		t.Fatalf("shards = %+v, want %+v", res.Shards, wantShards)
	}
	if len(res.Holes) != 0 {
		t.Fatalf("unexpected holes: %+v", res.Holes)
	}

	// Querying up to the largest representable length stays in 64-bit math.
	res2, err := v.MapInterval(0, math.MaxUint32)
	if err != nil {
		t.Fatalf("max-length query: %v", err)
	}
	total := uint64(0)
	for _, s := range res2.Shards {
		total += uint64(s.Length)
	}
	for _, h := range res2.Holes {
		total += uint64(h.Length)
	}
	if total != math.MaxUint32 {
		t.Fatalf("partition total = %d, want %d", total, uint64(math.MaxUint32))
	}
}

func TestDefensiveCopies(t *testing.T) {
	entries := []Entry{{ContainerStart: 0, ParentStart: 7, Length: 3}}
	layer, err := NewLayer(entries...)
	if err != nil {
		t.Fatalf("NewLayer: %v", err)
	}
	entries[0].ParentStart = 999 // mutating the input must not affect the layer

	v, err := NewChain().Publish(layer)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if host, mapped, _ := v.MapID(1); !mapped || host != 8 {
		t.Fatalf("layer saw caller mutation: host=%d mapped=%v", host, mapped)
	}

	got := layer.Entries()
	got[0].ParentStart = 555
	if host, mapped, _ := v.MapID(1); !mapped || host != 8 {
		t.Fatalf("layer leaked mutable entries: host=%d mapped=%v", host, mapped)
	}

	layers := v.Layers()
	layers[0] = nil
	if v.layers[0] == nil {
		t.Fatal("Version.Layers returned the backing slice")
	}
}
