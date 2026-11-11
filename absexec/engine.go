package absexec

import (
	"encoding/binary"
)

// Analyze runs the abstract execution and returns one finding per send node.
// A non-nil error indicates a malformed input; an exhausted budget is not an
// error, it is reported through Report.Complete and SendUnknown.
func Analyze(req Request) (*Report, error) {
	cfg := req.CFG
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if err := cfg.checkReducedAcyclic(); err != nil {
		return nil, err
	}

	a := &analyzer{
		cfg:        cfg,
		budget:     req.Budget,
		backByEdge: make(map[[2]int]int, len(cfg.BackEdges)),
		nodeByID:   make(map[int]Node, len(cfg.Nodes)),
		states:     make(map[string]*estate),
		sendReach:  make(map[int]bool),
		sendMust:   make(map[int]bitset),
	}
	for i, be := range cfg.BackEdges {
		a.backByEdge[[2]int{be.From, be.To}] = i
	}
	for _, n := range cfg.Nodes {
		a.nodeByID[n.ID] = n
	}

	exhausted := a.enumerate()

	report := &Report{Complete: !exhausted, Visited: a.visited}
	for _, n := range cfg.Nodes {
		if n.Op != OpSend {
			continue
		}
		if !a.sendReach[n.ID] {
			status := SendUnreachable
			if exhausted {
				status = SendUnknown
			}
			report.Sends = append(report.Sends, SendFinding{NodeID: n.ID, Status: status})
			continue
		}

		lo, hi := n.sendRange(cfg.BufferSize)
		gaps := a.sendMust[n.ID].gapIntervals(lo, hi)
		if len(gaps) == 0 {
			report.Sends = append(report.Sends, SendFinding{NodeID: n.ID, Status: SendSafe})
			continue
		}
		if exhausted {
			// The fixed point was not reached: the gap may shrink further,
			// so claiming uninitialized bytes would be unsound.
			report.Sends = append(report.Sends, SendFinding{NodeID: n.ID, Status: SendUnknown})
			continue
		}

		finding := SendFinding{NodeID: n.ID, Status: SendUninitialized}
		type group struct {
			path    []int
			written bitset
			missing bitset
		}
		var groups []*group
		for _, gap := range gaps {
			for target := gap.Lo; target < gap.Hi; target++ {
				path, written, ok := a.findWitness(n.ID, target)
				if !ok {
					finding = SendFinding{NodeID: n.ID, Status: SendUnknown}
					report.Complete = false
					break
				}
				var g *group
				for _, cand := range groups {
					if equalPath(cand.path, path) {
						g = cand
						break
					}
				}
				if g == nil {
					g = &group{path: path, written: written, missing: newBitset(cfg.BufferSize)}
					groups = append(groups, g)
				}
				g.missing.set(target, target+1)
			}
			if finding.Status == SendUnknown {
				break
			}
		}
		for _, g := range groups {
			// Restrict reported intervals to contiguous runs within this path.
			runs := missingRuns(g.missing, lo, hi)
			for _, iv := range runs {
				finding.Uninitialized = append(finding.Uninitialized, UninitializedInterval{
					Interval: iv,
					Counterexample: Counterexample{
						Path:          append([]int(nil), g.path...),
						MissingOnPath: g.written.gapIntervals(0, cfg.BufferSize),
					},
				})
			}
		}
		report.Sends = append(report.Sends, finding)
	}
	return report, nil
}

func equalPath(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// missingRuns returns maximal set-bit runs of missing restricted to [lo, hi).
func missingRuns(missing bitset, lo, hi int) []Interval {
	var out []Interval
	i := lo
	for i < hi {
		if !missing.has(i) {
			i++
			continue
		}
		start := i
		for i < hi && missing.has(i) {
			i++
		}
		out = append(out, Interval{Lo: start, Hi: i})
	}
	return out
}

type analyzer struct {
	cfg        *CFG
	budget     int
	visited    int
	backByEdge map[[2]int]int
	nodeByID   map[int]Node
	states     map[string]*estate
	sendReach  map[int]bool
	sendMust   map[int]bitset
}

// estate is one expanded (node, back-edge-count vector) vertex together with
// the current fixed-point must-written set of every path reaching it.
type estate struct {
	nodeID  int
	counts  []int
	must    bitset
	reached bool
}

type workItem struct {
	key string
}

// enumerate performs the must-set dataflow fixed point over the finite DAG
// obtained by unfolding back edges up to their iteration caps. It reports
// true when the budget prevented the fixed point from being reached.
func (a *analyzer) enumerate() bool {
	rootCounts := make([]int, len(a.cfg.BackEdges))
	rootKey := a.key(a.cfg.Entry, rootCounts)
	root := &estate{
		nodeID:  a.cfg.Entry,
		counts:  rootCounts,
		must:    newBitset(a.cfg.BufferSize),
		reached: true,
	}
	a.states[rootKey] = root
	worklist := []workItem{{key: rootKey}}

	for len(worklist) > 0 {
		item := worklist[0]
		worklist = worklist[1:]
		cur := a.states[item.key]

		if a.budget > 0 && a.visited >= a.budget {
			return true
		}
		a.visited++

		out := cur.must.clone()
		a.apply(a.nodeByID[cur.nodeID], out)
		if a.nodeByID[cur.nodeID].Op == OpSend {
			if !a.sendReach[cur.nodeID] {
				a.sendReach[cur.nodeID] = true
				a.sendMust[cur.nodeID] = out.clone()
			} else {
				a.sendMust[cur.nodeID].intersectInPlace(out)
			}
		}

		for _, succID := range a.nodeByID[cur.nodeID].Succ {
			counts := cur.counts
			if bi, ok := a.backByEdge[[2]int{cur.nodeID, succID}]; ok {
				if cur.counts[bi] >= a.cfg.BackEdges[bi].MaxCount {
					continue
				}
				counts = cloneCounts(cur.counts)
				counts[bi]++
			}
			skey := a.key(succID, counts)
			next, ok := a.states[skey]
			if !ok {
				next = &estate{
					nodeID:  succID,
					counts:  counts,
					must:    out.clone(),
					reached: true,
				}
				a.states[skey] = next
				worklist = append(worklist, workItem{key: skey})
				continue
			}
			if !next.reached {
				next.reached = true
				next.must = out.clone()
				worklist = append(worklist, workItem{key: skey})
				continue
			}
			// Merge: the successor must-set is the intersection over all
			// reaching paths. Re-enqueue only when the set actually shrinks,
			// which is what keeps possible writes from becoming must writes.
			if next.must.subsetOf(out) {
				continue
			}
			next.must.intersectInPlace(out)
			worklist = append(worklist, workItem{key: skey})
		}
	}
	return false
}

// apply updates written with the bytes that the node guarantees are written.
func (a *analyzer) apply(n Node, written bitset) {
	switch n.Op {
	case OpWrite:
		if n.Hi > n.Lo {
			written.set(n.Lo, n.Hi)
		}
	case OpDynWrite:
		lo, hi := n.LoMax, n.HiMin
		if hi > lo {
			if lo < 0 {
				lo = 0
			}
			if hi > a.cfg.BufferSize {
				hi = a.cfg.BufferSize
			}
			written.set(lo, hi)
		}
	}
}

// findWitness returns a concrete bounded path from entry to sendID whose
// written set misses byte target, together with that written set.
func (a *analyzer) findWitness(sendID, target int) ([]int, bitset, bool) {
	type frame struct {
		nodeID  int
		counts  []int
		written bitset
		edge    int
	}

	stack := []frame{{
		nodeID:  a.cfg.Entry,
		counts:  make([]int, len(a.cfg.BackEdges)),
		written: newBitset(a.cfg.BufferSize),
	}}
	spent := 0

	for len(stack) > 0 {
		top := len(stack) - 1
		fr := &stack[top]
		n := a.nodeByID[fr.nodeID]

		if fr.edge == 0 {
			if a.budget > 0 && spent >= a.budget {
				return nil, bitset{}, false
			}
			spent++

			a.apply(n, fr.written)

			if n.Op == OpSend && fr.nodeID == sendID && !fr.written.has(target) {
				path := make([]int, len(stack))
				for i, f := range stack {
					path[i] = f.nodeID
				}
				return append([]int(nil), path...), fr.written.clone(), true
			}
		}

		if fr.edge < len(n.Succ) {
			succID := n.Succ[fr.edge]
			fr.edge++

			counts := fr.counts
			if bi, ok := a.backByEdge[[2]int{fr.nodeID, succID}]; ok {
				if fr.counts[bi] >= a.cfg.BackEdges[bi].MaxCount {
					continue
				}
				counts = cloneCounts(fr.counts)
				counts[bi]++
			}
			stack = append(stack, frame{
				nodeID:  succID,
				counts:  counts,
				written: fr.written.clone(),
			})
			continue
		}

		stack = stack[:top]
	}
	return nil, bitset{}, false
}

func cloneCounts(in []int) []int {
	out := make([]int, len(in))
	copy(out, in)
	return out
}

// key encodes an expanded vertex (node ID + back-edge counts) as a stable
// map key. Counts are bounded by the declared iteration caps.
func (a *analyzer) key(nodeID int, counts []int) string {
	buf := make([]byte, 0, 8+len(counts)*binary.MaxVarintLen64)
	var head [8]byte
	binary.LittleEndian.PutUint64(head[:], uint64(nodeID))
	buf = append(buf, head[:]...)
	for _, c := range counts {
		var tmp [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(tmp[:], uint64(c))
		buf = append(buf, tmp[:n]...)
	}
	return string(buf)
}
