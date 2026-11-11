package netsec

import (
	"sort"
	"strconv"
	"strings"
)

// ReferenceReport 是参考模型的独立探索结论。
type ReferenceReport struct {
	Reachable bool
	Target    int
	Visited   int
	Steps     []Step
	Final     []float64
	Truncated bool
}

// ReferenceExplore 不使用 DBM：直接在离散边界网格上枚举具体估值，
// 用标准区域等价键去重，作为符号引擎的独立交叉核对。
// 严格与非严格边界的语义差异由 ProbeBoundaries 与双机器对照负责。
//
// maxStates 是探索上限；<=0 时使用默认上限。
func ReferenceExplore(m *Machine, maxStates int) (*ReferenceReport, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if maxStates <= 0 {
		maxStates = 200000
	}

	k := m.maxConsts()
	grid := delayGrid(k)

	type node struct {
		loc   int
		vals  []float64
		delay float64
		prev  *node
		edge  *Edge
	}

	rootVals := make([]float64, m.Clocks)
	if !EvalGuards(m.invariant(m.Initial), rootVals, Eps/10) {
		return &ReferenceReport{Target: -1}, nil
	}

	root := &node{loc: m.Initial, vals: rootVals}
	visited := map[string]bool{regionKey(m.Initial, rootVals, k): true}
	queue := []*node{root}

	var hit *node
	truncated := false

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if m.isError(cur.loc) {
			hit = cur
			break
		}
		if len(visited) >= maxStates {
			truncated = true
			break
		}

		for ei := range m.Edges {
			e := &m.Edges[ei]
			if e.From != cur.loc {
				continue
			}
			for _, d := range grid {
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
				queue = append(queue, &node{loc: e.To, vals: after, delay: d, prev: cur, edge: e})
			}
		}
	}

	rep := &ReferenceReport{Visited: len(visited), Target: -1, Truncated: truncated}
	if hit == nil {
		return rep, nil
	}
	if hit.prev == nil {
		// 初始位置本身就是错误位置：空见证。
		rep.Reachable = true
		rep.Target = hit.loc
		rep.Steps = []Step{}
		rep.Final = append([]float64(nil), hit.vals...)
		return rep, nil
	}

	chain := make([]*node, 0)
	for n := hit; n != nil; n = n.prev {
		chain = append(chain, n)
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	steps := make([]Step, 0, len(chain)-1)
	for idx := 1; idx < len(chain); idx++ {
		cur := chain[idx]
		e := cur.edge
		if e == nil {
			return nil, fmtError("netsec: internal reference chain broken at %d", cur.loc)
		}
		at := make([]float64, m.Clocks)
		for i := range cur.prev.vals {
			at[i] = cur.prev.vals[i] + cur.delay
		}
		steps = append(steps, Step{
			Location: cur.prev.loc,
			Event:    e.Event,
			Delay:    cur.delay,
			At:       at,
			Guards:   append([]Guard(nil), e.Guards...),
			Resets:   append([]Action(nil), e.Resets...),
			To:       e.To,
		})
	}

	rep.Reachable = true
	rep.Target = hit.loc
	rep.Steps = steps
	rep.Final = append([]float64(nil), hit.vals...)
	return rep, nil
}

// delayGrid 生成离散边界网格：每个整数边界本身、±微小偏移与 0.5 分数代表。
func delayGrid(k []int) []float64 {
	kMax := 0
	for _, v := range k {
		if v > kMax {
			kMax = v
		}
	}
	// 尾部探针需越过最大常数一个单位。
	limit := kMax + 1
	set := map[float64]bool{0: true, float64(limit): true}
	for c := 0; c <= limit; c++ {
		base := float64(c)
		set[base] = true
		set[base+Eps] = true
		set[base-Eps] = true
		set[base+0.5] = true
	}
	out := make([]float64, 0, len(set))
	for d := range set {
		if d >= 0 {
			out = append(out, d)
		}
	}
	sort.Float64s(out)
	return out
}

// regionKey 把具体估值映射到标准区域等价键：
// 整点区域 {c}、开区间区域 (c,c+1) 及尾部 (k_i,∞)，
// 另对非整点时钟记录分数部分的相对次序。
func regionKey(loc int, vals []float64, k []int) string {
	type part struct {
		integer int
		frac    int // 1 表示有正小数，0 表示整点
		tail    bool
	}
	parts := make([]part, len(vals))
	for i, v := range vals {
		v += Eps / 100 // 抵消探针浮点误差
		fl := int(v)
		if float64(fl) > v {
			fl--
		}
		frac := 1
		if abs(v-float64(fl)) < Eps*10 {
			frac = 0
		}
		ki := k[i]
		tail := v > float64(ki)+Eps*10
		integer := fl
		if tail {
			integer = ki
			frac = 0
		}
		parts[i] = part{integer: integer, frac: frac, tail: tail}
	}

	var sb strings.Builder
	sb.WriteString(strconv.Itoa(loc))
	for _, p := range parts {
		sb.WriteByte('|')
		if p.tail {
			sb.WriteByte('T')
		} else {
			sb.WriteString(strconv.Itoa(p.integer))
			sb.WriteByte('.')
			sb.WriteString(strconv.Itoa(p.frac))
		}
	}
	// 分数部分的相对次序是区域等价的一部分（仅非尾部时钟之间）。
	order := make([]int, 0)
	for i, p := range parts {
		if p.tail || p.frac == 0 {
			continue
		}
		order = append(order, i)
	}
	sort.SliceStable(order, func(a, b int) bool {
		return vals[order[a]]-float64(parts[order[a]].integer) <
			vals[order[b]]-float64(parts[order[b]].integer)
	})
	sb.WriteString("#")
	for _, idx := range order {
		sb.WriteString(strconv.Itoa(idx))
		sb.WriteByte(',')
	}
	return sb.String()
}

// BoundaryPoint 是边界处的单点探针结果。
type BoundaryPoint struct {
	Label string
	Delay float64
	Hold  bool
}

// BoundaryReport 描述严格与非严格语义在整数边界两侧的差异。
type BoundaryReport struct {
	Strict    []BoundaryPoint
	NonStrict []BoundaryPoint
}

// ProbeBoundaries 在给定起点估值上，沿同一条边的延迟空间取边界三点：
// c-微小偏移、c、c+微小偏移，分别按严格/非严格容差评估守卫与不变量。
func ProbeBoundaries(m *Machine, loc int, event string, start []float64, c int) (*BoundaryReport, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	var edge *Edge
	for ei := range m.Edges {
		if m.Edges[ei].From == loc && m.Edges[ei].Event == event {
			edge = &m.Edges[ei]
			break
		}
	}
	if edge == nil {
		return nil, fmtError("netsec: no edge %s at location %d", event, loc)
	}

	offsets := []struct {
		label string
		delta float64
	}{
		{"c-eps", -Eps},
		{"c", 0},
		{"c+eps", Eps},
	}

	rep := &BoundaryReport{}
	for _, off := range offsets {
		d := float64(c) + off.delta
		at := make([]float64, len(start))
		for i := range start {
			at[i] = start[i] + d
		}
		after := append([]float64(nil), at...)
		for _, a := range edge.Resets {
			after[a.Clock] = 0
		}
		// 严格判定：把非严格 <=/>= 视作严格比较，整数边界必为假；
		// 用小容差吸收 7.000001 这类探针浮点噪声。
		strictHold := d >= 0 &&
			evalPoint(m.invariant(loc), at, true) &&
			evalPoint(edge.Guards, at, true) &&
			evalPoint(m.invariant(edge.To), after, true)
		// 非严格判定：保留机器原始比较运算，整数边界允许成立。
		nonStrictHold := d >= 0 &&
			evalPoint(m.invariant(loc), at, false) &&
			evalPoint(edge.Guards, at, false) &&
			evalPoint(m.invariant(edge.To), after, false)

		rep.Strict = append(rep.Strict, BoundaryPoint{Label: off.label, Delay: d, Hold: strictHold})
		rep.NonStrict = append(rep.NonStrict, BoundaryPoint{Label: off.label, Delay: d, Hold: nonStrictHold})
	}
	return rep, nil
}

// evalPoint 在边界探针点上评估守卫。
// strict 为真时把非严格 <=/>= 当作严格比较处理，用于模拟严格边界语义。
func evalPoint(guards []Guard, vals []float64, strict bool) bool {
	for _, g := range guards {
		x := vals[g.Clock]
		c := float64(g.Const)
		tol := Eps / 10
		switch g.Op {
		case LE:
			if strict {
				if x >= c-tol {
					return false
				}
			} else if x > c+tol {
				return false
			}
		case LT:
			if x >= c-tol {
				return false
			}
		case GE:
			if x < c-tol {
				return false
			}
		case GT:
			if x <= c+tol {
				return false
			}
		case EQ:
			if abs(x-c) > tol {
				return false
			}
		default:
			return false
		}
	}
	return true
}
