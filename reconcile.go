// Package main implements a state-history reconciliation tool.
//
// It rebuilds the final state of a business object from multi-source event
// logs. Logs may contain duplicates, missing entries, and unreliable
// timestamps, but every event keeps causal references to its predecessors.
// A reconstruction is legal iff it applies every event in an order that
// respects the causal partial order (timestamps are never used for ordering).
// The tool computes the set of final states reachable by ALL legal
// reconstructions and only reports a definitive conclusion when that set is
// a singleton. Contradictions (causal cycles or conflicting records for the
// same event id) are reported as a minimal subset of events.
package main

import (
	"encoding/json"
	"fmt"
	"math/bits"
	"sort"
)

// MaxEvents bounds the input size; the subset DP is exponential in it.
const MaxEvents = 20

// Op is a single mutation applied to the object state.
type Op struct {
	Kind  string `json:"kind"`            // "set" | "add" | "del"
	Field string `json:"field"`           // target field
	Value any    `json:"value,omitempty"` // for "set"
	Delta int64  `json:"delta,omitempty"` // for "add"
}

// Event is one log record. Time is advisory only and never used to order
// events; Causes lists ids of events that must be applied before this one.
type Event struct {
	ID     string   `json:"id"`
	Time   int64    `json:"time,omitempty"`
	Source string   `json:"source,omitempty"`
	Causes []string `json:"causes,omitempty"`
	Op     Op       `json:"op"`
}

// State is the reconstructed object: a field -> value map.
type State map[string]any

// Result is the outcome of reconciliation.
type Result struct {
	Status  string   `json:"status"`            // "unique" | "ambiguous" | "contradiction"
	State   State    `json:"state,omitempty"`   // definitive final state (unique only)
	States  []State  `json:"states,omitempty"`  // all possible final states (ambiguous only)
	Subset  []string `json:"subset,omitempty"`  // minimal contradictory event ids
	Reason  string   `json:"reason,omitempty"`  // contradiction explanation
	Missing []string `json:"missing,omitempty"` // causal refs pointing at absent events
}

// normalize returns a canonical copy of e: causes sorted and deduplicated.
func normalize(e Event) Event {
	causes := append([]string(nil), e.Causes...)
	sort.Strings(causes)
	out := causes[:0]
	for i, c := range causes {
		if i == 0 || c != causes[i-1] {
			out = append(out, c)
		}
	}
	e.Causes = out
	return e
}

// sameRecord reports whether two records carry identical semantic content.
// Source and Time are transport metadata (and Time is unreliable), so only
// the causal references and the operation are compared.
func sameRecord(a, b Event) bool {
	if len(a.Causes) != len(b.Causes) {
		return false
	}
	for i := range a.Causes {
		if a.Causes[i] != b.Causes[i] {
			return false
		}
	}
	ja, _ := json.Marshal(a.Op)
	jb, _ := json.Marshal(b.Op)
	return string(ja) == string(jb)
}

// apply returns a new State with op applied.
func apply(s State, op Op) State {
	next := make(State, len(s)+1)
	for k, v := range s {
		next[k] = v
	}
	switch op.Kind {
	case "set":
		next[op.Field] = op.Value
	case "del":
		delete(next, op.Field)
	case "add":
		var cur int64
		switch v := s[op.Field].(type) {
		case float64:
			cur = int64(v)
		case int64:
			cur = v
		}
		next[op.Field] = float64(cur + op.Delta)
	}
	return next
}

// stateKey is a canonical serialization used to deduplicate states.
func stateKey(s State) string {
	b, _ := json.Marshal(s) // encoding/json sorts map keys
	return string(b)
}

// interner assigns compact ids to distinct states and caches transitions
// (stateID, eventIndex) -> stateID so each is computed at most once.
type interner struct {
	evs    []Event
	ids    map[string]int
	states []State
	trans  map[[2]int]int
}

func newInterner(evs []Event) *interner {
	return &interner{evs: evs, ids: map[string]int{}, trans: map[[2]int]int{}}
}

func (in *interner) intern(s State) int {
	k := stateKey(s)
	if id, ok := in.ids[k]; ok {
		return id
	}
	id := len(in.states)
	in.ids[k] = id
	in.states = append(in.states, s)
	return id
}

func (in *interner) transition(sid, eventIdx int) int {
	key := [2]int{sid, eventIdx}
	if id, ok := in.trans[key]; ok {
		return id
	}
	id := in.intern(apply(in.states[sid], in.evs[eventIdx].Op))
	in.trans[key] = id
	return id
}

// Reconcile computes the set of final states over all legal reconstructions.
func Reconcile(events []Event) Result {
	if len(events) > MaxEvents {
		return Result{Status: "contradiction", Reason: fmt.Sprintf("too many events: %d > %d", len(events), MaxEvents)}
	}

	// 1. Deduplicate identical records; same id with conflicting content is
	//    a contradiction whose minimal subset is that single event id.
	byID := make(map[string]Event)
	order := []string{}
	for _, raw := range events {
		e := normalize(raw)
		if prev, ok := byID[e.ID]; ok {
			if !sameRecord(prev, e) {
				return Result{
					Status: "contradiction",
					Subset: []string{e.ID},
					Reason: fmt.Sprintf("conflicting records for event %q", e.ID),
				}
			}
			continue // exact duplicate from another source
		}
		byID[e.ID] = e
		order = append(order, e.ID)
	}

	n := len(order)
	idx := make(map[string]int, n)
	for i, id := range order {
		idx[id] = i
	}
	evs := make([]Event, n)
	for i, id := range order {
		evs[i] = byID[id]
	}

	// 2. Build the causal graph over present events. References to absent
	//    events (missing log entries) are tolerated and reported.
	deps := make([]uint32, n) // deps[i]: bitmask of events that must precede i
	adj := make([][]int, n)   // adj[i]: direct predecessors of i
	missingSet := map[string]bool{}
	for i, e := range evs {
		for _, c := range e.Causes {
			j, ok := idx[c]
			if !ok {
				missingSet[c] = true
				continue
			}
			deps[i] |= 1 << uint(j)
			adj[i] = append(adj[i], j)
		}
	}
	missing := []string{}
	for m := range missingSet {
		missing = append(missing, m)
	}
	sort.Strings(missing)

	// 3. Causal cycles are unsatisfiable. Report a shortest cycle as the
	//    minimal contradictory subset (removing any of its events breaks it).
	if cycle := shortestCycle(adj); cycle != nil {
		ids := make([]string, len(cycle))
		for i, j := range cycle {
			ids[i] = evs[j].ID
		}
		sort.Strings(ids)
		return Result{Status: "contradiction", Subset: ids, Reason: "causal cycle", Missing: missing}
	}

	// 4. DP over subsets: reachable[mask] = set of states obtainable by
	//    applying exactly the events of mask in a causally legal order.
	full := uint32(1)<<uint(n) - 1
	in := newInterner(evs)
	reachable := make([]map[int]struct{}, full+1)
	reachable[0] = map[int]struct{}{in.intern(State{}): {}}
	for mask := uint32(1); mask <= full; mask++ {
		acc := map[int]struct{}{}
		for rem := mask; rem != 0; rem &= rem - 1 {
			bit := rem & -rem
			i := bits.TrailingZeros32(bit)
			prev := mask ^ bit
			// i may close mask only if all its predecessors are in prev.
			if deps[i]&^prev != 0 {
				continue
			}
			for sid := range reachable[prev] {
				acc[in.transition(sid, i)] = struct{}{}
			}
		}
		reachable[mask] = acc
	}

	finals := reachable[full]
	keys := make([]string, 0, len(finals))
	byKey := map[string]State{}
	for sid := range finals {
		k := stateKey(in.states[sid])
		keys = append(keys, k)
		byKey[k] = in.states[sid]
	}
	sort.Strings(keys)
	if len(keys) == 1 {
		return Result{Status: "unique", State: byKey[keys[0]], Missing: missing}
	}
	states := make([]State, len(keys))
	for i, k := range keys {
		states[i] = byKey[k]
	}
	return Result{Status: "ambiguous", States: states, Missing: missing}
}

// shortestCycle returns the node indices of a shortest directed cycle in the
// dependency graph (edges i -> predecessor), or nil if the graph is a DAG.
// A shortest cycle is a minimal contradictory subset: dropping any member
// makes the remaining constraints satisfiable.
func shortestCycle(adj [][]int) []int {
	n := len(adj)
	const inf = 1 << 30
	// All-pairs shortest paths (n <= MaxEvents, so Floyd-Warshall is cheap).
	dist := make([][]int, n)
	next := make([][]int, n)
	for i := range dist {
		dist[i] = make([]int, n)
		next[i] = make([]int, n)
		for j := range dist[i] {
			dist[i][j] = inf
			next[i][j] = -1
		}
		dist[i][i] = 0
	}
	for u := 0; u < n; u++ {
		for _, v := range adj[u] {
			if u == v {
				return []int{u} // self-dependency: minimal subset of size 1
			}
			if 1 < dist[u][v] {
				dist[u][v] = 1
				next[u][v] = v
			}
		}
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			if dist[i][k] == inf {
				continue
			}
			for j := 0; j < n; j++ {
				if d := dist[i][k] + dist[k][j]; d < dist[i][j] {
					dist[i][j] = d
					next[i][j] = next[i][k]
				}
			}
		}
	}
	// Shortest cycle: edge u->v plus shortest path v ~> u.
	best, bu, bv := inf, -1, -1
	for u := 0; u < n; u++ {
		for _, v := range adj[u] {
			if dist[v][u] != inf && 1+dist[v][u] < best {
				best, bu, bv = 1+dist[v][u], u, v
			}
		}
	}
	if bu < 0 {
		return nil
	}
	cycle := []int{bu}
	for cur := bv; cur != bu; {
		cycle = append(cycle, cur)
		cur = next[cur][bu]
	}
	return cycle
}
