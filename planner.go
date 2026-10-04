package main

import (
	"fmt"
	"sort"
	"time"
)

// ObjectInput describes one object in the deletion plan request.
type ObjectInput struct {
	ID             string     `json:"id"`
	RetentionUntil *time.Time `json:"retentionUntil,omitempty"`
	LegalHold      bool       `json:"legalHold,omitempty"`
	StrongRefs     []string   `json:"strongRefs,omitempty"`
	WeakRefs       []string   `json:"weakRefs,omitempty"`
}

// PlanInput is the top-level request document.
type PlanInput struct {
	Objects []ObjectInput `json:"objects"`
}

// BlockChain explains one permanent-blocking cause: the root cause object
// and the strong-reference path along which the block propagates.
type BlockChain struct {
	Cause  string   `json:"cause"` // e.g. "legal-hold"
	RootID string   `json:"rootId"`
	Path   []string `json:"path"` // object ids from the root cause to this object
}

// ObjectResult is the per-object planning outcome.
type ObjectResult struct {
	ID                   string       `json:"id"`
	PermanentlyBlocked   bool         `json:"permanentlyBlocked"`
	EarliestDeletionTime *time.Time   `json:"earliestDeletionTime,omitempty"`
	DeletableImmediately bool         `json:"deletableImmediately,omitempty"`
	Batch                *int         `json:"batch,omitempty"`
	BlockReasons         []BlockChain `json:"blockReasons,omitempty"`
	WeakRefs             []string     `json:"weakRefs,omitempty"` // echoed; never block
}

// Batch groups objects that become deletable at the same time.
type Batch struct {
	Index     int        `json:"index"`
	Immediate bool       `json:"immediate,omitempty"`
	Time      *time.Time `json:"time,omitempty"`
	ObjectIDs []string   `json:"objectIds"`
}

// PlanOutput is the top-level result document.
type PlanOutput struct {
	Objects  []ObjectResult `json:"objects"`
	Batches  []Batch        `json:"batches"`
	Warnings []string       `json:"warnings,omitempty"`
}

// Plan computes the deletion plan for the given input.
func Plan(in PlanInput) (PlanOutput, error) {
	objs, err := validate(in)
	if err != nil {
		return PlanOutput{}, err
	}
	out := PlanOutput{}

	// Weak references never block deletion; dangling ones are dropped with a warning.
	strong := make(map[string][]string, len(objs))
	weak := make(map[string][]string, len(objs))
	for _, o := range objs {
		strong[o.ID] = dedupe(o.StrongRefs)
		var kept []string
		for _, w := range dedupe(o.WeakRefs) {
			if _, ok := objs[w]; ok {
				kept = append(kept, w)
			} else {
				out.Warnings = append(out.Warnings,
					fmt.Sprintf("object %q: weak reference to unknown object %q ignored", o.ID, w))
			}
		}
		weak[o.ID] = kept
	}

	sccs := tarjanSCC(objs, strong)
	sccOf := make(map[string]int, len(objs))
	for i, scc := range sccs {
		for _, id := range scc {
			sccOf[id] = i
		}
	}

	// Condensation DAG. An edge u->v means u keeps v alive, so v depends on u.
	preds := make([]map[int]bool, len(sccs))
	succs := make([]map[int]bool, len(sccs))
	for i := range sccs {
		preds[i] = map[int]bool{}
		succs[i] = map[int]bool{}
	}
	for _, u := range sortedIDs(objs) {
		for _, v := range strong[u] {
			su, sv := sccOf[u], sccOf[v]
			if su != sv {
				preds[sv][su] = true
				succs[su][sv] = true
			}
		}
	}

	// Permanent blocking: legal-hold objects block everything reachable via
	// strong references (this also covers their whole cycle, since an SCC is
	// strongly connected). Chains are shortest strong-reference paths per root.
	chains := propagateLegalHolds(objs, strong)

	sccTime := make([]time.Time, len(sccs))
	sccBlocked := make([]bool, len(sccs))
	for i, scc := range sccs {
		for _, id := range scc {
			if _, blocked := chains[id]; blocked {
				sccBlocked[i] = true
			}
			if r := objs[id].RetentionUntil; r != nil && r.After(sccTime[i]) {
				sccTime[i] = *r
			}
		}
	}

	// Earliest deletion time per SCC: max of member retentions and of all
	// predecessor SCC times, evaluated in topological order of the DAG.
	for _, i := range topoOrder(preds, succs) {
		for p := range preds[i] {
			if !sccBlocked[p] && sccTime[p].After(sccTime[i]) {
				sccTime[i] = sccTime[p]
			}
		}
	}

	// Per-object results.
	results := make(map[string]ObjectResult, len(objs))
	for _, id := range sortedIDs(objs) {
		res := ObjectResult{ID: id, WeakRefs: weak[id]}
		if cs, blocked := chains[id]; blocked {
			res.PermanentlyBlocked = true
			res.BlockReasons = cs
		} else {
			t := sccTime[sccOf[id]]
			if t.IsZero() {
				res.DeletableImmediately = true
			} else {
				tt := t
				res.EarliestDeletionTime = &tt
			}
		}
		results[id] = res
	}

	// Batches: group deletable objects by earliest deletion time.
	timeKey := func(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
	byTime := map[string][]string{}
	var times []time.Time
	seen := map[string]bool{}
	for _, id := range sortedIDs(objs) {
		res := results[id]
		if res.PermanentlyBlocked {
			continue
		}
		t := sccTime[sccOf[id]]
		k := timeKey(t)
		if !seen[k] {
			seen[k] = true
			times = append(times, t)
		}
		byTime[k] = append(byTime[k], id)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	for i, t := range times {
		ids := byTime[timeKey(t)]
		idx := i
		b := Batch{Index: idx, Immediate: t.IsZero(), ObjectIDs: ids}
		if !t.IsZero() {
			tt := t
			b.Time = &tt
		}
		out.Batches = append(out.Batches, b)
		for _, id := range ids {
			res := results[id]
			res.Batch = &idx
			results[id] = res
		}
	}

	for _, id := range sortedIDs(objs) {
		out.Objects = append(out.Objects, results[id])
	}
	return out, nil
}

func validate(in PlanInput) (map[string]ObjectInput, error) {
	objs := make(map[string]ObjectInput, len(in.Objects))
	for _, o := range in.Objects {
		if o.ID == "" {
			return nil, fmt.Errorf("object with empty id")
		}
		if _, dup := objs[o.ID]; dup {
			return nil, fmt.Errorf("duplicate object id %q", o.ID)
		}
		objs[o.ID] = o
	}
	for _, o := range in.Objects {
		for _, r := range o.StrongRefs {
			if _, ok := objs[r]; !ok {
				return nil, fmt.Errorf("object %q: strong reference to unknown object %q", o.ID, r)
			}
		}
	}
	return objs, nil
}

// propagateLegalHolds returns, for every permanently blocked object, one
// reason chain per legal-hold root that reaches it via strong references.
func propagateLegalHolds(objs map[string]ObjectInput, strong map[string][]string) map[string][]BlockChain {
	var roots []string
	for _, id := range sortedIDs(objs) {
		if objs[id].LegalHold {
			roots = append(roots, id)
		}
	}
	chains := map[string][]BlockChain{}
	for _, root := range roots {
		parent := map[string]string{root: ""}
		queue := []string{root}
		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			for _, v := range strong[u] {
				if _, seen := parent[v]; !seen {
					parent[v] = u
					queue = append(queue, v)
				}
			}
		}
		for id := range parent {
			path := []string{}
			for cur := id; ; cur = parent[cur] {
				path = append([]string{cur}, path...)
				if cur == root {
					break
				}
			}
			chains[id] = append(chains[id], BlockChain{Cause: "legal-hold", RootID: root, Path: path})
		}
	}
	for id := range chains {
		sort.Slice(chains[id], func(i, j int) bool { return chains[id][i].RootID < chains[id][j].RootID })
	}
	return chains
}

// tarjanSCC returns the strongly connected components of the strong-reference
// graph. Components and their members are deterministically ordered.
func tarjanSCC(objs map[string]ObjectInput, strong map[string][]string) [][]string {
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	counter := 0
	var sccs [][]string

	var visit func(v string)
	visit = func(v string) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range strong[v] {
			if _, seen := index[w]; !seen {
				visit(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sort.Strings(scc)
			sccs = append(sccs, scc)
		}
	}
	for _, id := range sortedIDs(objs) {
		if _, seen := index[id]; !seen {
			visit(id)
		}
	}
	sort.Slice(sccs, func(i, j int) bool { return sccs[i][0] < sccs[j][0] })
	return sccs
}

// topoOrder returns a deterministic topological ordering of the condensation
// DAG (predecessors before dependents).
func topoOrder(preds, succs []map[int]bool) []int {
	indeg := make([]int, len(preds))
	for i := range preds {
		indeg[i] = len(preds[i])
	}
	var ready []int
	for i := range indeg {
		if indeg[i] == 0 {
			ready = append(ready, i)
		}
	}
	sort.Ints(ready)
	var order []int
	for len(ready) > 0 {
		u := ready[0]
		ready = ready[1:]
		order = append(order, u)
		for v := range succs[u] {
			indeg[v]--
			if indeg[v] == 0 {
				ready = append(ready, v)
				sort.Ints(ready)
			}
		}
	}
	return order
}

func sortedIDs(objs map[string]ObjectInput) []string {
	ids := make([]string, 0, len(objs))
	for id := range objs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
