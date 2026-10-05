// Package dagpath finds the lexicographically smallest concatenation path
// from s to t in a DAG whose edges carry string fragments.
//
// Ordering of candidate paths: first the concatenated string
// (lexicographic, compared as a whole, never per-edge), then fewer edges,
// then the lexicographically smallest sequence of edge IDs.
package dagpath

import (
	"errors"
	"strings"
)

// ErrNoPath is returned when t is unreachable from s.
var ErrNoPath = errors.New("dagpath: no path from s to t")

// ErrCycle is returned when the graph is not a DAG.
var ErrCycle = errors.New("dagpath: graph contains a cycle")

// Edge is a directed edge carrying a string fragment. The fragment may be
// empty. IDs must be unique within a graph; they are the final tie-breaker.
type Edge struct {
	ID    int
	From  int
	To    int
	Label string
}

// pstr is a persistent string: a cons list of non-empty chunks sharing
// suffixes, so DP states never re-concatenate large strings.
type pstr struct {
	label  string
	rest   *pstr
	length int
}

func cons(label string, rest *pstr) *pstr {
	if label == "" {
		return rest
	}
	n := 0
	if rest != nil {
		n = rest.length
	}
	return &pstr{label: label, rest: rest, length: len(label) + n}
}

func comparePStr(a, b *pstr) int {
	ia, ib := 0, 0
	for a != nil && b != nil {
		n := len(a.label) - ia
		if m := len(b.label) - ib; m < n {
			n = m
		}
		if d := strings.Compare(a.label[ia:ia+n], b.label[ib:ib+n]); d != 0 {
			return d
		}
		ia += n
		ib += n
		if ia == len(a.label) {
			a = a.rest
			ia = 0
		}
		if ib == len(b.label) {
			b = b.rest
			ib = 0
		}
	}
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	default:
		return 1
	}
}

func expandPStr(s *pstr) string {
	var sb strings.Builder
	for c := s; c != nil; c = c.rest {
		sb.WriteString(c.label)
	}
	return sb.String()
}

// idList is a persistent list of edge IDs, sharing suffixes like pstr.
type idList struct {
	id   int
	rest *idList
}

func compareIDs(a, b *idList) int {
	for a != nil && b != nil {
		if a.id != b.id {
			if a.id < b.id {
				return -1
			}
			return 1
		}
		a = a.rest
		b = b.rest
	}
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	default:
		return 1
	}
}

type solution struct {
	str     *pstr
	nEdges  int
	ids     *idList
	edge    Edge
	hasEdge bool
}

func lessSolution(a, b *solution) bool {
	if d := comparePStr(a.str, b.str); d != 0 {
		return d < 0
	}
	if a.nEdges != b.nEdges {
		return a.nEdges < b.nEdges
	}
	return compareIDs(a.ids, b.ids) < 0
}

type solver struct {
	adj     map[int][]Edge
	memo    map[int]*solution
	done    map[int]bool
	onStack map[int]bool
	t       int
}

func newSolver(edges []Edge, t int) *solver {
	sv := &solver{
		adj:     make(map[int][]Edge),
		memo:    make(map[int]*solution),
		done:    make(map[int]bool),
		onStack: make(map[int]bool),
		t:       t,
	}
	for _, e := range edges {
		sv.adj[e.From] = append(sv.adj[e.From], e)
	}
	return sv
}

// best returns the optimal continuation from v to t, or nil if unreachable.
func (sv *solver) best(v int) (*solution, error) {
	if sv.done[v] {
		return sv.memo[v], nil
	}
	if sv.onStack[v] {
		return nil, ErrCycle
	}
	sv.onStack[v] = true
	var best *solution
	if v == sv.t {
		best = &solution{}
	} else {
		for _, e := range sv.adj[v] {
			sub, err := sv.best(e.To)
			if err != nil {
				return nil, err
			}
			if sub == nil {
				continue
			}
			cand := &solution{
				str:     cons(e.Label, sub.str),
				nEdges:  sub.nEdges + 1,
				ids:     &idList{id: e.ID, rest: sub.ids},
				edge:    e,
				hasEdge: true,
			}
			if best == nil || lessSolution(cand, best) {
				best = cand
			}
		}
	}
	sv.onStack[v] = false
	sv.done[v] = true
	sv.memo[v] = best
	return best, nil
}

// Solve returns the optimal path from s to t as a list of edges. The result
// is independent of internal shared structures and can be expanded with
// Expand. Returns ErrNoPath if t is unreachable, ErrCycle if the graph is
// not a DAG.
func Solve(edges []Edge, s, t int) ([]Edge, error) {
	sv := newSolver(edges, t)
	sol, err := sv.best(s)
	if err != nil {
		return nil, err
	}
	if sol == nil {
		return nil, ErrNoPath
	}
	path := make([]Edge, 0, sol.nEdges)
	for cur := sol; cur.hasEdge; cur = sv.memo[cur.edge.To] {
		path = append(path, cur.edge)
	}
	return path, nil
}

// Expand concatenates the fragments of a path returned by Solve.
func Expand(path []Edge) string {
	var sb strings.Builder
	for _, e := range path {
		sb.WriteString(e.Label)
	}
	return sb.String()
}
