package dagpath

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// bruteForce enumerates every s->t path and picks the minimum under the
// full ordering: concatenated string, then edge count, then edge ID
// sequence. Edge IDs are unique, so the minimum is unique.
func bruteForce(adj map[int][]Edge, s, t int) ([]Edge, bool) {
	var best []Edge
	bestKey := ""
	var bestIDs []int
	keyOf := func(p []Edge) string {
		var sb strings.Builder
		for _, e := range p {
			sb.WriteString(e.Label)
		}
		return sb.String()
	}
	idsOf := func(p []Edge) []int {
		ids := make([]int, len(p))
		for i, e := range p {
			ids[i] = e.ID
		}
		return ids
	}
	lessIDs := func(a, b []int) bool {
		for i := 0; i < len(a) && i < len(b); i++ {
			if a[i] != b[i] {
				return a[i] < b[i]
			}
		}
		return len(a) < len(b)
	}
	var visit func(v int, path []Edge)
	visit = func(v int, path []Edge) {
		if v == t {
			k := keyOf(path)
			ids := idsOf(path)
			if best == nil || k < bestKey ||
				(k == bestKey && len(path) < len(best)) ||
				(k == bestKey && len(path) == len(best) && lessIDs(ids, bestIDs)) {
				best = append([]Edge(nil), path...)
				bestKey = k
				bestIDs = ids
			}
			return
		}
		for _, e := range adj[v] {
			visit(e.To, append(path, e))
		}
	}
	visit(s, nil)
	return best, best != nil
}

func buildAdj(edges []Edge) map[int][]Edge {
	adj := make(map[int][]Edge)
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e)
	}
	return adj
}

func pathIDs(path []Edge) []int {
	ids := make([]int, len(path))
	for i, e := range path {
		ids[i] = e.ID
	}
	return ids
}

func randomDAG(r *rand.Rand, n int) []Edge {
	labels := []string{"", "a", "b", "c", "ab", "ba", "aa", "abc", "acb", "z"}
	var edges []Edge
	id := 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if r.Intn(2) == 0 {
				continue
			}
			k := 1
			if r.Intn(4) == 0 {
				k = 2 // parallel edges with distinct IDs
			}
			for ; k > 0; k-- {
				edges = append(edges, Edge{ID: id, From: i, To: j, Label: labels[r.Intn(len(labels))]})
				id++
			}
		}
	}
	// Shuffle IDs so edge-ID order is independent of generation order.
	perm := r.Perm(id)
	for i := range edges {
		edges[i].ID = perm[edges[i].ID]
	}
	return edges
}

func TestAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 500; trial++ {
		n := 2 + r.Intn(8)
		edges := randomDAG(r, n)
		s, tgt := 0, n-1
		got, err := Solve(edges, s, tgt)
		want, ok := bruteForce(buildAdj(edges), s, tgt)
		if !ok {
			if err != ErrNoPath {
				t.Fatalf("trial %d: no path exists, got err=%v path=%v", trial, err, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: unexpected error %v", trial, err)
		}
		if !reflect.DeepEqual(pathIDs(got), pathIDs(want)) {
			t.Fatalf("trial %d: got ids %v (str %q), want ids %v (str %q)",
				trial, pathIDs(got), Expand(got), pathIDs(want), Expand(want))
		}
	}
}

// TestPrefixRelations checks that every suffix of the optimal path is itself
// the optimal continuation from the node it starts at (Bellman optimality),
// and that the DP's shared-suffix strings expand to exactly those suffixes.
func TestPrefixRelations(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 300; trial++ {
		n := 2 + r.Intn(8)
		edges := randomDAG(r, n)
		s, tgt := 0, n-1
		sv := newSolver(edges, tgt)
		sol, err := sv.best(s)
		if err != nil || sol == nil {
			continue
		}
		var path []Edge
		for cur := sol; cur.hasEdge; cur = sv.memo[cur.edge.To] {
			path = append(path, cur.edge)
		}
		// For every prefix, the remaining labels must equal the expansion of
		// the DP state at the node reached by that prefix.
		node := s
		cur := sol
		for i, e := range path {
			if cur.edge.ID != e.ID {
				t.Fatalf("trial %d: path edge %d inconsistent with DP state", trial, i)
			}
			var rest strings.Builder
			for _, f := range path[i:] {
				rest.WriteString(f.Label)
			}
			if got := expandPStr(sv.memo[node].str); got != rest.String() {
				t.Fatalf("trial %d: node %d DP string %q != remaining labels %q",
					trial, node, got, rest.String())
			}
			node = e.To
			cur = sv.memo[e.To]
		}
		if got := expandPStr(sv.memo[tgt].str); got != "" {
			t.Fatalf("trial %d: target DP string %q, want empty", trial, got)
		}
	}
}

// TestSharedSuffixes verifies the persistent-string representation never
// duplicates label bytes: the union of all live pstr chunks holds at most
// the bytes of the input labels, however large the expanded strings are.
func TestSharedSuffixes(t *testing.T) {
	big := strings.Repeat("x", 4096)
	edges := []Edge{
		{ID: 0, From: 0, To: 1, Label: "a"},
		{ID: 1, From: 0, To: 2, Label: "b"},
		{ID: 2, From: 1, To: 3, Label: big},
		{ID: 3, From: 2, To: 3, Label: big},
		{ID: 4, From: 3, To: 4, Label: big},
	}
	sv := newSolver(edges, 4)
	if _, err := sv.best(0); err != nil {
		t.Fatal(err)
	}
	seen := make(map[*pstr]bool)
	total := 0
	var walk func(p *pstr)
	walk = func(p *pstr) {
		for ; p != nil; p = p.rest {
			if seen[p] {
				return
			}
			seen[p] = true
			total += len(p.label)
		}
	}
	for _, sol := range sv.memo {
		if sol != nil {
			walk(sol.str)
		}
	}
	input := 0
	for _, e := range edges {
		input += len(e.Label)
	}
	if total > input {
		t.Fatalf("stored %d label bytes, input had %d: strings were re-concatenated", total, input)
	}
	// Structural sharing: both branches converge on node 3, so their
	// continuations must share the exact same suffix cells.
	if sv.memo[1].str.rest != sv.memo[2].str.rest {
		t.Fatal("expected shared suffix cells between converging branches")
	}
}

// TestWholeStringComparison is the case where greedy per-edge label ordering
// fails: "a" < "ab" per edge, but "ab" < "ac" as whole strings.
func TestWholeStringComparison(t *testing.T) {
	edges := []Edge{
		{ID: 0, From: 0, To: 1, Label: "a"},
		{ID: 1, From: 1, To: 2, Label: "c"},
		{ID: 2, From: 0, To: 2, Label: "ab"},
	}
	path, err := Solve(edges, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := Expand(path); got != "ab" {
		t.Fatalf("got %q, want %q (per-edge greedy would pick %q)", got, "ab", "ac")
	}
}

func TestEmptyFragmentsAndTieBreaks(t *testing.T) {
	// Equal strings: fewer edges wins over the empty-label detour.
	edges := []Edge{
		{ID: 0, From: 0, To: 1, Label: ""},
		{ID: 1, From: 1, To: 2, Label: ""},
		{ID: 2, From: 0, To: 2, Label: ""},
	}
	path, err := Solve(edges, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pathIDs(path), []int{2}) {
		t.Fatalf("got ids %v, want [2]", pathIDs(path))
	}
	// Equal strings and equal edge counts: smallest ID sequence wins.
	edges = []Edge{
		{ID: 5, From: 0, To: 2, Label: ""},
		{ID: 3, From: 0, To: 2, Label: ""},
		{ID: 4, From: 0, To: 1, Label: ""},
		{ID: 1, From: 1, To: 2, Label: ""},
	}
	path, err = Solve(edges, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pathIDs(path), []int{3}) {
		t.Fatalf("got ids %v, want [3]", pathIDs(path))
	}
	// ID sequence is compared lexicographically, not by first differing
	// edge count or sum: [1,9] beats [2].
	edges = []Edge{
		{ID: 2, From: 0, To: 3, Label: "x"},
		{ID: 1, From: 0, To: 1, Label: "x"},
		{ID: 9, From: 1, To: 3, Label: ""},
	}
	path, err = Solve(edges, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pathIDs(path), []int{2}) {
		t.Fatalf("got ids %v, want [2] (fewer edges beats id sequence)", pathIDs(path))
	}
}

func TestNoPathAndCycle(t *testing.T) {
	if _, err := Solve([]Edge{{ID: 0, From: 0, To: 1, Label: "a"}}, 0, 2); err != ErrNoPath {
		t.Fatalf("got %v, want ErrNoPath", err)
	}
	cyclic := []Edge{
		{ID: 0, From: 0, To: 1, Label: ""},
		{ID: 1, From: 1, To: 0, Label: ""},
		{ID: 2, From: 1, To: 2, Label: ""},
	}
	if _, err := Solve(cyclic, 0, 2); err != ErrCycle {
		t.Fatalf("got %v, want ErrCycle", err)
	}
}

func TestSourceEqualsTarget(t *testing.T) {
	path, err := Solve(nil, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 0 || Expand(path) != "" {
		t.Fatalf("got %v, want empty path", path)
	}
}

func ExampleSolve() {
	edges := []Edge{
		{ID: 0, From: 0, To: 1, Label: "a"},
		{ID: 1, From: 1, To: 2, Label: "c"},
		{ID: 2, From: 0, To: 2, Label: "ab"},
	}
	path, _ := Solve(edges, 0, 2)
	fmt.Println(Expand(path))
	// Output: ab
}
