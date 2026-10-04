package main

import (
	"fmt"
	"strings"
)

// Problem 描述一个无环路由更新规划实例。
// 每个节点对每条目的节点各有一个旧下一跳和一个新下一跳。
// 状态用位掩码表示: 第 i 位为 1 表示节点 i 已切换到新路由。
type Problem struct {
	Nodes []string
	Dests []string
	Old   map[string]map[string]string // node -> dest -> nexthop
	New   map[string]map[string]string

	nodeIdx    map[string]int
	usefulMask uint64 // 旧/新下一跳存在差异、需要更新的节点
	useful     []int

	safeCache map[uint64]bool
}

// NewProblem 校验输入并构造规划问题。
func NewProblem(nodes, dests []string, oldT, newT map[string]map[string]string) (*Problem, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("节点列表为空")
	}
	if len(nodes) > 18 {
		return nil, fmt.Errorf("节点数 %d 超过上限 18", len(nodes))
	}
	p := &Problem{Nodes: nodes, Dests: dests, Old: oldT, New: newT, nodeIdx: make(map[string]int)}
	for i, n := range nodes {
		if _, dup := p.nodeIdx[n]; dup {
			return nil, fmt.Errorf("节点 %q 重复", n)
		}
		p.nodeIdx[n] = i
	}
	if len(dests) == 0 {
		return nil, fmt.Errorf("目的节点列表为空")
	}
	for _, d := range dests {
		if _, ok := p.nodeIdx[d]; !ok {
			return nil, fmt.Errorf("目的节点 %q 不在节点列表中", d)
		}
	}
	tables := []struct {
		name string
		t    map[string]map[string]string
	}{{"old", oldT}, {"new", newT}}
	for _, tb := range tables {
		for _, u := range nodes {
			for _, d := range dests {
				if u == d {
					continue
				}
				nh, ok := tb.t[u][d]
				if !ok {
					return nil, fmt.Errorf("%s 表缺少节点 %s 到目的 %s 的下一跳", tb.name, u, d)
				}
				if _, ok := p.nodeIdx[nh]; !ok {
					return nil, fmt.Errorf("%s 表中节点 %s 到目的 %s 的下一跳 %q 不是已知节点", tb.name, u, d, nh)
				}
			}
		}
	}
	for i, u := range nodes {
		for _, d := range dests {
			if u == d {
				continue
			}
			if oldT[u][d] != newT[u][d] {
				p.usefulMask |= 1 << uint(i)
				p.useful = append(p.useful, i)
				break
			}
		}
	}
	return p, nil
}

// nextHop 返回状态 state 下节点 u 到目的节点 d (节点下标) 的下一跳节点下标。
func (p *Problem) nextHop(state uint64, u, d int) (int, bool) {
	node := p.Nodes[u]
	dest := p.Nodes[d]
	var nh string
	var ok bool
	if state>>uint(u)&1 == 1 {
		nh, ok = p.New[node][dest]
	} else {
		nh, ok = p.Old[node][dest]
	}
	if !ok {
		return 0, false
	}
	v, ok := p.nodeIdx[nh]
	return v, ok
}

// DestEvidence 记录某一目的节点在给定状态下的可达性与环检测证据。
type DestEvidence struct {
	Dest     string
	OK       bool
	Reached  int
	Total    int
	Paths    []string // 每个源节点到目的的转发路径
	Failures []string // 不可达/成环的文字描述
	Cycle    []string // 发现的第一个转发环 (含首尾重复节点)
}

// checkDest 检查状态 state 下所有节点到目的节点 d (节点下标) 的转发图:
// 从每个源节点沿下一跳游走, 检测转发环与黑洞, 并记录路径证据。
func (p *Problem) checkDest(state uint64, d int) DestEvidence {
	n := len(p.Nodes)
	ev := DestEvidence{Dest: p.Nodes[d], OK: true}
	for s := 0; s < n; s++ {
		if s == d {
			continue
		}
		ev.Total++
		var path []int
		pos := make([]int, n)
		for i := range pos {
			pos[i] = -1
		}
		cur := s
		ok := false
		blackhole := false
		var cycle []int
		for {
			if cur == d {
				ok = true
				break
			}
			if pos[cur] >= 0 { // 再次访问 => 转发环
				cycle = append([]int(nil), path[pos[cur]:]...)
				break
			}
			pos[cur] = len(path)
			path = append(path, cur)
			nh, has := p.nextHop(state, cur, d)
			if !has {
				blackhole = true
				break
			}
			cur = nh
		}
		names := make([]string, 0, len(path)+1)
		for _, v := range path {
			names = append(names, p.Nodes[v])
		}
		switch {
		case ok:
			ev.Paths = append(ev.Paths, strings.Join(append(names, p.Nodes[d]), " -> "))
			ev.Reached++
		case len(cycle) > 0:
			cyc := make([]string, 0, len(cycle)+1)
			for _, v := range cycle {
				cyc = append(cyc, p.Nodes[v])
			}
			cyc = append(cyc, p.Nodes[cycle[0]])
			if ev.Cycle == nil {
				ev.Cycle = cyc
			}
			ev.Failures = append(ev.Failures, fmt.Sprintf("%s: 进入转发环 %s", p.Nodes[s], strings.Join(cyc, " -> ")))
			ev.OK = false
		case blackhole:
			ev.Failures = append(ev.Failures, fmt.Sprintf("%s: 沿 %s 转发后无下一跳 (黑洞)", p.Nodes[s], strings.Join(names, " -> ")))
			ev.OK = false
		default:
			ev.Failures = append(ev.Failures, fmt.Sprintf("%s: 不可达 %s", p.Nodes[s], p.Nodes[d]))
			ev.OK = false
		}
	}
	return ev
}

// checkState 返回状态 state 对所有目的节点是否均无环且全可达, 以及逐项证据。
func (p *Problem) checkState(state uint64) (bool, []DestEvidence) {
	ok := true
	evs := make([]DestEvidence, 0, len(p.Dests))
	for d := range p.Dests {
		ev := p.checkDest(state, p.nodeIdx[p.Dests[d]])
		if !ev.OK {
			ok = false
		}
		evs = append(evs, ev)
	}
	return ok, evs
}

// safe 带缓存地判断状态是否安全 (无环且全可达)。
func (p *Problem) safe(state uint64) bool {
	if v, ok := p.safeCache[state]; ok {
		return v
	}
	ok, _ := p.checkState(state)
	p.safeCache[state] = ok
	return ok
}

// Move 表示一步更新: Group 为本步同步更新的节点集合, State 为更新后的状态。
type Move struct {
	Group uint64
	State uint64
}

// FindSchedule 搜索每步至多同步更新 maxGroup 个节点的安全更新方案。
// 返回更新序列、是否成功、以及搜索过程中访问的状态数 (作为穷举证据)。
func (p *Problem) FindSchedule(maxGroup int) ([]Move, bool, int) {
	p.safeCache = make(map[uint64]bool)
	dead := make(map[uint64]bool)
	explored := 0
	var path []Move
	var rec func(state uint64) bool
	rec = func(state uint64) bool {
		explored++
		remaining := p.usefulMask &^ state
		if remaining == 0 {
			return true
		}
		if dead[state] {
			return false
		}
		bits := bitsOf(remaining)
		maxSize := maxGroup
		if len(bits) < maxSize {
			maxSize = len(bits)
		}
		// 优先小组 (单节点优先), 组内按字典序尝试
		for size := 1; size <= maxSize; size++ {
			found := false
			combinations(bits, size, func(group uint64) bool {
				ns := state | group
				if p.safe(ns) && rec(ns) {
					path = append(path, Move{Group: group, State: ns})
					found = true
					return false
				}
				return true
			})
			if found {
				return true
			}
		}
		dead[state] = true
		return false
	}
	if !rec(0) {
		return nil, false, explored
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, true, explored
}

// MinGroupSchedule 求最小同步更新组大小 k 及对应方案。
// k = popcount(useful) 时一步更新全部节点必然可行, 故循环必然终止。
func (p *Problem) MinGroupSchedule() ([]Move, int) {
	for k := 1; k <= len(p.Nodes); k++ {
		if moves, ok, _ := p.FindSchedule(k); ok {
			return moves, k
		}
	}
	return nil, 0
}

func (p *Problem) namesOf(mask uint64) []string {
	var names []string
	for i, n := range p.Nodes {
		if mask>>uint(i)&1 == 1 {
			names = append(names, n)
		}
	}
	return names
}

func bitsOf(mask uint64) []int {
	var bits []int
	for i := 0; i < 64; i++ {
		if mask>>uint(i)&1 == 1 {
			bits = append(bits, i)
		}
	}
	return bits
}

// combinations 枚举 bits 中所有大小为 size 的组合 (以位掩码形式回调)。
// yield 返回 false 时提前终止。
func combinations(bits []int, size int, yield func(uint64) bool) {
	if size > len(bits) || size <= 0 {
		return
	}
	chosen := make([]int, 0, size)
	var rec func(start int) bool
	rec = func(start int) bool {
		if len(chosen) == size {
			var g uint64
			for _, b := range chosen {
				g |= 1 << uint(b)
			}
			return yield(g)
		}
		for i := start; i <= len(bits)-(size-len(chosen)); i++ {
			chosen = append(chosen, bits[i])
			if !rec(i + 1) {
				return false
			}
			chosen = chosen[:len(chosen)-1]
		}
		return true
	}
	rec(0)
}
