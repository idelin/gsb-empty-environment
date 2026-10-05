package main

import (
	"fmt"
	"io"
	"sort"
)

// printCycle 定位并打印矛盾约束环。
func printCycle(w io.Writer, sys *csystem, cycle []cedge) {
	fmt.Fprintln(w, "检测到矛盾：以下时间约束构成不可满足的环（负环），无法简单相减得到负耗时，需修正其中之一：")
	sum := 0.0
	for _, e := range cycle {
		fmt.Fprintf(w, "  x[%s] - x[%s] <= %.3f    | %s\n", sys.names[e.to], sys.names[e.from], e.w, e.desc)
		sum += e.w
	}
	fmt.Fprintf(w, "约束和 = %.3f < 0，环上约束互相冲突。\n", sum)
	involved := map[string]bool{}
	for _, e := range cycle {
		involved[sys.names[e.from]] = true
		involved[sys.names[e.to]] = true
	}
	delete(involved, srcName)
	names := []string{}
	for n := range involved {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "涉及事件: %v\n", names)
}

// printForcedNegativeSpan 处理系统可行但约束强制“终点早于起点”的情形。
func printForcedNegativeSpan(w io.Writer, sys *csystem, begin, end string, maxSpan float64) {
	fmt.Fprintln(w, "检测到矛盾：约束系统本身可行，但以下约束链强制终点事件早于起点事件（总耗时为负）：")
	a := sys.idx[begin]
	b := sys.idx[end]
	_, pred := shortestFrom(sys.n(), sys.edges, a)
	path := pathTo(sys.edges, pred, a, b)
	for _, e := range path {
		fmt.Fprintf(w, "  x[%s] - x[%s] <= %.3f    | %s\n", sys.names[e.to], sys.names[e.from], e.w, e.desc)
	}
	fmt.Fprintf(w, "链上约束和 = %.3f < 0，即 x[%s] - x[%s] <= %.3f。\n", maxSpan, end, begin, maxSpan)
	fmt.Fprintln(w, "请检查上述事件的时钟区间或依赖方向是否填反。")
}

// 关键链上的分段类型
const (
	kindCompute = "计算"
	kindWait    = "排队"
	kindReady   = "就绪等待"
	kindDep     = "依赖传递"
)

type seg struct {
	from, to string
	kind     string
	task     string
}

// progressEdges 构造“时间只会前进”的边，用于在 witness 上还原关键链。
func progressEdges(in *Input) []seg {
	var segs []seg
	for _, t := range in.Tasks {
		if t.Request != "" && t.Acquire != "" {
			segs = append(segs, seg{from: t.Request, to: t.Acquire, kind: kindWait, task: t.Name})
		}
		if t.Acquire != "" {
			segs = append(segs, seg{from: t.Acquire, to: t.Start, kind: kindReady, task: t.Name})
		}
		segs = append(segs, seg{from: t.Start, to: t.End, kind: kindCompute, task: t.Name})
	}
	for _, d := range in.Deps {
		segs = append(segs, seg{from: d.After, to: d.Before, kind: kindDep})
	}
	return segs
}

// criticalChain 在 witness 时间线上求 begin->end 的最长链，并按类型汇总。
func criticalChain(in *Input, times map[string]float64) (chain []seg, totals map[string]float64) {
	segs := progressEdges(in)
	names := []string{}
	idx := map[string]int{}
	node := func(name string) int {
		if i, ok := idx[name]; ok {
			return i
		}
		i := len(names)
		names = append(names, name)
		idx[name] = i
		return i
	}
	type pedge struct{ to, si int }
	adj := map[int][]pedge{}
	for si, s := range segs {
		u := node(s.from)
		v := node(s.to)
		adj[u] = append(adj[u], pedge{to: v, si: si})
	}
	src := node(in.SpanBegin)
	dst := node(in.SpanEnd)
	const inf = 1e100
	dist := make([]float64, len(names))
	prev := make([]pedge, len(names))
	hasPrev := make([]bool, len(names))
	for i := range dist {
		dist[i] = -inf
	}
	dist[src] = 0
	for iter := 0; iter < len(names)-1; iter++ {
		changed := false
		for u := range adj {
			if dist[u] <= -inf {
				continue
			}
			for _, pe := range adj[u] {
				w := times[segs[pe.si].to] - times[segs[pe.si].from]
				if dist[u]+w > dist[pe.to]+eps {
					dist[pe.to] = dist[u] + w
					prev[pe.to] = pe
					prev[pe.to].to = u // 复用字段存前驱节点
					hasPrev[pe.to] = true
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	totals = map[string]float64{kindCompute: 0, kindWait: 0, kindReady: 0, kindDep: 0}
	if dist[dst] <= -inf {
		return nil, totals
	}
	for v := dst; v != src; {
		pe := prev[v]
		if !hasPrev[v] {
			return nil, totals
		}
		s := segs[pe.si]
		chain = append(chain, s)
		totals[s.kind] += times[s.to] - times[s.from]
		v = pe.to // 前驱节点
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, totals
}

// printExplanation 打印一条可实现解释：时间线、任务分解、关键链与结论。
func printExplanation(w io.Writer, in *Input, title string, times map[string]float64, span float64) {
	fmt.Fprintf(w, "=== %s（总耗时 %.3f）===\n", title, span)
	type ev struct {
		name string
		t    float64
	}
	evs := []ev{}
	for _, e := range in.Events {
		evs = append(evs, ev{e.Name, times[e.Name]})
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].t != evs[j].t {
			return evs[i].t < evs[j].t
		}
		return evs[i].name < evs[j].name
	})
	fmt.Fprintln(w, "可实现时间线（时钟误差内的一组合法取值）:")
	for _, e := range evs {
		fmt.Fprintf(w, "  %-18s %.3f\n", e.name, e.t)
	}
	fmt.Fprintln(w, "任务分解:")
	for _, t := range in.Tasks {
		compute := times[t.End] - times[t.Start]
		line := fmt.Sprintf("  %-10s 计算 %.3f", t.Name, compute)
		if t.Request != "" && t.Acquire != "" {
			line += fmt.Sprintf("，排队 %.3f", times[t.Acquire]-times[t.Request])
		}
		fmt.Fprintln(w, line)
	}
	chain, totals := criticalChain(in, times)
	if chain == nil {
		fmt.Fprintln(w, "关键链: 起点与终点之间没有由任务/依赖连接的路径，无法分解。")
		return
	}
	fmt.Fprintln(w, "关键链:")
	for _, s := range chain {
		d := times[s.to] - times[s.from]
		label := s.kind
		if s.task != "" {
			label += "(" + s.task + ")"
		}
		fmt.Fprintf(w, "  %s -[%s %.3f]-> %s\n", s.from, label, d, s.to)
	}
	explained := totals[kindCompute] + totals[kindWait] + totals[kindReady] + totals[kindDep]
	gap := span - explained
	fmt.Fprintf(w, "汇总: 计算 %.3f，排队 %.3f，就绪等待 %.3f，依赖传递 %.3f，时钟误差间隙 %.3f\n",
		totals[kindCompute], totals[kindWait], totals[kindReady], totals[kindDep], gap)
	if totals[kindWait]+totals[kindReady] > totals[kindCompute] {
		fmt.Fprintf(w, "结论: 排队慢 —— 资源等待(%.3f)超过计算执行(%.3f)，瓶颈在排队。\n",
			totals[kindWait]+totals[kindReady], totals[kindCompute])
	} else {
		fmt.Fprintf(w, "结论: 计算慢 —— 计算执行(%.3f)占主导，资源等待(%.3f)其次。\n",
			totals[kindCompute], totals[kindWait]+totals[kindReady])
	}
}
