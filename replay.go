package main

import (
	"fmt"
	"strings"
)

// Replay 把区分公式展开为一棵可重放的区分过程树：
// 每个进攻步给出具体的弱转移路径（tau 前缀、可见动作、tau 后缀），
// 防守方的每个弱响应都递归地被反驳，直到防守方无响应可走。
// f 必须满足：在进攻方状态 as 上为真、在防守方状态 ds 上为假。
func Replay(f Formula, al *LTS, as string, dl *LTS, ds string) []string {
	var lines []string
	replay(f, al, as, dl, ds, 0, &lines)
	return lines
}

func replay(f Formula, al *LTS, as string, dl *LTS, ds string, depth int, lines *[]string) {
	pad := strings.Repeat("  ", depth)
	switch g := f.(type) {
	case True:
		*lines = append(*lines, fmt.Sprintf("%s%s 无任何可匹配的响应，区分完成", pad, ds))
	case Not:
		*lines = append(*lines, fmt.Sprintf("%s攻守互换：由 %s 进攻、%s 防守", pad, ds, as))
		replay(g.Sub, dl, ds, al, as, depth+1, lines)
	case And:
		for _, part := range g.Parts {
			replay(part, al, as, dl, ds, depth, lines)
		}
	case Diamond:
		path, target := findWeakPath(al, as, g.Action, func(s string) bool {
			return g.Sub.Eval(al, s)
		})
		if target == "" {
			*lines = append(*lines, fmt.Sprintf("%s[内部错误] %s 上找不到满足子公式的弱转移", pad, as))
			return
		}
		*lines = append(*lines, fmt.Sprintf("%s%s 走出弱转移 <<%s>>：%s", pad, as, g.Action, formatPath(path)))
		responders := dl.WeakMove(ds, g.Action)
		if len(responders) == 0 {
			*lines = append(*lines, fmt.Sprintf("%s%s 没有任何 <<%s>> 弱响应，区分完成", pad, ds, g.Action))
			return
		}
		for _, r := range responders {
			sub := refutingConjunct(g.Sub, dl, r)
			*lines = append(*lines, fmt.Sprintf("%s%s 弱响应到 %s，被反驳：", pad, ds, r))
			replay(sub, al, target, dl, r, depth+1, lines)
		}
	}
}

// refutingConjunct 在合取式 f 中找出在防守方响应状态 s 上为假的合取项；
// 该项在进攻方目标状态上为真，可继续作为区分公式。
func refutingConjunct(f Formula, l *LTS, s string) Formula {
	if g, ok := f.(And); ok {
		for _, part := range g.Parts {
			if !part.Eval(l, s) {
				return part
			}
		}
	}
	return f
}

// findWeakPath 找出一条从 s 出发的弱转移具体路径：
// 对可见动作 a 为 tau* · a · tau*，对 tau 为 tau*，
// 终点满足 pred。调用方保证这样的路径存在。
func findWeakPath(l *LTS, s, a string, pred func(string) bool) ([]Transition, string) {
	type node struct {
		state string
		path  []Transition
	}
	extend := func(p []Transition, tr Transition) []Transition {
		q := make([]Transition, len(p)+1)
		copy(q, p)
		q[len(p)] = tr
		return q
	}
	// 阶段一：沿 tau* 前进，收集所有可出 a 边的位置。
	seen := map[string]bool{s: true}
	queue := []node{{state: s}}
	type candidate struct {
		prefix []Transition
		edge   Transition
	}
	var cands []candidate
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if a == Tau && pred(n.state) {
			return n.path, n.state
		}
		for _, tr := range l.Transitions(n.state) {
			if tr.Label == Tau {
				if !seen[tr.To] {
					seen[tr.To] = true
					queue = append(queue, node{tr.To, extend(n.path, tr)})
				}
			} else if a != Tau && tr.Label == a {
				cands = append(cands, candidate{n.path, tr})
			}
		}
	}
	// 阶段二：对每个候选 a 边，沿 tau* 寻找满足 pred 的终点。
	for _, c := range cands {
		seen2 := map[string]bool{c.edge.To: true}
		q2 := []node{{c.edge.To, extend(c.prefix, c.edge)}}
		for len(q2) > 0 {
			n := q2[0]
			q2 = q2[1:]
			if pred(n.state) {
				return n.path, n.state
			}
			for _, tr := range l.Transitions(n.state) {
				if tr.Label == Tau && !seen2[tr.To] {
					seen2[tr.To] = true
					q2 = append(q2, node{tr.To, extend(n.path, tr)})
				}
			}
		}
	}
	return nil, ""
}

func formatPath(path []Transition) string {
	if len(path) == 0 {
		return "(零步，原地)"
	}
	var b strings.Builder
	b.WriteString(path[0].From)
	for _, tr := range path {
		fmt.Fprintf(&b, " -%s-> %s", tr.Label, tr.To)
	}
	return b.String()
}
