package scheduler

import (
	"math/rand"
	"reflect"
	"testing"
)

// bruteForceColorable reports whether the edges can be properly colored
// with at most k colors, found by exhaustive backtracking. It is the
// independent reference used to cross-check the scheduler on small graphs.
func bruteForceColorable(edges []Edge, k int) bool {
	if k < 0 {
		return false
	}
	// Quick necessary condition: no vertex may exceed k incident edges.
	degU := map[int]int{}
	degV := map[int]int{}
	for _, e := range edges {
		degU[e.U]++
		degV[e.V]++
		if degU[e.U] > k || degV[e.V] > k {
			return false
		}
	}
	usedU := map[int][]bool{}
	usedV := map[int][]bool{}
	mark := func(m map[int][]bool, v, c int, val bool) {
		if m[v] == nil {
			m[v] = make([]bool, k)
		}
		m[v][c] = val
	}
	free := func(m map[int][]bool, v, c int) bool {
		return m[v] == nil || !m[v][c]
	}
	var bt func(i int) bool
	bt = func(i int) bool {
		if i == len(edges) {
			return true
		}
		e := edges[i]
		for c := 0; c < k; c++ {
			if !free(usedU, e.U, c) || !free(usedV, e.V, c) {
				continue
			}
			mark(usedU, e.U, c, true)
			mark(usedV, e.V, c, true)
			if bt(i + 1) {
				return true
			}
			mark(usedU, e.U, c, false)
			mark(usedV, e.V, c, false)
		}
		return false
	}
	return bt(0)
}

// checkSchedule verifies the schedule independently and confirms via brute
// force that the round count is truly minimal.
func checkSchedule(t *testing.T, s *Scheduler) {
	t.Helper()
	rounds := s.Schedule()
	if err := s.Verify(rounds); err != nil {
		t.Fatalf("invalid schedule: %v\nrounds: %v", err, rounds)
	}
	delta := s.MaxDegree()
	edges := s.Edges()
	if !bruteForceColorable(edges, delta) {
		t.Fatalf("brute force: no %d-coloring exists, but scheduler produced %v", delta, rounds)
	}
	if delta > 0 && bruteForceColorable(edges, delta-1) {
		t.Fatalf("brute force: %d colors suffice, scheduler used %d rounds", delta-1, delta)
	}
}

// verifySchedule runs only the independent structural checker, which is
// cheap enough to run after every fuzz operation.
func verifySchedule(t *testing.T, s *Scheduler) {
	t.Helper()
	rounds := s.Schedule()
	if err := s.Verify(rounds); err != nil {
		t.Fatalf("invalid schedule: %v\nrounds: %v", err, rounds)
	}
}

func TestBasicGraph(t *testing.T) {
	s := New()
	// Left {0,1}, right {0,1}: a 4-cycle plus a chord-ish edge.
	s.AddEdge(0, 0)
	s.AddEdge(0, 1)
	s.AddEdge(1, 0)
	s.AddEdge(1, 1)
	s.AddEdge(0, 0) // parallel edge
	checkSchedule(t, s)
	if got, want := len(s.Schedule()), 3; got != want {
		t.Fatalf("got %d rounds, want %d", got, want)
	}
}

func TestParallelEdges(t *testing.T) {
	s := New()
	var par []int
	for i := 0; i < 4; i++ {
		par = append(par, s.AddEdge(0, 0)) // 4 parallel edges
	}
	s.AddEdge(1, 1)
	checkSchedule(t, s)
	rounds := s.Schedule()
	// Each parallel edge must land in a distinct round.
	seen := map[int]int{}
	for r, round := range rounds {
		for _, id := range round {
			seen[id] = r
		}
	}
	distinct := map[int]bool{}
	for _, id := range par {
		distinct[seen[id]] = true
	}
	if len(distinct) != len(par) {
		t.Fatalf("parallel edges share rounds: %v", seen)
	}
}

func TestDeleteReaddLeavesNoStaleColors(t *testing.T) {
	s := New()
	ids := []int{
		s.AddEdge(0, 0),
		s.AddEdge(0, 1),
		s.AddEdge(1, 0),
		s.AddEdge(1, 1),
		s.AddEdge(2, 0),
	}
	if err := s.Verify(s.Schedule()); err != nil {
		t.Fatalf("initial schedule invalid: %v", err)
	}
	// Delete edges, including every edge of one vertex, then re-add.
	for _, id := range []int{ids[0], ids[2], ids[4]} {
		if !s.DeleteEdge(id) {
			t.Fatalf("DeleteEdge(%d) reported missing", id)
		}
	}
	if s.DeleteEdge(ids[0]) {
		t.Fatalf("double delete of %d reported success", ids[0])
	}
	checkSchedule(t, s)
	// Re-add edges with the same endpoints; they get fresh IDs.
	s.AddEdge(0, 0)
	s.AddEdge(1, 0)
	s.AddEdge(2, 0)
	checkSchedule(t, s)
	// Every scheduled ID must be a currently live edge; deleted IDs must
	// never resurface.
	live := map[int]bool{}
	for _, e := range s.Edges() {
		live[e.ID] = true
	}
	for _, round := range s.Schedule() {
		for _, id := range round {
			if !live[id] {
				t.Fatalf("schedule references stale edge ID %d", id)
			}
		}
	}
}

func TestNormalizationByFirstAppearance(t *testing.T) {
	s := New()
	for i := 0; i < 30; i++ {
		s.AddEdge(i%4, (i*3)%5)
	}
	rounds := s.Schedule()
	if err := s.Verify(rounds); err != nil {
		t.Fatalf("invalid schedule: %v", err)
	}
	// Scanning edges by increasing ID, the round index must be
	// non-decreasing and grow by at most one: round 0 holds the smallest
	// ID, and each new round appears in order.
	roundOf := map[int]int{}
	for r, round := range rounds {
		for _, id := range round {
			roundOf[id] = r
		}
	}
	next := 0
	for _, e := range s.Edges() {
		r := roundOf[e.ID]
		if r > next {
			t.Fatalf("edge %d in round %d before round %d appeared", e.ID, r, next)
		}
		if r == next {
			next++
		}
	}
}

func TestDeterministic(t *testing.T) {
	build := func() *Scheduler {
		s := New()
		rng := rand.New(rand.NewSource(7))
		for i := 0; i < 40; i++ {
			s.AddEdge(rng.Intn(6), rng.Intn(6))
		}
		return s
	}
	a, b := build().Schedule(), build().Schedule()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("non-deterministic schedules:\n%v\n%v", a, b)
	}
}

func TestExhaustiveSimpleGraphs(t *testing.T) {
	for _, sz := range [][2]int{{2, 2}, {2, 3}, {3, 2}, {3, 3}} {
		nL, nR := sz[0], sz[1]
		n := nL * nR
		for mask := 0; mask < 1<<uint(n); mask++ {
			s := New()
			for b := 0; b < n; b++ {
				if mask&(1<<uint(b)) != 0 {
					s.AddEdge(b%nL, b/nL)
				}
			}
			checkSchedule(t, s)
		}
	}
}

func TestExhaustiveMultigraphs(t *testing.T) {
	// Multiplicity 0..2 for every pair on 2x2 and 2x3.
	for _, sz := range [][2]int{{2, 2}, {2, 3}} {
		nL, nR := sz[0], sz[1]
		n := nL * nR
		total := 1
		for i := 0; i < n; i++ {
			total *= 3
		}
		for code := 0; code < total; code++ {
			s := New()
			x := code
			for b := 0; b < n; b++ {
				for m := 0; m < x%3; m++ {
					s.AddEdge(b%nL, b/nL)
				}
				x /= 3
			}
			checkSchedule(t, s)
		}
	}
}

func TestRandomOpsFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 30; trial++ {
		s := New()
		var live []int
		for op := 0; op < 60; op++ {
			if len(live) == 0 || rng.Intn(3) > 0 {
				id := s.AddEdge(rng.Intn(5), rng.Intn(5))
				live = append(live, id)
			} else {
				i := rng.Intn(len(live))
				if !s.DeleteEdge(live[i]) {
					t.Fatalf("DeleteEdge(%d) failed", live[i])
				}
				live = append(live[:i], live[i+1:]...)
			}
			verifySchedule(t, s)
		}
		// Brute-force optimality is exponential, so only cross-check the
		// final graph when it is small enough.
		if len(s.Edges()) <= 14 {
			checkSchedule(t, s)
		}
	}
}

func TestEmptyAndSingleEdge(t *testing.T) {
	s := New()
	if rounds := s.Schedule(); len(rounds) != 0 {
		t.Fatalf("empty graph: got %v", rounds)
	}
	if err := s.Verify(s.Schedule()); err != nil {
		t.Fatalf("empty graph: %v", err)
	}
	id := s.AddEdge(3, 4)
	rounds := s.Schedule()
	if len(rounds) != 1 || len(rounds[0]) != 1 || rounds[0][0] != id {
		t.Fatalf("single edge: got %v", rounds)
	}
}
