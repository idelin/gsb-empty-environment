package pagearchive

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestPlanMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 80; iteration++ {
		pageCount := 1 + rng.Intn(5)
		pageSize := 1 + rng.Intn(8)
		pages := make([][]byte, pageCount)
		for page := range pages {
			pages[page] = make([]byte, pageSize)
			rng.Read(pages[page])
		}

		options := make([][]planOption, pageCount)
		edges := []CandidateEdge{}
		for page := range pages {
			fullCost := pageSize
			options[page] = []planOption{{full: true, cost: fullCost}}
			bases := rng.Perm(pageCount)
			basesAdded := 0
			for _, base := range bases {
				if base == page || basesAdded == MaxBaseCandidates || rng.Intn(2) == 0 {
					continue
				}
				patch := canonicalPatch(pages[base], pages[page])
				options[page] = append(options[page], planOption{
					base:  base,
					cost:  len(patch),
					patch: patch,
				})
				edges = append(edges, CandidateEdge{From: base, To: page})
				basesAdded++
			}
		}

		snapshots := make([][]int, rng.Intn(3))
		for snapshot := range snapshots {
			snapshots[snapshot] = make([]int, rng.Intn(7))
			for access := range snapshots[snapshot] {
				snapshots[snapshot][access] = rng.Intn(pageCount)
			}
		}

		budget := pageCount * pageSize
		if rng.Intn(3) == 0 {
			budget -= rng.Intn(3)
		}
		input := PlanInput{
			Pages:          pages,
			Snapshots:      snapshots,
			CandidateEdges: edges,
			MetadataCost:   0,
			Budget:         budget,
			MaxDepth:       rng.Intn(MaxDependencyDepth + 1),
			CacheCapacity:  1 + rng.Intn(MaxCachePages),
		}

		got, err := Plan(input)
		wantRead, wantSize, feasible := bruteForceBest(options, snapshots, input.Budget, input.MaxDepth, input.CacheCapacity)
		if !feasible {
			if err != ErrNoFeasibleLayout {
				t.Fatalf("iteration %d: error = %v, want ErrNoFeasibleLayout", iteration, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		if got.MaxSnapshotReadBytes != wantRead || got.PersistentBytes != wantSize {
			t.Fatalf("iteration %d: got (read=%d,size=%d), want (read=%d,size=%d)", iteration, got.MaxSnapshotReadBytes, got.PersistentBytes, wantRead, wantSize)
		}

		reader, err := NewReader(got)
		if err != nil {
			t.Fatalf("iteration %d: reader: %v", iteration, err)
		}
		for page, want := range pages {
			decoded, err := reader.Read(page)
			if err != nil {
				t.Fatalf("iteration %d: read page %d: %v", iteration, page, err)
			}
			if !bytes.Equal(decoded, want) {
				t.Fatalf("iteration %d: page %d = %v, want %v", iteration, page, decoded, want)
			}
		}
	}
}

func bruteForceBest(options [][]planOption, snapshots [][]int, budget, maxDepth, cacheLimit int) (int, int, bool) {
	chosen := make([]*planOption, len(options))
	depths := make([]int, len(options))
	state := make([]uint8, len(options))
	bestRead := 0
	bestSize := 0
	found := false

	var visit func(int, int)
	visit = func(page, storage int) {
		if storage > budget {
			return
		}
		if page == len(options) {
			read := bruteForceRead(chosen, snapshots, cacheLimit)
			if !found || read < bestRead || (read == bestRead && storage < bestSize) {
				found, bestRead, bestSize = true, read, storage
			}
			return
		}

		for optionIndex := range options[page] {
			option := &options[page][optionIndex]
			depth := 0
			if !option.full {
				if state[option.base] == 1 {
					continue
				}
				if state[option.base] == 2 {
					depth = depths[option.base] + 1
					if depth > maxDepth {
						continue
					}
				} else {
					depth = 1
				}
			}
			chosen[page] = option
			depths[page] = depth
			state[page] = 1
			if refreshBruteForceDepths(chosen, depths, state, page, maxDepth) {
				visit(page+1, storage+option.cost)
			}
			state[page] = 0
			chosen[page] = nil
			depths[page] = 0
		}
	}
	visit(0, 0)
	return bestRead, bestSize, found
}

func refreshBruteForceDepths(chosen []*planOption, depths []int, state []uint8, changedPage, maxDepth int) bool {
	for page := 0; page <= changedPage; page++ {
		option := chosen[page]
		if option == nil || option.full {
			depths[page] = 0
			continue
		}
		if state[option.base] != 2 {
			depths[page] = changedPage - page + 1
			continue
		}
		depths[page] = depths[option.base] + 1
	}
	for page := 0; page <= changedPage; page++ {
		if depths[page] > maxDepth {
			return false
		}
	}
	return true
}

func bruteForceRead(chosen []*planOption, snapshots [][]int, cacheLimit int) int {
	largest := 0
	for _, accesses := range snapshots {
		cache := newLRUCache(cacheLimit)
		total := 0
		for _, page := range accesses {
			var load func(int) int
			load = func(current int) int {
				if cache.contains(current) {
					cache.touch(current)
					return 0
				}
				cost := chosen[current].cost
				if !chosen[current].full {
					cost += load(chosen[current].base)
				}
				cache.add(current)
				return cost
			}
			total += load(page)
		}
		if total > largest {
			largest = total
		}
	}
	return largest
}
