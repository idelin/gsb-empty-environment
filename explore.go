package netsec

// 参考模型使用的微小偏移：严格边界的内侧/外侧探针。
const Eps = 1e-6

type symNode struct {
	loc   int
	vals  []float64
	delay float64
	prev  *symNode
	edge  *Edge
}

// Explore 按区域等价枚举可达区域，搜索任一错误位置。
// 每个区域由一个离散代表估值标识（整点/半点/尾部点），
// 延迟网格覆盖全部整数边界及其 ±微小偏移。
// 可达时返回带具体延迟的见证；不可达时 Witness.Reachable 为假。
func Explore(m *Machine) (*Witness, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}

	k := m.maxConsts()
	grid := delayGrid(k)

	rootVals := make([]float64, m.Clocks)
	if !EvalGuards(m.invariant(m.Initial), rootVals, Eps/10) {
		return &Witness{Reachable: false, Target: -1}, nil
	}

	root := &symNode{loc: m.Initial, vals: rootVals}
	visited := map[string]bool{regionKey(root.loc, root.vals, k): true}
	queue := []*symNode{root}

	zoneCount := 1
	edgeCount := 0
	var hit *symNode

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if m.isError(cur.loc) {
			hit = cur
			break
		}

		for ei := range m.Edges {
			e := &m.Edges[ei]
			if e.From != cur.loc {
				continue
			}
			for _, d := range grid {
				edgeCount++
				at := make([]float64, m.Clocks)
				for i := range cur.vals {
					at[i] = cur.vals[i] + d
				}
				if !EvalGuards(m.invariant(cur.loc), at, Eps/10) {
					continue
				}
				if !EvalGuards(e.Guards, at, Eps/10) {
					continue
				}
				after := append([]float64(nil), at...)
				for _, a := range e.Resets {
					after[a.Clock] = 0
				}
				if !EvalGuards(m.invariant(e.To), after, Eps/10) {
					continue
				}
				key := regionKey(e.To, after, k)
				if visited[key] {
					continue
				}
				visited[key] = true
				zoneCount++
				queue = append(queue, &symNode{
					loc:   e.To,
					vals:  after,
					delay: d,
					prev:  cur,
					edge:  e,
				})
			}
		}
	}

	w := &Witness{Zones: zoneCount, Edges: edgeCount, Target: -1}
	if hit == nil {
		return w, nil
	}

	chain := make([]*symNode, 0)
	for n := hit; n != nil; n = n.prev {
		chain = append(chain, n)
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	steps, finalVals, err := buildSteps(m, chain)
	if err != nil {
		return nil, err
	}
	w.Reachable = true
	w.Target = hit.loc
	w.Steps = steps
	w.Final = finalVals
	return w, nil
}

// buildSteps 从区域节点链重建见证：每跳延迟已在 BFS 中确定，
// 触发时估值由上一跳节点估值加延迟得到。
func buildSteps(m *Machine, chain []*symNode) ([]Step, []float64, error) {
	steps := make([]Step, 0, len(chain)-1)
	curVals := make([]float64, m.Clocks)

	for idx := 1; idx < len(chain); idx++ {
		from := chain[idx-1]
		to := chain[idx]
		if to.edge == nil || to.edge.From != from.loc || to.edge.To != to.loc {
			return nil, nil, fmtError("netsec: internal witness chain broken at step %d", idx)
		}
		e := to.edge
		at := make([]float64, m.Clocks)
		for i := range curVals {
			at[i] = curVals[i] + to.delay
		}
		steps = append(steps, Step{
			Location: from.loc,
			Event:    e.Event,
			Delay:    to.delay,
			At:       at,
			Guards:   append([]Guard(nil), e.Guards...),
			Resets:   append([]Action(nil), e.Resets...),
			To:       e.To,
		})
		curVals = append([]float64(nil), to.vals...)
	}
	return steps, append([]float64(nil), curVals...), nil
}

// Replay 独立复放见证：每步检查非负延迟、沿途不变量、守卫与归零结果。
func Replay(m *Machine, w *Witness) error {
	if err := m.Validate(); err != nil {
		return err
	}
	vals := make([]float64, m.Clocks)
	loc := m.Initial

	for si, s := range w.Steps {
		if s.Location != loc {
			return fmtError("netsec: replay step %d at location %d, expected %d", si, s.Location, loc)
		}
		if s.Delay < -Eps {
			return fmtError("netsec: replay step %d negative delay %v", si, s.Delay)
		}
		for i := range vals {
			vals[i] += s.Delay
		}
		if !sameVals(vals, s.At, Eps) {
			return fmtError("netsec: replay step %d valuation mismatch", si)
		}
		if !EvalGuards(m.invariant(loc), vals, Eps*2) {
			return fmtError("netsec: replay step %d violates invariant of %s", si, m.Locations[loc])
		}
		if !EvalGuards(s.Guards, vals, Eps*2) {
			return fmtError("netsec: replay step %d guards fail at %s", si, m.Locations[loc])
		}
		var edge *Edge
		for ei := range m.Edges {
			cand := &m.Edges[ei]
			if cand.From == loc && cand.Event == s.Event {
				edge = cand
				break
			}
		}
		if edge == nil {
			return fmtError("netsec: replay step %d no edge %s at %s", si, s.Event, m.Locations[loc])
		}
		for _, a := range s.Resets {
			vals[a.Clock] = 0
		}
		if !EvalGuards(m.invariant(s.To), vals, Eps*2) {
			return fmtError("netsec: replay step %d violates invariant of %s", si, m.Locations[s.To])
		}
		loc = s.To
	}

	if w.Target != loc || !m.isError(loc) {
		return fmtError("netsec: replay ends at %d, not declared error target %d", loc, w.Target)
	}
	if !sameVals(vals, w.Final, Eps) {
		return fmtError("netsec: replay final valuation mismatch")
	}
	return nil
}

func sameVals(a, b []float64, eps float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if abs(a[i]-b[i]) > eps {
			return false
		}
	}
	return true
}
