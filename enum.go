package main

import (
	"fmt"
	"sort"
	"strings"
)

// Enumerator exhaustively explores the reachable state space of a small
// network. The tick phases match Sim exactly (inject -> receive ->
// deliver+commit), but every scheduling choice that the deterministic
// simulator fixes arbitrarily is treated as nondeterministic here:
// the order of edge receives and the order of send commits are both
// enumerated over all permutations. This proves that safety invariants
// hold for *any* traversal order, and finds every reachable deadlock.
//
// Restriction: all traffic must have Born == 0 (injection is part of
// the initial state), and at most 64 packets.
type Enumerator struct {
	cfg     Config
	edgeIdx [][]int
	pkts    []Packet
}

// EState is a canonical, hashable network state.
type EState struct {
	buffers   [][]int // sorted packet ids per node
	queues    [][]int // FIFO packet ids per edge
	pending   [][]int // sorted packet ids per node
	delivered uint64  // bitmask over packet ids
}

func NewEnumerator(cfg Config) (*Enumerator, error) {
	s, err := NewSim(cfg) // reuse validation
	if err != nil {
		return nil, err
	}
	if len(cfg.Traffic) > 64 {
		return nil, fmt.Errorf("enumeration supports at most 64 packets")
	}
	for _, p := range cfg.Traffic {
		if p.Born != 0 {
			return nil, fmt.Errorf("enumeration requires Born == 0 (packet %d)", p.ID)
		}
	}
	return &Enumerator{cfg: cfg, edgeIdx: s.edgeIdx, pkts: s.pkts}, nil
}

func (e *Enumerator) initial() EState {
	st := EState{
		buffers: make([][]int, e.cfg.Nodes),
		queues:  make([][]int, len(e.cfg.Edges)),
		pending: make([][]int, e.cfg.Nodes),
	}
	for _, p := range e.pkts {
		st.pending[p.Src] = append(st.pending[p.Src], p.ID)
	}
	for n := range st.pending {
		sort.Ints(st.pending[n])
	}
	return st
}

func (st EState) key() string {
	var b strings.Builder
	for _, buf := range st.buffers {
		fmt.Fprintf(&b, "%v|", buf)
	}
	b.WriteString("#")
	for _, q := range st.queues {
		fmt.Fprintf(&b, "%v|", q)
	}
	b.WriteString("#")
	for _, p := range st.pending {
		fmt.Fprintf(&b, "%v|", p)
	}
	fmt.Fprintf(&b, "#%d", st.delivered)
	return b.String()
}

func cloneInts(in [][]int) [][]int {
	out := make([][]int, len(in))
	for i, v := range in {
		out[i] = append([]int(nil), v...)
	}
	return out
}

// inject moves pending packets into buffers while room remains
// (deterministic: pending is sorted by id).
func (e *Enumerator) inject(st *EState) {
	for n := 0; n < e.cfg.Nodes; n++ {
		for len(st.pending[n]) > 0 && len(st.buffers[n]) < e.cfg.BufferCap {
			st.buffers[n] = append(st.buffers[n], st.pending[n][0])
			st.pending[n] = st.pending[n][1:]
		}
		sort.Ints(st.buffers[n])
	}
}

// deliver removes packets that arrived at their destination
// (deterministic).
func (e *Enumerator) deliver(st *EState) {
	for n := 0; n < e.cfg.Nodes; n++ {
		out := st.buffers[n][:0]
		for _, pid := range st.buffers[n] {
			if e.pkts[pid].Dst == n {
				st.delivered |= 1 << uint(pid)
			} else {
				out = append(out, pid)
			}
		}
		st.buffers[n] = out
	}
}

// receiveOnce applies one specific edge ordering for the receive phase.
func (e *Enumerator) receiveOnce(st EState, order []int) EState {
	st.buffers = cloneInts(st.buffers)
	st.queues = cloneInts(st.queues)
	for _, ei := range order {
		edge := e.cfg.Edges[ei]
		if len(st.queues[ei]) == 0 {
			continue
		}
		if len(st.buffers[edge.To]) >= e.cfg.BufferCap {
			continue
		}
		p := st.queues[ei][0]
		st.queues[ei] = st.queues[ei][1:]
		st.buffers[edge.To] = append(st.buffers[edge.To], p)
		sort.Ints(st.buffers[edge.To])
	}
	return st
}

// commitOnce applies one specific intent ordering for the commit phase.
func (e *Enumerator) commitOnce(st EState, intents []intent) EState {
	st.buffers = cloneInts(st.buffers)
	st.queues = cloneInts(st.queues)
	blocked := make(map[int]bool)
	for _, it := range intents {
		if len(st.queues[it.edge]) < e.cfg.Edges[it.edge].Cap {
			st.queues[it.edge] = append(st.queues[it.edge], it.pkt)
		} else {
			blocked[it.pkt] = true
		}
	}
	for n := 0; n < e.cfg.Nodes; n++ {
		out := st.buffers[n][:0]
		for _, pid := range st.buffers[n] {
			if blocked[pid] {
				out = append(out, pid)
			}
		}
		st.buffers[n] = out
	}
	return st
}

func (e *Enumerator) intentsOf(st EState) []intent {
	var intents []intent
	for n := 0; n < e.cfg.Nodes; n++ {
		for _, pid := range st.buffers[n] {
			p := e.pkts[pid]
			nxt := e.cfg.NextHop[n][p.Dst]
			intents = append(intents, intent{pkt: pid, edge: e.edgeIdx[n][nxt]})
		}
	}
	return intents
}

// permutations calls fn for every permutation of 0..n-1.
func permutations(n int, fn func([]int)) {
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			fn(append([]int(nil), perm...))
			return
		}
		for i := k; i < n; i++ {
			perm[k], perm[i] = perm[i], perm[k]
			rec(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	rec(0)
}

// successors returns all distinct next states after one full tick.
func (e *Enumerator) successors(st EState) []EState {
	base := EState{
		buffers:   cloneInts(st.buffers),
		queues:    cloneInts(st.queues),
		pending:   cloneInts(st.pending),
		delivered: st.delivered,
	}
	e.inject(&base)
	seen := make(map[string]bool)
	var out []EState
	nEdges := len(e.cfg.Edges)
	permutations(nEdges, func(edgeOrder []int) {
		afterRecv := e.receiveOnce(base, edgeOrder)
		e.deliver(&afterRecv)
		intents := e.intentsOf(afterRecv)
		permutations(len(intents), func(perm []int) {
			ordered := make([]intent, len(intents))
			for i, idx := range perm {
				ordered[i] = intents[idx]
			}
			next := e.commitOnce(afterRecv, ordered)
			k := next.key()
			if !seen[k] {
				seen[k] = true
				out = append(out, next)
			}
		})
	})
	return out
}

// checkInvariants verifies conservation and capacity bounds on a state.
func (e *Enumerator) checkInvariants(st EState) error {
	where := make(map[int]int) // packet id -> occurrence count
	for n := 0; n < e.cfg.Nodes; n++ {
		if len(st.buffers[n]) > e.cfg.BufferCap {
			return fmt.Errorf("node %d buffer overflow", n)
		}
		for _, pid := range st.buffers[n] {
			where[pid]++
		}
		for _, pid := range st.pending[n] {
			where[pid]++
		}
	}
	for ei, edge := range e.cfg.Edges {
		if len(st.queues[ei]) > edge.Cap {
			return fmt.Errorf("edge %d->%d queue overflow", edge.From, edge.To)
		}
		for _, pid := range st.queues[ei] {
			where[pid]++
		}
	}
	for pid := 0; pid < len(e.pkts); pid++ {
		cnt := where[pid]
		if st.delivered&(1<<uint(pid)) != 0 {
			cnt++
		}
		if cnt != 1 {
			return fmt.Errorf("packet %d occurs %d times (loss or duplication)", pid, cnt)
		}
	}
	return nil
}

// EnumResult summarizes an exhaustive exploration.
type EnumResult struct {
	States         int
	TerminalOK     int      // terminal states with all packets delivered
	TerminalDead   int      // terminal states with undelivered packets
	DeadWitness    []EState // one trace leading to a deadlock, if any
	DeadCycle      []WaitEdge
	InvariantError error
}

// ExploredStateCount is a safety bound against accidental blow-ups.
const MaxEnumStates = 2000000

// Run explores the full reachable state space with BFS.
func (e *Enumerator) Run() EnumResult {
	var res EnumResult
	init := e.initial()
	if err := e.checkInvariants(init); err != nil {
		res.InvariantError = err
		return res
	}
	index := map[string]int{init.key(): 0}
	states := []EState{init}
	parent := []int{-1}
	for head := 0; head < len(states); head++ {
		st := states[head]
		succs := e.successors(st)
		// A state is terminal when its only successor is itself.
		terminal := true
		for _, nx := range succs {
			if nx.key() != st.key() {
				terminal = false
			}
			k := nx.key()
			if _, ok := index[k]; !ok {
				if len(states) >= MaxEnumStates {
					res.InvariantError = fmt.Errorf("state space exceeds %d states", MaxEnumStates)
					return res
				}
				if err := e.checkInvariants(nx); err != nil {
					res.InvariantError = err
					return res
				}
				index[k] = len(states)
				states = append(states, nx)
				parent = append(parent, head)
			}
		}
		if terminal {
			if st.delivered == (uint64(1)<<uint(len(e.pkts)))-1 {
				res.TerminalOK++
			} else {
				res.TerminalDead++
				if res.DeadWitness == nil {
					// Reconstruct the trace back to the initial state.
					var trace []EState
					for cur := head; cur != -1; cur = parent[cur] {
						trace = append(trace, states[cur])
					}
					for i, j := 0, len(trace)-1; i < j; i, j = i+1, j-1 {
						trace[i], trace[j] = trace[j], trace[i]
					}
					res.DeadWitness = trace
					res.DeadCycle = FindWaitCycle(e.cfg, e.edgeIdx, st.buffers, st.queues, e.pkts)
				}
			}
		}
	}
	res.States = len(states)
	return res
}

// DeliveredList decodes the delivered bitmask of a state.
func (e *Enumerator) DeliveredList(st EState) []int {
	var out []int
	for pid := 0; pid < len(e.pkts); pid++ {
		if st.delivered&(1<<uint(pid)) != 0 {
			out = append(out, pid)
		}
	}
	return out
}

// FormatState renders an EState for evidence output.
func (e *Enumerator) FormatState(st EState) string {
	return FormatState(e.cfg, st.buffers, st.queues, st.pending, e.DeliveredList(st))
}
