package netsec

import (
	"math/rand"
	"reflect"
	"testing"
)

// brutePoint is the independent oracle: map one ID through every layer,
// returning mapped=false at the first layer that has no covering entry.
func brutePoint(layers [][]Entry, id uint32) (host uint32, mapped bool) {
	cur := id
	for _, entries := range layers {
		hit := false
		for _, e := range entries {
			if cur >= e.ContainerStart && uint64(cur) < containerEnd(e) {
				cur = e.ParentStart + (cur - e.ContainerStart)
				hit = true
				break
			}
		}
		if !hit {
			return 0, false
		}
	}
	return cur, true
}

// expectedPartition walks every point of the query and groups contiguous
// points into shards (mapped with contiguous host IDs) and holes.
func expectedPartition(t *testing.T, layers [][]Entry, start, length uint32) ([]Shard, []Hole) {
	t.Helper()
	var shards []Shard
	var holes []Hole

	i := uint32(0)
	for i < length {
		id := start + i
		host, mapped := brutePoint(layers, id)
		run := uint32(1)
		for i+run < length {
			nextHost, nextMapped := brutePoint(layers, id+run)
			if nextMapped != mapped {
				break
			}
			if mapped && nextHost != host+run {
				break
			}
			run++
		}
		if mapped {
			shards = append(shards, Shard{ContainerStart: id, HostStart: host, Length: run})
		} else {
			holes = append(holes, Hole{ContainerStart: id, Length: run})
		}
		i += run
	}
	return shards, holes
}

func occupies(flags []bool, start, length uint32) bool {
	for i := uint32(0); i < length; i++ {
		if flags[start+i] {
			return true
		}
	}
	return false
}

func mark(flags []bool, start, length uint32) {
	for i := uint32(0); i < length; i++ {
		flags[start+i] = true
	}
}

// randomShrunkLayer builds a layer over a tiny ID space with disjoint
// intervals in both container and parent coordinates.
func randomShrunkLayer(t *testing.T, rng *rand.Rand, n uint32) *Layer {
	t.Helper()
	usedContainer := make([]bool, n)
	usedParent := make([]bool, n)
	var entries []Entry

	for attempt := 0; attempt < 40; attempt++ {
		start := uint32(rng.Intn(int(n)))
		length := uint32(1 + rng.Intn(int(n-start)))
		if occupies(usedContainer, start, length) {
			continue
		}
		slots := int(n-length) + 1
		found := -1
		offset := rng.Intn(slots)
		for try := 0; try < slots; try++ {
			candidate := (offset + try) % slots
			if !occupies(usedParent, uint32(candidate), length) {
				found = candidate
				break
			}
		}
		if found < 0 {
			continue
		}
		mark(usedContainer, start, length)
		mark(usedParent, uint32(found), length)
		entries = append(entries, Entry{
			ContainerStart: start,
			ParentStart:    uint32(found),
			Length:         length,
		})
	}

	layer, err := NewLayer(entries...)
	if err != nil {
		t.Fatalf("random layer should validate: %v", err)
	}
	return layer
}

func assertPartitionInvariants(t *testing.T, v *Version, res *MapResult, start, length uint32) {
	t.Helper()
	covered := uint64(0)
	var prevEnd uint64
	for _, s := range res.Shards {
		if uint64(s.ContainerStart) < prevEnd {
			t.Fatalf("shards out of order/overlap: %+v", res.Shards)
		}
		prevEnd = uint64(s.ContainerStart) + uint64(s.Length)
		covered += uint64(s.Length)
	}

	prevEnd = 0
	holeSet := map[uint32]bool{}
	for _, h := range res.Holes {
		if uint64(h.ContainerStart) < prevEnd {
			t.Fatalf("holes out of order/overlap: %+v", res.Holes)
		}
		prevEnd = uint64(h.ContainerStart) + uint64(h.Length)
		covered += uint64(h.Length)
		for i := uint32(0); i < h.Length; i++ {
			holeSet[h.ContainerStart+i] = true
		}
	}
	if covered != uint64(length) {
		t.Fatalf("partition covers %d IDs, query length is %d", covered, length)
	}

	// Point-by-point cross-check: shard points map via MapID to their host,
	// hole points stay unmapped so a hole can never be read as host ID 0.
	for _, s := range res.Shards {
		for i := uint32(0); i < s.Length; i++ {
			cid := s.ContainerStart + i
			host, mapped, err := v.MapID(cid)
			if err != nil || !mapped {
				t.Fatalf("shard ID %d reported unmapped: mapped=%v err=%v", cid, mapped, err)
			}
			if want := s.HostStart + i; host != want {
				t.Fatalf("MapID(%d) = %d, want %d", cid, host, want)
			}
		}
	}
	for cid := range holeSet {
		if host, mapped, err := v.MapID(cid); err != nil || mapped {
			t.Fatalf("hole ID %d leaked as mapped host %d", cid, host)
		}
	}
}

func TestBruteForceShrunkSpace(t *testing.T) {
	const n = uint32(48)
	rng := rand.New(rand.NewSource(1305))

	for trial := 0; trial < 300; trial++ {
		layerCount := 1 + rng.Intn(3)
		layers := make([]*Layer, layerCount)
		rawLayers := make([][]Entry, layerCount)
		for i := range layers {
			layer := randomShrunkLayer(t, rng, n)
			layers[i] = layer
			rawLayers[i] = layer.Entries()
		}

		v, err := NewChain().Publish(layers...)
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}

		// Full-space query over the shrunk ID space, compared point by point.
		full, err := v.MapInterval(0, n)
		if err != nil {
			t.Fatalf("full-space MapInterval: %v", err)
		}
		wantShards, wantHoles := expectedPartition(t, rawLayers, 0, n)
		if !reflect.DeepEqual(full.Shards, wantShards) {
			t.Fatalf("trial %d full-space shards = %+v, want %+v", trial, full.Shards, wantShards)
		}
		if !reflect.DeepEqual(full.Holes, wantHoles) {
			t.Fatalf("trial %d full-space holes = %+v, want %+v", trial, full.Holes, wantHoles)
		}
		assertPartitionInvariants(t, v, full, 0, n)

		// Every sub-interval of the shrunk space must match the oracle exactly.
		for start := uint32(0); start < n; start++ {
			for end := start + 1; end <= n; end++ {
				length := end - start
				res, err := v.MapInterval(start, length)
				if err != nil {
					t.Fatalf("MapInterval(%d,%d): %v", start, length, err)
				}
				ws, wh := expectedPartition(t, rawLayers, start, length)
				if !reflect.DeepEqual(res.Shards, ws) || !reflect.DeepEqual(res.Holes, wh) {
					t.Fatalf("trial %d interval [%d,+%d) = shards %+v holes %+v, want shards %+v holes %+v",
						trial, start, length, res.Shards, res.Holes, ws, wh)
				}
				assertPartitionInvariants(t, v, res, start, length)
			}
		}

		// Exhaustive MapID comparison against the oracle across the space.
		for id := uint32(0); id < n; id++ {
			host, mapped, err := v.MapID(id)
			if err != nil {
				t.Fatalf("MapID(%d): %v", id, err)
			}
			wantHost, wantMapped := brutePoint(rawLayers, id)
			if mapped != wantMapped || (mapped && host != wantHost) {
				t.Fatalf("trial %d MapID(%d) = (%d,%v), want (%d,%v)",
					trial, id, host, mapped, wantHost, wantMapped)
			}
			if !mapped && host != 0 {
				t.Fatalf("unmapped ID %d returned non-zero sentinel host %d", id, host)
			}
		}
	}
}
