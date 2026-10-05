package edgecolor

import (
	"math/rand"
	"testing"
)

// checkNormalized verifies that round indices appear for the first time in
// the order 0,1,2,... when scanning edge IDs ascending.
func checkNormalized(t *testing.T, s *Scheduler, rounds [][]int) {
	t.Helper()
	roundOf := make(map[int]int)
	for r, round := range rounds {
		for _, id := range round {
			roundOf[id] = r
		}
	}
	next := 0
	seen := make(map[int]bool)
	for _, e := range s.Edges() {
		r, ok := roundOf[e.ID]
		if !ok {
			t.Fatalf("edge %d missing from rounds", e.ID)
		}
		if !seen[r] {
			if r != next {
				t.Fatalf("normalization broken: edge %d introduces round %d, want %d", e.ID, r, next)
			}
			seen[r] = true
			next++
		}
	}
	if next != len(rounds) {
		t.Fatalf("normalization: %d distinct first-seen rounds, but %d rounds", next, len(rounds))
	}
}

func checkSchedule(t *testing.T, s *Scheduler) [][]int {
	t.Helper()
	rounds := s.Rounds()
	if err := s.Verify(rounds); err != nil {
		t.Fatalf("invalid schedule: %v", err)
	}
	checkNormalized(t, s, rounds)
	return rounds
}

func TestParallelEdges(t *testing.T) {
	s := New()
	for i := 0; i < 5; i++ {
		s.AddEdge(0, 0) // 5 parallel edges between the same pair
	}
	rounds := checkSchedule(t, s)
	if len(rounds) != 5 {
		t.Fatalf("got %d rounds, want 5", len(rounds))
	}
	for r, round := range rounds {
		if len(round) != 1 || round[0] != r {
			t.Fatalf("round %d = %v, want [%d]", r, round, r)
		}
	}
}

func TestCompleteBipartite(t *testing.T) {
	s := New()
	for u := 0; u < 4; u++ {
		for v := 0; v < 4; v++ {
			s.AddEdge(u, v)
		}
	}
	rounds := checkSchedule(t, s)
	if len(rounds) != 4 {
		t.Fatalf("K4,4: got %d rounds, want 4", len(rounds))
	}
	for r, round := range rounds {
		if len(round) != 4 {
			t.Fatalf("round %d has %d edges, want a perfect matching of 4", r, len(round))
		}
	}
}

func TestDeleteReaddLeavesNoStaleColors(t *testing.T) {
	s := New()
	ids := []int{
		s.AddEdge(0, 0),
		s.AddEdge(0, 1),
		s.AddEdge(1, 0),
		s.AddEdge(1, 1),
		s.AddEdge(2, 1),
	}
	checkSchedule(t, s)

	// Delete edges, then add fresh ones; the recomputed schedule must only
	// reference live edges and use contiguous, normalized round indices.
	if !s.RemoveEdge(ids[1]) || !s.RemoveEdge(ids[3]) {
		t.Fatal("RemoveEdge reported missing edge")
	}
	rounds := checkSchedule(t, s)
	for _, round := range rounds {
		for _, id := range round {
			if id == ids[1] || id == ids[3] {
				t.Fatalf("deleted edge %d still scheduled", id)
			}
		}
	}

	s.AddEdge(0, 2)
	s.AddEdge(2, 0)
	s.AddEdge(3, 3)
	rounds = checkSchedule(t, s)
	if len(rounds) != s.MaxDegree() {
		t.Fatalf("rounds %d != max degree %d", len(rounds), s.MaxDegree())
	}

	// Removing everything must yield an empty schedule.
	for _, e := range s.Edges() {
		s.RemoveEdge(e.ID)
	}
	if got := s.Rounds(); got != nil {
		t.Fatalf("empty graph: got rounds %v, want nil", got)
	}
	if err := s.Verify(nil); err != nil {
		t.Fatalf("empty graph verify: %v", err)
	}
}

func TestRemoveMissingEdge(t *testing.T) {
	s := New()
	if s.RemoveEdge(42) {
		t.Fatal("RemoveEdge(42) on empty scheduler returned true")
	}
	id := s.AddEdge(0, 0)
	if s.RemoveEdge(id+1) || s.RemoveEdge(-1) {
		t.Fatal("RemoveEdge accepted a non-existent ID")
	}
	if !s.RemoveEdge(id) || s.RemoveEdge(id) {
		t.Fatal("RemoveEdge double-delete semantics broken")
	}
}

func TestVerifyRejectsBadSchedules(t *testing.T) {
	s := New()
	a := s.AddEdge(0, 0)
	b := s.AddEdge(0, 1)
	c := s.AddEdge(1, 0)

	if err := s.Verify([][]int{{a, b}, {c}}); err == nil {
		t.Fatal("accepted a round with a left-vertex conflict")
	}
	if err := s.Verify([][]int{{a, c}, {b}}); err == nil {
		t.Fatal("accepted a round with a right-vertex conflict")
	}
	if err := s.Verify([][]int{{a}, {b}}); err == nil {
		t.Fatal("accepted a schedule missing an edge")
	}
	if err := s.Verify([][]int{{a}, {b}, {c}}); err == nil {
		t.Fatal("accepted wrong round count")
	}
	if err := s.Verify([][]int{{a, 999}, {b, c}}); err == nil {
		t.Fatal("accepted unknown edge ID")
	}
	if err := s.Verify([][]int{{a}, {b, c, a}}); err == nil {
		t.Fatal("accepted duplicated edge")
	}
	if err := s.Verify(s.Rounds()); err != nil {
		t.Fatalf("rejected the scheduler's own output: %v", err)
	}
}

// TestFuzzAgainstBruteForce generates random small multigraphs, checks the
// schedule, and confirms via exhaustive search that the chromatic index
// equals the max degree. It also exercises random delete/re-add sequences.
func TestFuzzAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		nl := 1 + rng.Intn(4)
		nr := 1 + rng.Intn(4)
		m := rng.Intn(13)
		s := New()
		var pairs [][2]int
		for i := 0; i < m; i++ {
			u := rng.Intn(nl)
			v := rng.Intn(nr)
			s.AddEdge(u, v)
			pairs = append(pairs, [2]int{u, v})
		}

		rounds := checkSchedule(t, s)
		if want := bruteForceMinColors(pairs); len(rounds) != want {
			t.Fatalf("trial %d: scheduler used %d rounds, brute force optimum is %d (edges %v)",
				trial, len(rounds), want, pairs)
		}

		// Random deletions and re-additions; after each mutation the
		// schedule must stay valid and normalized.
		for step := 0; step < 6; step++ {
			live := s.Edges()
			if len(live) > 0 && rng.Intn(2) == 0 {
				s.RemoveEdge(live[rng.Intn(len(live))].ID)
			} else {
				s.AddEdge(rng.Intn(nl), rng.Intn(nr))
			}
			rounds = checkSchedule(t, s)
			var cur [][2]int
			for _, e := range s.Edges() {
				cur = append(cur, [2]int{e.Left, e.Right})
			}
			if want := bruteForceMinColors(cur); len(rounds) != want {
				t.Fatalf("trial %d step %d: %d rounds, brute force optimum %d (edges %v)",
					trial, step, len(rounds), want, cur)
			}
		}
	}
}
