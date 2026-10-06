package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Step is one observed event in a trace: a script executed its next
// action and a specific public outcome transition fired.
type Step struct {
	Script  string `json:"script"`
	Action  string `json:"action"`
	Outcome string `json:"outcome"`
}

// Status is the checker verdict.
type Status string

const (
	StatusViolation Status = "violation" // terminal state with unreleased resources found
	StatusSafe      Status = "safe"      // full exploration, no violation
	StatusUnknown   Status = "unknown"   // budget exhausted before a verdict
)

// Result is the checker output.
type Result struct {
	Status     Status   `json:"status"`
	Trace      []Step   `json:"trace,omitempty"`
	Unreleased []string `json:"unreleased,omitempty"`
	Explored   int      `json:"explored"`
	Reason     string   `json:"reason,omitempty"`
}

// node is a composite state: resource valuation + per-script program counter.
type node struct {
	res   map[string]string
	pc    []int
	depth int
	trace []Step
}

func (n *node) key(order []string) string {
	var b strings.Builder
	for _, r := range order {
		b.WriteString(n.res[r])
		b.WriteByte(0)
	}
	for _, p := range n.pc {
		b.WriteString(strconv.Itoa(p))
		b.WriteByte(0)
	}
	return b.String()
}

func (n *node) terminal(lens []int) bool {
	for i, p := range n.pc {
		if p < lens[i] {
			return false
		}
	}
	return true
}

// unreleased lists resources not in a terminal (released) state.
func unreleased(spec *Spec, res map[string]string) []string {
	var out []string
	for _, r := range spec.resourceOrder() {
		ok := false
		for _, t := range spec.Terminals[r] {
			if res[r] == t {
				ok = true
				break
			}
		}
		if !ok {
			out = append(out, fmt.Sprintf("%s=%s", r, res[r]))
		}
	}
	return out
}

// Check enumerates all order-preserving interleavings of the scripts with
// BFS (shortest trace first) and looks for a terminal composite state in
// which some resource is not released. Action failure transitions are
// explored like any other public transition.
func Check(spec *Spec) Result {
	order := spec.resourceOrder()
	lens := make([]int, len(spec.Scripts))
	for i, sc := range spec.Scripts {
		lens[i] = len(sc.Actions)
	}

	start := &node{
		res: make(map[string]string, len(order)),
		pc:  make([]int, len(spec.Scripts)),
	}
	for _, r := range order {
		start.res[r] = spec.Initial[r]
	}

	visited := map[string]bool{start.key(order): true}
	queue := []*node{start}
	explored := 0

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		explored++

		if cur.terminal(lens) {
			if bad := unreleased(spec, cur.res); len(bad) > 0 {
				return Result{
					Status:     StatusViolation,
					Trace:      cur.trace,
					Unreleased: bad,
					Explored:   explored,
				}
			}
			continue
		}

		if cur.depth >= spec.Budget.MaxDepth {
			return Result{Status: StatusUnknown, Explored: explored,
				Reason: fmt.Sprintf("depth budget %d exhausted", spec.Budget.MaxDepth)}
		}

		for si, sc := range spec.Scripts {
			if cur.pc[si] >= len(sc.Actions) {
				continue
			}
			aname := sc.Actions[cur.pc[si]]
			for _, tr := range spec.Actions[aname].Transitions {
				if !guardHolds(tr.When, cur.res) {
					continue
				}
				next := &node{
					res:   applySet(cur.res, tr.Set),
					pc:    bumpPC(cur.pc, si),
					depth: cur.depth + 1,
					trace: appendStep(cur.trace, Step{Script: sc.Name, Action: aname, Outcome: tr.Outcome}),
				}
				k := next.key(order)
				if visited[k] {
					continue
				}
				visited[k] = true
				queue = append(queue, next)
			}
		}

		if explored >= spec.Budget.MaxStates && len(queue) > 0 {
			return Result{Status: StatusUnknown, Explored: explored,
				Reason: fmt.Sprintf("state budget %d exhausted", spec.Budget.MaxStates)}
		}
	}

	return Result{Status: StatusSafe, Explored: explored}
}

func guardHolds(when, res map[string]string) bool {
	for r, want := range when {
		if res[r] != want {
			return false
		}
	}
	return true
}

func applySet(res, set map[string]string) map[string]string {
	out := make(map[string]string, len(res))
	for k, v := range res {
		out[k] = v
	}
	for k, v := range set {
		out[k] = v
	}
	return out
}

func bumpPC(pc []int, i int) []int {
	out := make([]int, len(pc))
	copy(out, pc)
	out[i]++
	return out
}

func appendStep(trace []Step, s Step) []Step {
	out := make([]Step, len(trace)+1)
	copy(out, trace)
	out[len(trace)] = s
	return out
}
