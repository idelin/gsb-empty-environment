package commit

import (
	"container/heap"
	"fmt"
	"sort"
)

const (
	actRead  = 1
	actWrite = 2
	actFree  = 3
)

type traceAction struct {
	kind byte
	idx  int
}

type node struct {
	key    uint64
	label  [2]int64 // (总读费用, 读次数)，按字典序比较
	parent uint64
	action traceAction
}

type heapItem struct {
	key   uint64
	label [2]int64
}

type pq []heapItem

func (p pq) Len() int { return len(p) }
func (p pq) Less(i, j int) bool {
	if p[i].label[0] != p[j].label[0] {
		return p[i].label[0] < p[j].label[0]
	}
	return p[i].label[1] < p[j].label[1]
}
func (p pq) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p *pq) Push(x any)   { *p = append(*p, x.(heapItem)) }
func (p *pq) Pop() any {
	old := *p
	n := len(old)
	it := old[n-1]
	*p = old[:n-1]
	return it
}

// engine 持有规划所需的全部派生索引。规划全程不接触物理设备。
type engine struct {
	inst *Instance

	nTargets int
	nSources int

	sourceID    []SectorID // 源位号 -> 扇区地址
	sourceCyl   []Cylinder
	sourceIdx   map[SectorID]int
	targetCyl   []Cylinder
	targetReq   []uint16 // 写目标 t 时必须同时驻留的源集合
	targetSrc   []int    // 目标地址本身作为源时的源位号，否则 -1
	headCyl     []Cylinder
	headIdx     map[Cylinder]int
	live        []uint16 // live[W]：仍被未写目标引用的源
	overwritten []uint16 // overwritten[W]：入口值已被覆盖的源
	allW        uint16
}

func newEngine(inst *Instance) (*engine, error) {
	if err := validate(inst); err != nil {
		return nil, err
	}
	e := &engine{inst: inst, nTargets: len(inst.Targets)}

	seen := map[SectorID]bool{}
	for t := range inst.Targets {
		for f := range inst.Targets[t].Fragments {
			if src := inst.Targets[t].Fragments[f].Source; src != nil {
				seen[*src] = true
			}
		}
	}
	ids := make([]SectorID, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	e.nSources = len(ids)
	if e.nSources > 15 {
		return nil, fmt.Errorf("commit: %d distinct reference sources exceed limit 15", e.nSources)
	}
	if e.nTargets > 11 {
		return nil, fmt.Errorf("commit: %d target sectors exceed limit 11", e.nTargets)
	}
	if inst.K > 5 {
		return nil, fmt.Errorf("commit: K=%d exceeds limit 5", inst.K)
	}

	e.sourceID = ids
	e.sourceIdx = make(map[SectorID]int, e.nSources)
	e.sourceCyl = make([]Cylinder, e.nSources)
	for i, id := range ids {
		e.sourceIdx[id] = i
		e.sourceCyl[i] = inst.Cylinders[id]
	}

	e.targetCyl = make([]Cylinder, e.nTargets)
	e.targetReq = make([]uint16, e.nTargets)
	e.targetSrc = make([]int, e.nTargets)
	for t := range inst.Targets {
		tgt := &inst.Targets[t]
		e.targetCyl[t] = tgt.Sector
		e.targetSrc[t] = -1
		var req uint16
		for f := range tgt.Fragments {
			if src := tgt.Fragments[f].Source; src != nil {
				req |= 1 << uint(e.sourceIdx[*src])
			}
		}
		e.targetReq[t] = req
		if popcount16(req) > inst.K {
			return nil, infeasible(fmt.Sprintf(
				"target %d needs %d simultaneously resident sources but K=%d",
				t, popcount16(req), inst.K))
		}
	}
	for t := range inst.Targets {
		if s, ok := e.sourceIdx[inst.Targets[t].Address]; ok {
			e.targetSrc[t] = s
		}
	}

	e.headCyl = []Cylinder{inst.Initial}
	e.headIdx = map[Cylinder]int{inst.Initial: 0}
	addHead := func(c Cylinder) {
		if _, ok := e.headIdx[c]; !ok {
			e.headIdx[c] = len(e.headCyl)
			e.headCyl = append(e.headCyl, c)
		}
	}
	for t := 0; t < e.nTargets; t++ {
		addHead(e.targetCyl[t])
	}
	for s := 0; s < e.nSources; s++ {
		addHead(e.sourceCyl[s])
	}

	states := 1 << uint(e.nTargets)
	e.live = make([]uint16, states)
	e.overwritten = make([]uint16, states)
	for w := 0; w < states; w++ {
		var live, gone uint16
		for t := 0; t < e.nTargets; t++ {
			if w&(1<<uint(t)) == 0 {
				live |= e.targetReq[t]
			} else if e.targetSrc[t] >= 0 {
				gone |= 1 << uint(e.targetSrc[t])
			}
		}
		e.live[w] = live
		e.overwritten[w] = gone
	}
	e.allW = uint16(states - 1)

	for hi := range e.headCyl {
		for s := 0; s < e.nSources; s++ {
			if c := inst.ReadCost(e.headCyl[hi], e.sourceCyl[s]); c < 0 {
				return nil, fmt.Errorf("commit: negative read cost %d", c)
			}
		}
	}
	return e, nil
}

func popcount16(m uint16) int {
	c := 0
	for m != 0 {
		m &= m - 1
		c++
	}
	return c
}

// 状态键布局：W 占 0..15 位，C 占 16..31 位，磁头柱面编号占 32..47 位。
func packKey(w, c uint16, h int) uint64 {
	return uint64(w) | uint64(c)<<16 | uint64(h)<<32
}

func unpackKey(key uint64) (w, c uint16, h int) {
	w = uint16(key & 0xffff)
	c = uint16((key >> 16) & 0xffff)
	h = int((key >> 32) & 0xffff)
	return
}

func lexLess(a, b [2]int64) bool {
	if a[0] != b[0] {
		return a[0] < b[0]
	}
	return a[1] < b[1]
}

// search 运行精确 Dijkstra，完整保留每个 (W, C, 磁头) 状态的最优字典序标签。
// 读、写、释放三类边与独立枚举器同构：写保留缓存，由显式释放边腾槽；
// 释放免费且不动磁头，因此不会改变双指标，只扩展可达的缓存子集。
func (e *engine) search() (*node, map[uint64]*node, error) {
	start := packKey(0, 0, e.headIdx[e.inst.Initial])
	best := map[uint64]*node{
		start: {key: start},
	}
	queue := &pq{{key: start}}
	heap.Init(queue)

	for queue.Len() > 0 {
		it := heap.Pop(queue).(heapItem)
		cur, ok := best[it.key]
		if !ok || cur.label != it.label {
			continue // 已被替换或剔除的陈旧堆项
		}
		w, c, h := unpackKey(cur.key)
		if w == e.allW {
			return cur, best, nil
		}
		head := e.headCyl[h]

		// 写边：目标配方的全部引用源此刻同时驻留，才允许组装寄存器写入。
		for t := 0; t < e.nTargets; t++ {
			bit := uint16(1 << uint(t))
			if w&bit != 0 || c&e.targetReq[t] != e.targetReq[t] {
				continue
			}
			w2 := w | bit
			// 规范化：写后丢弃已不被任何未写目标需要的副本。这种释放是免费、
			// 不移动磁头且不可避免的，不改变双指标；仍被需要的源保留显式释放边。
			c2 := c & e.live[w2]
			h2 := e.headIdx[e.targetCyl[t]]
			e.relax(best, queue, cur, packKey(w2, c2, h2), cur.label, traceAction{actWrite, t})
		}

		// 读边：只能装入未覆盖、仍被需要且当前未驻留的入口扇区。
		for s := 0; s < e.nSources; s++ {
			bit := uint16(1 << uint(s))
			if c&bit != 0 || e.live[w]&bit == 0 || e.overwritten[w]&bit != 0 {
				continue
			}
			if popcount16(c) >= e.inst.K {
				continue
			}
			cost := e.inst.ReadCost(head, e.sourceCyl[s])
			if cost < 0 {
				return nil, nil, fmt.Errorf("commit: negative read cost %d", cost)
			}
			label := [2]int64{cur.label[0] + cost, cur.label[1] + 1}
			h2 := e.headIdx[e.sourceCyl[s]]
			e.relax(best, queue, cur, packKey(w, c|bit, h2), label, traceAction{actRead, s})
		}

		// 释放边：免费、不动磁头。允许释放任意驻留副本（之后可重新读取），
		// 与独立枚举器完全同构，保证双指标可达性一致。
		for s := 0; s < e.nSources; s++ {
			bit := uint16(1 << uint(s))
			if c&bit == 0 {
				continue
			}
			e.relax(best, queue, cur, packKey(w, c^bit, h), cur.label, traceAction{actFree, s})
		}
	}
	return nil, nil, infeasible("no ordering keeps every simultaneously-required source resident within K slots")
}
