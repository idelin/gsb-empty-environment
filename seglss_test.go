package seglss

import (
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"testing"
)

// bruteForce enumerates every composition of n into parts of size >= m and
// applies the same tie-breaking rules (min loss, then fewer segments, then
// lexicographically smallest cuts). It is the independent reference for the
// dynamic program.
func bruteForce(xs []int64, m int, penalty *big.Rat) *Result {
	n := len(xs)
	s, err := NewSolver(xs, m)
	if err != nil {
		panic(err)
	}

	var best *Result
	better := func(cand *Result) {
		if best == nil {
			best = cand
			return
		}
		if c := cand.Loss.Cmp(best.Loss); c != 0 {
			if c < 0 {
				best = cand
			}
			return
		}
		if len(cand.Segments) != len(best.Segments) {
			if len(cand.Segments) < len(best.Segments) {
				best = cand
			}
			return
		}
		for i := range cand.Segments {
			if cand.Segments[i].End != best.Segments[i].End {
				if cand.Segments[i].End < best.Segments[i].End {
					best = cand
				}
				return
			}
		}
	}

	var rec func(i int, segs []Segment, sse *big.Rat)
	rec = func(i int, segs []Segment, sse *big.Rat) {
		if i == n {
			loss := new(big.Rat).Set(sse)
			loss.Add(loss, new(big.Rat).Mul(penalty, big.NewRat(int64(len(segs)), 1)))
			cp := make([]Segment, len(segs))
			copy(cp, segs)
			better(&Result{Loss: loss, SSE: new(big.Rat).Set(sse), Segments: cp})
			return
		}
		for j := i + m; j <= n; j++ {
			cost := s.Cost(i, j)
			rec(j, append(segs, Segment{Start: i, End: j, Mean: s.mean(i, j)}),
				new(big.Rat).Add(sse, cost))
		}
	}
	rec(0, nil, new(big.Rat))
	return best
}

func checkAgainstBruteForce(t *testing.T, xs []int64, m int, penalty *big.Rat) {
	t.Helper()
	s, err := NewSolver(xs, m)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	got, err := s.Solve(penalty)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	want := bruteForce(xs, m, penalty)

	if got.Loss.Cmp(want.Loss) != 0 {
		t.Errorf("xs=%v m=%d p=%s: loss=%s, want %s", xs, m, penalty, got.Loss, want.Loss)
	}
	if got.SSE.Cmp(want.SSE) != 0 {
		t.Errorf("xs=%v m=%d p=%s: sse=%s, want %s", xs, m, penalty, got.SSE, want.SSE)
	}
	if got.Cuts() == nil && want.Cuts() == nil {
		// reflect.DeepEqual distinguishes nil from empty; normalize below.
	}
	if !reflect.DeepEqual(got.Cuts(), want.Cuts()) {
		t.Errorf("xs=%v m=%d p=%s: cuts=%v, want %v", xs, m, penalty, got.Cuts(), want.Cuts())
	}
	for i, seg := range got.Segments {
		if seg.Mean.Cmp(want.Segments[i].Mean) != 0 {
			t.Errorf("xs=%v m=%d p=%s: mean[%d]=%s, want %s",
				xs, m, penalty, i, seg.Mean, want.Segments[i].Mean)
		}
	}
	// Loss must equal SSE + penalty*segments exactly.
	sum := new(big.Rat).Set(got.SSE)
	sum.Add(sum, new(big.Rat).Mul(penalty, big.NewRat(int64(len(got.Segments)), 1)))
	if sum.Cmp(got.Loss) != 0 {
		t.Errorf("xs=%v m=%d p=%s: loss %s != sse+penalty*segs %s", xs, m, penalty, got.Loss, sum)
	}
}

func penalties() []*big.Rat {
	return []*big.Rat{
		big.NewRat(0, 1),
		big.NewRat(1, 3),
		big.NewRat(1, 2),
		big.NewRat(1, 1),
		big.NewRat(2, 1),
		big.NewRat(7, 2),
		big.NewRat(10, 1),
	}
}

func TestAgainstBruteForceExhaustive(t *testing.T) {
	// All short binary/ternary sequences, every feasible m and penalty.
	for n := 1; n <= 8; n++ {
		total := 1
		for i := 0; i < n; i++ {
			total *= 3
		}
		for code := 0; code < total; code++ {
			xs := make([]int64, n)
			c := code
			for i := 0; i < n; i++ {
				xs[i] = int64(c%3) - 1
				c /= 3
			}
			for m := 1; m <= n; m++ {
				for _, p := range penalties() {
					checkAgainstBruteForce(t, xs, m, p)
				}
			}
		}
	}
}

func TestAgainstBruteForceRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(12)
		xs := make([]int64, n)
		for i := range xs {
			xs[i] = int64(rng.Intn(11) - 5)
		}
		m := 1 + rng.Intn(3)
		if m > n {
			m = n
		}
		for _, p := range penalties() {
			checkAgainstBruteForce(t, xs, m, p)
		}
	}
}

func TestPenaltyReuseAcrossParameters(t *testing.T) {
	// One Solver, many penalties: results must match a freshly built Solver
	// (and hence the shared interval costs must be consistent).
	xs := []int64{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5}
	s, err := NewSolver(xs, 2)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	for _, p := range penalties() {
		got, err := s.Solve(p)
		if err != nil {
			t.Fatalf("Solve(%s): %v", p, err)
		}
		fresh, _ := NewSolver(xs, 2)
		want, err := fresh.Solve(p)
		if err != nil {
			t.Fatalf("fresh Solve(%s): %v", p, err)
		}
		if got.Loss.Cmp(want.Loss) != 0 || !reflect.DeepEqual(got.Cuts(), want.Cuts()) {
			t.Errorf("p=%s: reused solver gives %v loss=%s, fresh gives %v loss=%s",
				p, got.Cuts(), got.Loss, want.Cuts(), want.Loss)
		}
	}
}

func TestKnownAnswer(t *testing.T) {
	// xs = {0, 0, 10, 10}, m = 2, penalty = 1/2.
	// One segment: mean 5, SSE = 25+25+25+25 = 100, loss = 100.5.
	// Two segments: SSE = 0, loss = 1. Optimum is two segments.
	s, err := NewSolver([]int64{0, 0, 10, 10}, 2)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	res, err := s.Solve(big.NewRat(1, 2))
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.Loss.Cmp(big.NewRat(1, 1)) != 0 {
		t.Errorf("loss = %s, want 1", res.Loss)
	}
	if got := res.Cuts(); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("cuts = %v, want [2]", got)
	}
	for i, seg := range res.Segments {
		want := []*big.Rat{big.NewRat(0, 1), big.NewRat(10, 1)}[i]
		if seg.Mean.Cmp(want) != 0 {
			t.Errorf("mean[%d] = %s, want %s", i, seg.Mean, want)
		}
	}
}

func TestTieBreakFewerSegments(t *testing.T) {
	// xs = {0, 2}, m = 1, penalty = 2: one segment gives SSE 2 + 2 = 4,
	// two segments give 0 + 4 = 4. Equal loss: prefer fewer segments.
	s, err := NewSolver([]int64{0, 2}, 1)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	res, err := s.Solve(big.NewRat(2, 1))
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Segments) != 1 {
		t.Errorf("segments = %d, want 1 (res=%s)", len(res.Segments), res)
	}
	if res.Loss.Cmp(big.NewRat(4, 1)) != 0 {
		t.Errorf("loss = %s, want 4", res.Loss)
	}
}

func TestTieBreakLexicographicCuts(t *testing.T) {
	// xs = {0, 1, 5, 1, 0}, m = 2, penalty = 1: two segments are optimal
	// (loss 29/2 + 2 < 86/5 + 1), and by symmetry cuts {2} and {3} tie
	// with SSE 29/2 each. Lexicographic tie-breaking picks {2}.
	s, err := NewSolver([]int64{0, 1, 5, 1, 0}, 2)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	res, err := s.Solve(big.NewRat(1, 1))
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if got := res.Cuts(); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("cuts = %v, want [2]", got)
	}
	if res.Loss.Cmp(big.NewRat(33, 2)) != 0 {
		t.Errorf("loss = %s, want 33/2", res.Loss)
	}
}

func TestErrors(t *testing.T) {
	if _, err := NewSolver([]int64{1, 2}, 0); err == nil {
		t.Error("expected error for m = 0")
	}
	s, err := NewSolver([]int64{1, 2}, 3)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	if _, err := s.Solve(big.NewRat(1, 1)); err == nil {
		t.Error("expected error for n < m")
	}
}

func ExampleSolver_Solve() {
	s, _ := NewSolver([]int64{1, 2, 10, 11, 3}, 1)
	res, _ := s.Solve(big.NewRat(3, 2))
	fmt.Println(res)
	// Output: loss=11/2 sse=1 segments=3 {[0,2) mean=3/2 [2,4) mean=21/2 [4,5) mean=3}
}
