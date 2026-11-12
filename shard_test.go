package netsec

import "testing"

func shardBoxes(shards []Shard) []Box {
	out := make([]Box, len(shards))
	for i, s := range shards {
		out[i] = s.Region
	}
	return out
}

func assertShardPartition(t *testing.T, access Box, denied []Box, shards []Shard, maxVolume int64) {
	t.Helper()

	if len(shards) == 0 {
		// Only legal when every accessible point was denied.
		accessCount := 0
		for _, p := range enumeratePoints(access) {
			isDenied := false
			for _, d := range denied {
				if d.Contains(p) {
					isDenied = true
					break
				}
			}
			if !isDenied {
				accessCount++
			}
		}
		if accessCount != 0 {
			t.Fatalf("no shards for %d accessible points", accessCount)
		}
		return
	}

	coverage := map[string]int{}
	for i, shard := range shards {
		b := shard.Region
		if b.Empty() {
			t.Fatalf("shard %d is empty", i)
		}
		vol, err := b.Volume()
		if err != nil {
			t.Fatalf("shard %d volume: %v", i, err)
		}
		if vol > maxVolume {
			t.Fatalf("shard %d volume %d exceeds max %d", i, vol, maxVolume)
		}
		for _, p := range enumeratePoints(b) {
			key := pointKey(p)
			if prev, dup := coverage[key]; dup {
				t.Fatalf("point %v duplicated in shards %d and %d", p, prev, i)
			}
			coverage[key] = i
			for _, d := range denied {
				if d.Contains(p) {
					t.Fatalf("shard %d contains denied point %v", i, p)
				}
			}
		}
	}

	accessibleCount := 0
	for _, p := range enumeratePoints(access) {
		isDenied := false
		for _, d := range denied {
			if d.Contains(p) {
				isDenied = true
				break
			}
		}
		key := pointKey(p)
		_, covered := coverage[key]
		if isDenied {
			if covered {
				t.Fatalf("denied point %v covered by shard", p)
			}
			continue
		}
		accessibleCount++
		if !covered {
			t.Fatalf("accessible point %v is a hole", p)
		}
	}
	if len(coverage) != accessibleCount {
		t.Fatalf("covered %d points; accessible %d", len(coverage), accessibleCount)
	}
}

func TestSplitExhaustiveFixedGrids(t *testing.T) {
	access := Box{Lo: []int64{0, 0}, Hi: []int64{6, 5}}
	denied := []Box{
		{Lo: []int64{2, 1}, Hi: []int64{4, 3}},
		{Lo: []int64{5, 4}, Hi: []int64{6, 5}},
	}
	for _, maxVolume := range []int64{1, 2, 3, 5, 7, 30, 1000} {
		parts, err := Subtract(access, denied...)
		if err != nil {
			t.Fatalf("Subtract: %v", err)
		}
		shards, err := Split(parts, maxVolume)
		if err != nil {
			t.Fatalf("Split(maxVolume=%d): %v", maxVolume, err)
		}
		assertShardPartition(t, access, denied, shards, maxVolume)
	}
}

func TestSplitSinglePointShards(t *testing.T) {
	access := Box{Lo: []int64{-1, -1}, Hi: []int64{3, 2}}
	parts, err := Subtract(access)
	if err != nil {
		t.Fatal(err)
	}
	shards, err := Split(parts, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := int64(4 * 3)
	if int64(len(shards)) != want {
		t.Fatalf("got %d unit shards; want %d", len(shards), want)
	}
	assertShardPartition(t, access, nil, shards, 1)
}

func TestSplitFullyDenied(t *testing.T) {
	access := Box{Lo: []int64{0, 0, 0}, Hi: []int64{3, 3, 3}}
	denied := Box{Lo: []int64{0, 0, 0}, Hi: []int64{3, 3, 3}}
	parts, err := Subtract(access, denied)
	if err != nil {
		t.Fatal(err)
	}
	shards, err := Split(parts, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 0 {
		t.Fatalf("fully denied region produced %d shards", len(shards))
	}
}

func TestSplitRejectsInvalidMaxVolume(t *testing.T) {
	access := Box{Lo: []int64{0}, Hi: []int64{4}}
	for _, maxVolume := range []int64{0, -1} {
		if _, err := Split([]Box{access}, maxVolume); err == nil {
			t.Fatalf("Split with maxVolume=%d must fail", maxVolume)
		}
	}
}

// rng is a tiny deterministic xorshift64 generator so the fuzz run is
// reproducible without seeding from the OS.
type rng struct{ state uint64 }

func (r *rng) next() uint64 {
	x := r.state
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	r.state = x
	return x
}

func (r *rng) intn(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return int64(r.next() % uint64(n))
}

func TestSplitFuzzSmallGrids(t *testing.T) {
	r := &rng{state: 0x9e3779b97f4a7c15}
	for trial := 0; trial < 200; trial++ {
		dim := 1 + int(r.intn(3)) // 1..3 dimensions
		lo := make([]int64, dim)
		hi := make([]int64, dim)
		for i := 0; i < dim; i++ {
			lo[i] = r.intn(3) - 1 // -1..1
			hi[i] = lo[i] + 1 + r.intn(4)
		}
		access := Box{Lo: lo, Hi: hi}

		denied := make([]Box, r.intn(4))
		for i := range denied {
			dlo := make([]int64, dim)
			dhi := make([]int64, dim)
			for k := 0; k < dim; k++ {
				dlo[k] = lo[k] + r.intn(hi[k]-lo[k]+1)
				dhi[k] = dlo[k] + r.intn(hi[k]-dlo[k]+1)
			}
			denied[i] = Box{Lo: dlo, Hi: dhi}
		}

		parts, err := Subtract(access, denied...)
		if err != nil {
			t.Fatalf("trial %d Subtract: %v", trial, err)
		}
		maxVolume := 1 + r.intn(8)
		shards, err := Split(parts, maxVolume)
		if err != nil {
			t.Fatalf("trial %d Split: %v", trial, err)
		}
		assertShardPartition(t, access, denied, shards, maxVolume)
	}
}

func TestSplitOneDimensionalBoundaries(t *testing.T) {
	// Powers of two and odd lengths exercise every bisection path.
	for _, length := range []int64{1, 2, 3, 7, 8, 15, 16, 17} {
		access := Box{Lo: []int64{0}, Hi: []int64{length}}
		parts, err := Subtract(access)
		if err != nil {
			t.Fatal(err)
		}
		for _, maxVolume := range []int64{1, 2, 3, length} {
			shards, err := Split(parts, maxVolume)
			if err != nil {
				t.Fatalf("length=%d max=%d: %v", length, maxVolume, err)
			}
			assertShardPartition(t, access, nil, shards, maxVolume)
			// In 1D the pieces must tile the line in order with no gap.
			cursor := access.Lo[0]
			for _, shard := range shards {
				if shard.Region.Lo[0] != cursor {
					t.Fatalf("length=%d: gap/overlap at %d", length, cursor)
				}
				cursor = shard.Region.Hi[0]
			}
			if cursor != access.Hi[0] {
				t.Fatalf("length=%d: tiling ends at %d", length, cursor)
			}
		}
	}
}
