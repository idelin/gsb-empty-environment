package archive

import (
	"container/heap"
	"errors"
	"math/bits"
	"math/rand"
	"testing"
)

type bruteState struct {
	cache   uint32
	emitted uint32
}

type bruteItem struct {
	st   bruteState
	cost int
}

type bruteHeap []bruteItem

func (h bruteHeap) Len() int            { return len(h) }
func (h bruteHeap) Less(i, j int) bool  { return h[i].cost < h[j].cost }
func (h bruteHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *bruteHeap) Push(x interface{}) { *h = append(*h, x.(bruteItem)) }
func (h *bruteHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func TestExactPlannerMatchesFullSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(20261126))
	for iter := 0; iter < 200; iter++ {
		nblocks := 2 + rng.Intn(3)
		sizes := make([]uint64, nblocks)
		var total uint64
		for i := range sizes {
			sizes[i] = uint64(1 + rng.Intn(3))
			total += sizes[i]
		}
		m, _ := concatRawManifest(t, sizes...)
		g := NewGrant("tenant", Interval{0, total})
		nreq := 1 + rng.Intn(4)
		batch := make([]Request, nreq)
		for i := range batch {
			start := uint64(rng.Intn(int(total)))
			length := uint64(1 + rng.Intn(int(total-start)))
			batch[i] = Request{Start: start, Length: length}
		}
		capacity := uint64(1 + rng.Intn(nblocks+1))
		opts := DefaultPlanOptions(capacity)
		opts.ExactLimit = 9
		opts.PreferRequestOrder = false

		needs := make([][]BlockID, len(batch))
		for i, r := range batch {
			parts, err := Resolve(m, r.Start, r.Start+r.Length)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			needs[i] = SourceBlocks(parts)
		}

		// Optional genuine initial cache.
		var checkpoint *CacheCheckpoint
		if rng.Intn(2) == 0 {
			resident := make([]BlockID, 0, int(capacity))
			for id := BlockID(1); id <= BlockID(nblocks); id++ {
				if rng.Intn(3) == 0 && uint64(len(resident)) < capacity {
					resident = append(resident, id)
				}
			}
			if len(resident) > 0 {
				checkpoint = &CacheCheckpoint{Version: m.Version, StrategyDigest: m.StrategyDigest, Resident: resident}
				opts.Checkpoint = checkpoint
			}
		}

		var initial []BlockID
		if checkpoint != nil {
			initial = checkpoint.Resident
		}
		brute, ok := bruteWithInitial(needs, initial, capacity)

		p, err := PlanBatch(m, g, batch, opts)
		if !ok {
			if !errors.Is(err, ErrNoFeasiblePlan) {
				t.Fatalf("iter %d: brute says infeasible, planner returned %v", iter, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("iter %d: brute cost %d, planner error %v\nneeds=%v cap=%d initial=%v",
				iter, brute, err, needs, capacity, initial)
		}
		if int(p.DecodeBlocks) != brute {
			t.Fatalf("iter %d: planner=%d brute=%d\nneeds=%v cap=%d initial=%v",
				iter, p.DecodeBlocks, brute, needs, capacity, initial)
		}
	}
}

func TestLargerBatchExactSearchMatchesBrute(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	for iter := 0; iter < 25; iter++ {
		sizes := []uint64{2, 3, 2, 3}
		m, _ := concatRawManifest(t, sizes...)
		var total uint64
		for _, s := range sizes {
			total += s
		}
		g := NewGrant("tenant", Interval{0, total})
		batch := make([]Request, 10)
		for i := range batch {
			start := uint64(rng.Intn(int(total)))
			length := uint64(1 + rng.Intn(int(total-start)))
			batch[i] = Request{Start: start, Length: length}
		}
		capacity := uint64(1 + rng.Intn(3))
		needs := make([][]BlockID, len(batch))
		for i, r := range batch {
			parts, err := Resolve(m, r.Start, r.Start+r.Length)
			if err != nil {
				t.Fatal(err)
			}
			needs[i] = SourceBlocks(parts)
		}
		brute, ok := bruteWithInitial(needs, nil, capacity)
		opts := DefaultPlanOptions(capacity)
		opts.ExactLimit = 8 // force the larger-batch exact state search
		opts.PreferRequestOrder = false
		p, err := PlanBatch(m, g, batch, opts)
		if !ok {
			if !errors.Is(err, ErrNoFeasiblePlan) {
				t.Fatalf("iter %d: brute infeasible, planner %v", iter, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("iter %d: brute=%d planner err %v needs=%v cap=%d", iter, brute, err, needs, capacity)
		}
		if int(p.DecodeBlocks) != brute {
			t.Fatalf("iter %d: planner=%d brute=%d needs=%v cap=%d", iter, p.DecodeBlocks, brute, needs, capacity)
		}
		if _, err := CheckPlan(m, g, batch, p); err != nil {
			t.Fatalf("iter %d: exact-state plan failed audit: %v", iter, err)
		}
	}
}

func bruteWithInitial(needs [][]BlockID, initial []BlockID, capacity uint64) (int, bool) {
	return bruteMinDecodesFrom(needs, capacity, initial)
}

func bruteMinDecodesFrom(needs [][]BlockID, capacity uint64, initial []BlockID) (int, bool) {
	all := make(map[BlockID]int)
	for _, list := range needs {
		for _, id := range list {
			all[id] = 0
		}
	}
	for _, id := range initial {
		if _, ok := all[id]; !ok {
			all[id] = 0
		}
	}
	k := 0
	for id := range all {
		all[id] = k
		k++
	}
	needMask := make([]uint32, len(needs))
	for i, list := range needs {
		for _, id := range list {
			needMask[i] |= 1 << uint(all[id])
		}
	}
	var startCache uint32
	for _, id := range initial {
		if idx, ok := all[id]; ok {
			startCache |= 1 << uint(idx)
		}
	}
	goal := uint32(1)<<uint(len(needs)) - 1
	cacheLimit := capacity
	if cacheLimit > uint64(k) {
		cacheLimit = uint64(k)
	}
	start := bruteState{cache: startCache}
	dist := map[bruteState]int{start: 0}
	h := &bruteHeap{{st: start, cost: 0}}
	for h.Len() > 0 {
		it := heap.Pop(h).(bruteItem)
		if d, ok := dist[it.st]; !ok || d != it.cost {
			continue
		}
		if it.st.emitted == goal {
			return it.cost, true
		}
		for i := range needs {
			bit := uint32(1) << uint(i)
			if it.st.emitted&bit == 0 && needMask[i]&^it.st.cache == 0 {
				ns := bruteState{cache: it.st.cache, emitted: it.st.emitted | bit}
				if old, ok := dist[ns]; !ok || old > it.cost {
					dist[ns] = it.cost
					heap.Push(h, bruteItem{st: ns, cost: it.cost})
				}
			}
		}
		for b := 0; b < k; b++ {
			bit := uint32(1) << uint(b)
			if it.st.cache&bit != 0 {
				continue
			}
			if uint64(bits.OnesCount32(it.st.cache)) < cacheLimit {
				ns := bruteState{cache: it.st.cache | bit, emitted: it.st.emitted}
				if old, ok := dist[ns]; !ok || old > it.cost+1 {
					dist[ns] = it.cost + 1
					heap.Push(h, bruteItem{st: ns, cost: it.cost + 1})
				}
				continue
			}
			for v := 0; v < k; v++ {
				vbit := uint32(1) << uint(v)
				if it.st.cache&vbit == 0 {
					continue
				}
				ns := bruteState{cache: (it.st.cache &^ vbit) | bit, emitted: it.st.emitted}
				if old, ok := dist[ns]; !ok || old > it.cost+1 {
					dist[ns] = it.cost + 1
					heap.Push(h, bruteItem{st: ns, cost: it.cost + 1})
				}
			}
		}
	}
	return 0, false
}
