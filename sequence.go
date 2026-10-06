package treeplan

import (
	"container/heap"
	"errors"
	"fmt"
	"math/bits"
	"sort"
	"strings"
)

// MaxSequenceGroups bounds the state search of problem 2.
const MaxSequenceGroups = 12

// MaxSequenceTargets bounds the served-set dimension of the state space.
const MaxSequenceTargets = 12

// ActionKind is one of expand, collapse or serve.
type ActionKind string

const (
	Expand   ActionKind = "expand"
	Collapse ActionKind = "collapse"
	Serve    ActionKind = "serve"
)

// Action is a single plan step. Name is a group name for expand/collapse
// and a target leaf name for serve.
type Action struct {
	Kind ActionKind `json:"kind"`
	Name string     `json:"name"`
}

// Plan is the answer to problem 2.
type Plan struct {
	Actions []Action `json:"actions"`
	Cost    int64    `json:"cost"`
	Peak    int64    `json:"peakMemory"`
	Budget  int64    `json:"budget"`
}

// seqState is the search state: which groups are expanded and which targets
// have been served. Memory is derived from it (see sequenceMemory).
type seqState struct {
	expanded uint32
	served   uint32
}

// sequenceModel precomputes everything the search and the brute-force
// checker need.
type sequenceModel struct {
	groupBytes   []int64  // own chunk + chunks of leaves exposed per group
	mem          []int64  // mem[mask] = loaded bytes of expanded groups mask
	targetParent []int    // group index of each target leaf
	targetMem    []int64  // data chunk of each target leaf
	targetAnc    []uint32 // groups that must be expanded to serve each target
	fullServed   uint32
}

func (t *Tree) newSequenceModel() *sequenceModel {
	n := len(t.Groups)
	m := &sequenceModel{
		groupBytes: make([]int64, n),
		mem:        make([]int64, 1<<uint(n)),
		fullServed: uint32(1)<<uint(len(t.Targets)) - 1,
	}
	for g := range t.Groups {
		b := t.Groups[g].Memory
		for _, li := range t.GroupLeaves[g] {
			b += t.Leaves[li].Memory
		}
		m.groupBytes[g] = b
	}
	for mask := 1; mask < len(m.mem); mask++ {
		lb := mask & -mask
		g := bits.TrailingZeros(uint(lb))
		m.mem[mask] = m.mem[mask^lb] + m.groupBytes[g]
	}
	for _, li := range t.Targets {
		m.targetParent = append(m.targetParent, t.LeafGroup[li])
		m.targetMem = append(m.targetMem, t.Leaves[li].Memory)
		m.targetAnc = append(m.targetAnc, t.LeafAncMask[li])
	}
	return m
}

// memory returns the bytes loaded in a state: chunks of expanded groups and
// of the leaves they expose, plus the chunk of every served target whose
// group is currently collapsed (an open view still references it, so
// collapsing its ancestor could not free it).
func (m *sequenceModel) memory(e, s uint32) int64 {
	total := m.mem[e]
	for i := range m.targetParent {
		if s&(uint32(1)<<uint(i)) == 0 {
			continue
		}
		if e&(uint32(1)<<uint(m.targetParent[i])) == 0 {
			total += m.targetMem[i]
		}
	}
	return total
}

// transitions applies every legal action from (e, s).
func (t *Tree) transitions(m *sequenceModel, e, s uint32, apply func(ne, ns uint32, cost int64, act Action)) {
	for g := range t.Groups {
		bit := uint32(1) << uint(g)
		if e&bit != 0 {
			continue
		}
		if p := t.Parent[g]; p >= 0 && e&(uint32(1)<<uint(p)) == 0 {
			continue // a group expands only after its parent
		}
		apply(e|bit, s, t.Groups[g].ExpandCost, Action{Expand, t.Groups[g].Name})
	}
	for g := range t.Groups {
		bit := uint32(1) << uint(g)
		if e&bit == 0 {
			continue
		}
		childOpen := false
		for _, c := range t.Children[g] {
			if e&(uint32(1)<<uint(c)) != 0 {
				childOpen = true
				break
			}
		}
		if childOpen {
			continue // child groups must be collapsed first
		}
		apply(e&^bit, s, t.Groups[g].CollapseCost, Action{Collapse, t.Groups[g].Name})
	}
	for i, li := range t.Targets {
		if s&(uint32(1)<<uint(i)) != 0 {
			continue
		}
		if e&m.targetAnc[i] != m.targetAnc[i] {
			continue // every ancestor must be expanded to reach the target
		}
		apply(e, s|(uint32(1)<<uint(i)), t.Leaves[li].ServeCost, Action{Serve, t.Leaves[li].Name})
	}
}

type pqItem struct {
	st   seqState
	cost int64
}

type statePQ []pqItem

func (q statePQ) Len() int            { return len(q) }
func (q statePQ) Less(i, j int) bool  { return q[i].cost < q[j].cost }
func (q statePQ) Swap(i, j int)       { q[i], q[j] = q[j], q[i] }
func (q *statePQ) Push(x interface{}) { *q = append(*q, x.(pqItem)) }
func (q *statePQ) Pop() interface{} {
	old := *q
	n := len(old)
	it := old[n-1]
	*q = old[:n-1]
	return it
}

// SolveSequence finds a minimum-cost expand/collapse/serve plan that serves
// every target while the loaded bytes never exceed budget. It runs Dijkstra
// over (expanded, served) states.
func (t *Tree) SolveSequence(budget int64) (*Plan, error) {
	n := len(t.Groups)
	if n > MaxSequenceGroups {
		return nil, fmt.Errorf("state search supports at most %d groups, got %d", MaxSequenceGroups, n)
	}
	if len(t.Targets) > MaxSequenceTargets {
		return nil, fmt.Errorf("state search supports at most %d targets, got %d", MaxSequenceTargets, len(t.Targets))
	}
	if budget < 0 {
		return nil, errors.New("budget must be non-negative")
	}
	m := t.newSequenceModel()
	start := seqState{}
	dist := map[seqState]int64{start: 0}
	type step struct {
		prev seqState
		act  Action
	}
	from := map[seqState]step{}
	pq := &statePQ{{st: start}}
	heap.Init(pq)
	var goal seqState
	found := false
	for pq.Len() > 0 {
		it := heap.Pop(pq).(pqItem)
		if cur, ok := dist[it.st]; ok && it.cost > cur {
			continue
		}
		if it.st.served == m.fullServed {
			goal, found = it.st, true
			break
		}
		t.transitions(m, it.st.expanded, it.st.served, func(ne, ns uint32, cost int64, act Action) {
			if m.memory(ne, ns) > budget {
				return
			}
			next := seqState{ne, ns}
			nd := it.cost + cost
			if cur, ok := dist[next]; !ok || nd < cur {
				dist[next] = nd
				from[next] = step{it.st, act}
				heap.Push(pq, pqItem{st: next, cost: nd})
			}
		})
	}
	if !found {
		return nil, fmt.Errorf("no feasible plan within memory budget %d", budget)
	}
	var rev []Action
	var states []seqState
	for st := goal; ; {
		states = append(states, st)
		if st == start {
			break
		}
		s := from[st]
		rev = append(rev, s.act)
		st = s.prev
	}
	plan := &Plan{Cost: dist[goal], Budget: budget}
	for i := len(rev) - 1; i >= 0; i-- {
		plan.Actions = append(plan.Actions, rev[i])
	}
	for _, st := range states {
		if mem := m.memory(st.expanded, st.served); mem > plan.Peak {
			plan.Peak = mem
		}
	}
	return plan, nil
}

// BruteForceSequenceCost is the independent checker for SolveSequence: a
// depth-first branch and bound over the same action graph. Small instances
// only.
func (t *Tree) BruteForceSequenceCost(budget int64) (int64, bool) {
	m := t.newSequenceModel()
	best := inf
	seen := map[seqState]int64{}
	var dfs func(e, s uint32, cost int64)
	dfs = func(e, s uint32, cost int64) {
		if cost >= best || m.memory(e, s) > budget {
			return
		}
		st := seqState{e, s}
		if c, ok := seen[st]; ok && c <= cost {
			return
		}
		seen[st] = cost
		if s == m.fullServed {
			best = cost
			return
		}
		t.transitions(m, e, s, func(ne, ns uint32, c int64, act Action) {
			dfs(ne, ns, cost+c)
		})
	}
	dfs(0, 0, 0)
	if best == inf {
		return 0, false
	}
	return best, true
}

// LedgerEntry is one row of the step-by-step reference account produced by
// Replay.
type LedgerEntry struct {
	Step   int            `json:"step"`
	Action Action         `json:"action"`
	Cost   int64          `json:"cost"`   // cumulative action cost
	Memory int64          `json:"memory"` // loaded bytes after the action
	Peak   int64          `json:"peak"`   // running peak
	Refs   map[string]int `json:"refs,omitempty"`
	Note   string         `json:"note,omitempty"`
}

// Ledger is the full reference account of a replayed plan.
type Ledger struct {
	Entries []LedgerEntry `json:"entries"`
	Cost    int64         `json:"cost"`
	Peak    int64         `json:"peak"`
}

// Replay simulates a plan action by action with explicit reference
// accounting, independent of the search code. It validates legality of every
// action, tracks which data chunks stay loaded and why, enforces the memory
// budget, and requires every target to be served by the end.
func (t *Tree) Replay(actions []Action, budget int64) (*Ledger, error) {
	if budget < 0 {
		return nil, errors.New("budget must be non-negative")
	}
	expanded := make([]bool, len(t.Groups))
	served := map[int]bool{}
	refs := map[string]int{} // target leaf name -> open view count
	loaded := func() int64 {
		var total int64
		for g := range t.Groups {
			if !expanded[g] {
				continue
			}
			total += t.Groups[g].Memory
			for _, li := range t.GroupLeaves[g] {
				total += t.Leaves[li].Memory
			}
		}
		for name, c := range refs {
			if c == 0 {
				continue
			}
			li := t.LeafIndex[name]
			if !expanded[t.LeafGroup[li]] {
				total += t.Leaves[li].Memory // retained: referenced by an open view
			}
		}
		return total
	}
	l := &Ledger{}
	var peak int64
	record := func(step int, act Action, cost int64, note string) error {
		mem := loaded()
		if mem > peak {
			peak = mem
		}
		if mem > budget {
			return fmt.Errorf("step %d (%s %s): memory %d exceeds budget %d", step, act.Kind, act.Name, mem, budget)
		}
		entry := LedgerEntry{Step: step, Action: act, Cost: cost, Memory: mem, Peak: peak, Note: note}
		if len(refs) > 0 {
			entry.Refs = map[string]int{}
			for k, v := range refs {
				entry.Refs[k] = v
			}
		}
		l.Entries = append(l.Entries, entry)
		l.Cost = cost
		l.Peak = peak
		return nil
	}
	var cost int64
	for i, act := range actions {
		step := i + 1
		note := ""
		switch act.Kind {
		case Expand:
			g, ok := t.GroupIndex[act.Name]
			if !ok {
				return nil, fmt.Errorf("step %d: unknown group %q", step, act.Name)
			}
			if expanded[g] {
				return nil, fmt.Errorf("step %d: group %q already expanded", step, act.Name)
			}
			if p := t.Parent[g]; p >= 0 && !expanded[p] {
				return nil, fmt.Errorf("step %d: group %q expanded before its parent", step, act.Name)
			}
			expanded[g] = true
			cost += t.Groups[g].ExpandCost
		case Collapse:
			g, ok := t.GroupIndex[act.Name]
			if !ok {
				return nil, fmt.Errorf("step %d: unknown group %q", step, act.Name)
			}
			if !expanded[g] {
				return nil, fmt.Errorf("step %d: group %q is not expanded", step, act.Name)
			}
			for _, c := range t.Children[g] {
				if expanded[c] {
					return nil, fmt.Errorf("step %d: group %q collapsed while child %q is expanded", step, act.Name, t.Groups[c].Name)
				}
			}
			expanded[g] = false
			cost += t.Groups[g].CollapseCost
			var retained []string
			for _, li := range t.GroupLeaves[g] {
				if refs[t.Leaves[li].Name] > 0 {
					retained = append(retained, t.Leaves[li].Name)
				}
			}
			if len(retained) > 0 {
				sort.Strings(retained)
				note = "retained, referenced by open view(s): " + strings.Join(retained, ", ")
			}
		case Serve:
			li, ok := t.LeafIndex[act.Name]
			if !ok {
				return nil, fmt.Errorf("step %d: unknown leaf %q", step, act.Name)
			}
			if !t.IsTarget[li] {
				return nil, fmt.Errorf("step %d: leaf %q is not a target", step, act.Name)
			}
			if served[li] {
				return nil, fmt.Errorf("step %d: target %q already served", step, act.Name)
			}
			for g := t.LeafGroup[li]; g >= 0; g = t.Parent[g] {
				if !expanded[g] {
					return nil, fmt.Errorf("step %d: target %q not reachable, group %q collapsed", step, act.Name, t.Groups[g].Name)
				}
			}
			served[li] = true
			refs[act.Name]++
			cost += t.Leaves[li].ServeCost
		default:
			return nil, fmt.Errorf("step %d: unknown action kind %q", step, act.Kind)
		}
		if err := record(step, act, cost, note); err != nil {
			return nil, err
		}
	}
	if len(served) != len(t.Targets) {
		var missing []string
		for _, li := range t.Targets {
			if !served[li] {
				missing = append(missing, t.Leaves[li].Name)
			}
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("plan ends with unserved targets: %s", strings.Join(missing, ", "))
	}
	return l, nil
}
