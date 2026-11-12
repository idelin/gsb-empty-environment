package cond

import "fmt"

// Step 是一次求值动作的证据条目。
type Step struct {
	Path    string // 节点在表达式树中的位置，如 "and[1]"
	Node    string // 节点的规范化文本
	Result  Tri    // 该节点的求值结果；被跳过的节点为 Unknown 占位
	Skipped bool   // 是否因短路而未求值
}

func (s Step) String() string {
	if s.Skipped {
		return fmt.Sprintf("%s %s => skipped", s.Path, s.Node)
	}
	return fmt.Sprintf("%s %s => %s", s.Path, s.Node, s.Result)
}

// Evidence 记录一次求值的完整轨迹，含被短路跳过的分支，
// 可供网关审计日志直接落盘。
type Evidence struct {
	Steps []Step
}

func (ev *Evidence) record(path string, e Expr, r Tri, skipped bool) {
	ev.Steps = append(ev.Steps, Step{Path: path, Node: e.String(), Result: r, Skipped: skipped})
}

// Eval 对请求标签求值并生成短路证据。
func Eval(e Expr, l Labels) (Tri, Evidence) {
	var ev Evidence
	r := e.eval(l, rootPath(e), &ev)
	return r, ev
}

// Decide 是网关入口：求值并按“仅真放行”给出放行判定与证据。
func Decide(e Expr, l Labels) (allow bool, t Tri, ev Evidence) {
	t, ev = Eval(e, l)
	return Allow(t), t, ev
}

func rootPath(e Expr) string {
	switch e.(type) {
	case Conj:
		return "and"
	case Disj:
		return "or"
	case Neg:
		return "not"
	default:
		return "leaf"
	}
}

func childPath(parent string, i int) string { return fmt.Sprintf("%s[%d]", parent, i) }

func (e Exists) eval(l Labels, path string, ev *Evidence) Tri {
	r := False
	if l.Get(e.Label).Present() {
		r = True
	}
	ev.record(path, e, r, false)
	return r
}

func (e AnyEq) eval(l Labels, path string, ev *Evidence) Tri {
	r := l.Get(e.Label).Any(func(s string) bool { return s == e.Want })
	ev.record(path, e, r, false)
	return r
}

func (e AllEq) eval(l Labels, path string, ev *Evidence) Tri {
	r := l.Get(e.Label).All(func(s string) bool { return s == e.Want })
	ev.record(path, e, r, false)
	return r
}

func (e Neg) eval(l Labels, path string, ev *Evidence) Tri {
	r := Not(e.Arg.eval(l, childPath(path, 0), ev))
	ev.record(path, e, r, false)
	return r
}

func (e Conj) eval(l Labels, path string, ev *Evidence) Tri {
	r := True
	for i, a := range e.Args {
		if r == False {
			// 已短路：仅记录跳过证据，不再触碰请求标签。
			ev.record(childPath(path, i), a, Unknown, true)
			continue
		}
		r = And(r, a.eval(l, childPath(path, i), ev))
	}
	ev.record(path, e, r, false)
	return r
}

func (e Disj) eval(l Labels, path string, ev *Evidence) Tri {
	r := False
	for i, a := range e.Args {
		if r == True {
			ev.record(childPath(path, i), a, Unknown, true)
			continue
		}
		r = Or(r, a.eval(l, childPath(path, i), ev))
	}
	ev.record(path, e, r, false)
	return r
}
