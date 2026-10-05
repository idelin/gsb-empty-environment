package main

import (
	"fmt"
	"math"
)

// 把所有时间约束统一为差分约束 x[to] - x[from] <= w，
// 这样“任务时长>=0”“排队>=0”“依赖先后”“时钟区间”全部变成图上的边，
// 可行性等价于无负环，总耗时范围等价于两点间最短/最长路。

const srcName = "$" // 超级源点，用于表达事件的绝对区间界

type cedge struct {
	from, to int
	w        float64
	desc     string // 人类可读的约束来源，用于矛盾定位与解释
}

type csystem struct {
	names []string
	idx   map[string]int
	edges []cedge
}

func newCsystem() *csystem {
	s := &csystem{idx: map[string]int{}}
	s.node(srcName)
	return s
}

func (s *csystem) node(name string) int {
	if i, ok := s.idx[name]; ok {
		return i
	}
	i := len(s.names)
	s.names = append(s.names, name)
	s.idx[name] = i
	return i
}

// addLE 加入约束 x[to] - x[from] <= w。
func (s *csystem) addLE(from, to int, w float64, desc string) {
	s.edges = append(s.edges, cedge{from: from, to: to, w: w, desc: desc})
}

// buildSystem 把输入翻译成差分约束系统。
func buildSystem(in *Input) *csystem {
	s := newCsystem()
	src := s.node(srcName)
	for _, e := range in.Events {
		i := s.node(e.Name)
		s.addLE(src, i, e.Hi, fmt.Sprintf("事件 %s 不晚于 %.3f（时钟区间上界）", e.Name, e.Hi))
		s.addLE(i, src, -e.Lo, fmt.Sprintf("事件 %s 不早于 %.3f（时钟区间下界）", e.Name, e.Lo))
	}
	ord := func(a, b, desc string) { // a 不晚于 b：x[b] >= x[a]
		s.addLE(s.node(b), s.node(a), 0, desc)
	}
	for _, t := range in.Tasks {
		if t.Request != "" && t.Acquire != "" {
			ord(t.Request, t.Acquire, fmt.Sprintf("任务 %s：排队时间不小于 0（获取不早于请求）", t.Name))
		}
		if t.Acquire != "" {
			ord(t.Acquire, t.Start, fmt.Sprintf("任务 %s：获得资源后才能开始", t.Name))
		}
		ord(t.Start, t.End, fmt.Sprintf("任务 %s：执行时长不小于 0（结束不早于开始）", t.Name))
	}
	for _, d := range in.Deps {
		label := d.Label
		if label == "" {
			label = d.After + " -> " + d.Before
		}
		ord(d.After, d.Before, fmt.Sprintf("依赖 %s：%s 完成后 %s 才能发生", label, d.After, d.Before))
	}
	return s
}

const eps = 1e-9

// bellmanFord 求解差分约束系统。可行时返回一组可行解 dist（满足所有约束）；
// 不可行时返回一个负环（矛盾约束环）。
func bellmanFord(n int, edges []cedge) (dist []float64, cycle []cedge) {
	dist = make([]float64, n) // 全 0 初始化等价于对超级源点连 0 边
	pred := make([]int, n)
	for i := range pred {
		pred[i] = -1
	}
	relaxed := -1
	for iter := 0; iter < n; iter++ {
		relaxed = -1
		for ei, e := range edges {
			if dist[e.to] > dist[e.from]+e.w+eps {
				dist[e.to] = dist[e.from] + e.w
				pred[e.to] = ei
				relaxed = e.to
			}
		}
		if relaxed == -1 {
			return dist, nil
		}
	}
	// 第 n 轮仍可松弛 => 存在负环，沿 pred 回溯定位
	v := relaxed
	for i := 0; i < n; i++ {
		v = edges[pred[v]].from
	}
	start := v
	cur := v
	for {
		e := edges[pred[cur]]
		cycle = append(cycle, e)
		cur = e.from
		if cur == start {
			break
		}
	}
	// 反转成沿环方向的顺序
	for i, j := 0, len(cycle)-1; i < j; i, j = i+1, j-1 {
		cycle[i], cycle[j] = cycle[j], cycle[i]
	}
	return nil, cycle
}

// shortestFrom 在可行系统上求从 src 出发的最短路（x[v] - x[src] 的最大可行值）。
func shortestFrom(n int, edges []cedge, src int) ([]float64, []int) {
	dist := make([]float64, n)
	pred := make([]int, n)
	for i := range dist {
		dist[i] = math.Inf(1)
		pred[i] = -1
	}
	dist[src] = 0
	for iter := 0; iter < n-1; iter++ {
		changed := false
		for ei, e := range edges {
			if math.IsInf(dist[e.from], 1) {
				continue
			}
			if dist[e.to] > dist[e.from]+e.w+eps {
				dist[e.to] = dist[e.from] + e.w
				pred[e.to] = ei
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return dist, pred
}

// pathTo 用 pred 还原 src->v 的约束链。
func pathTo(edges []cedge, pred []int, src, v int) []cedge {
	var rev []cedge
	for v != src && v >= 0 {
		if pred[v] < 0 {
			return nil
		}
		e := edges[pred[v]]
		rev = append(rev, e)
		v = e.from
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// spanRange 返回总耗时 x[end]-x[begin] 的可能范围 [min, max]。
// 前提：系统可行。max = 最短路 d(begin,end)，min = -d(end,begin)。
func spanRange(sys *csystem, begin, end string) (minSpan, maxSpan float64) {
	a := sys.idx[begin]
	b := sys.idx[end]
	dAB, _ := shortestFrom(sys.n(), sys.edges, a)
	dBA, _ := shortestFrom(sys.n(), sys.edges, b)
	return -dBA[a], dAB[b]
}

func (s *csystem) n() int { return len(s.names) }
