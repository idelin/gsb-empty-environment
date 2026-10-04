package main

import (
	"math/big"
	"math/rand"
	"testing"
)

func bi(x int64) *big.Int { return big.NewInt(x) }

func mkProblem(lo, hi []int64, cons []Constraint) *Problem {
	p := &Problem{Dims: len(lo)}
	for i := range lo {
		p.Lo = append(p.Lo, bi(lo[i]))
		p.Hi = append(p.Hi, bi(hi[i]))
	}
	p.Cons = cons
	return p
}

func con(b int64, coeffs ...int64) Constraint {
	c := Constraint{B: bi(b)}
	for _, a := range coeffs {
		c.A = append(c.A, bi(a))
	}
	return c
}

func mustCount(t *testing.T, p *Problem, budget int64) *big.Int {
	s, err := NewSolver(p, budget)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	n, err := s.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	return n
}

func TestCountNoConstraints(t *testing.T) {
	p := mkProblem([]int64{0, 0, 0}, []int64{4, 9, 1}, nil)
	if got := mustCount(t, p, 0); got.Cmp(bi(100)) != 0 {
		t.Fatalf("got %s want 100", got)
	}
}

func TestCountBigBoxBigInts(t *testing.T) {
	// 10^30 box, no constraints: count must be exact and instant.
	p := &Problem{Dims: 3}
	for i := 0; i < 3; i++ {
		p.Lo = append(p.Lo, bi(0))
		h, _ := new(big.Int).SetString("1000000000000000000000000000000", 10)
		p.Hi = append(p.Hi, h)
	}
	s, _ := NewSolver(p, 1000)
	n, err := s.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	h, _ := new(big.Int).SetString("1000000000000000000000000000000", 10)
	want := new(big.Int).Add(h, bigOne)
	want.Exp(want, bi(3), nil)
	if n.Cmp(want) != 0 {
		t.Fatalf("got %s want %s", n, want)
	}
}

func TestStrictBoundaries(t *testing.T) {
	// 0 <= x <= 10, 0 <= y <= 10, x + y < 5  =>  x + y <= 4  =>  15 points.
	p := mkProblem([]int64{0, 0}, []int64{10, 10},
		[]Constraint{con(4, 1, 1)})
	if got := mustCount(t, p, 0); got.Cmp(bi(15)) != 0 {
		t.Fatalf("got %s want 15", got)
	}
	// x - y > 3 over [-3, 3]^2: brute-check below in random test; direct:
	// pairs with x-y >= 4: x in -3..3, y in -3..3 => (1,-3),(2,-3..-2),(3,-3..-1)
	p2 := mkProblem([]int64{-3, -3}, []int64{3, 3},
		[]Constraint{con(-4, -1, 1)}) // -(x) + y <= -4  <=>  x - y >= 4
	if got := mustCount(t, p2, 0); got.Cmp(bi(6)) != 0 {
		t.Fatalf("got %s want 6", got)
	}
}

func TestEmptyDimension(t *testing.T) {
	// lo > hi in one dimension: empty box, zero points.
	p := mkProblem([]int64{0, 5}, []int64{10, 2}, nil)
	if got := mustCount(t, p, 0); got.Sign() != 0 {
		t.Fatalf("got %s want 0", got)
	}
	s, _ := NewSolver(p, 0)
	if _, _, err := s.Kth(bi(1)); err != ErrOutOfRange {
		t.Fatalf("kth on empty box: got %v want ErrOutOfRange", err)
	}
}

func TestZeroDimensions(t *testing.T) {
	// 0-dim box: exactly one (empty) point when constraints hold, else zero.
	p := mkProblem(nil, nil, nil)
	if got := mustCount(t, p, 0); got.Cmp(bi(1)) != 0 {
		t.Fatalf("got %s want 1", got)
	}
	s, _ := NewSolver(p, 0)
	pt, proof, err := s.Kth(bi(1))
	if err != nil || len(pt) != 0 || len(proof) != 0 {
		t.Fatalf("kth(1) on 0-dim: pt=%v proof=%v err=%v", pt, proof, err)
	}
	// Unsatisfiable 0-dim constraint: 1 <= 0 i.e. empty conjunction.
	p2 := mkProblem(nil, nil, []Constraint{{A: nil, B: bi(-1)}})
	if got := mustCount(t, p2, 0); got.Sign() != 0 {
		t.Fatalf("got %s want 0", got)
	}
}

func TestRedundantConstraints(t *testing.T) {
	// x + y <= 100 is implied by the box [0,10]^2; count must equal the box.
	base := mkProblem([]int64{0, 0}, []int64{10, 10}, nil)
	red := mkProblem([]int64{0, 0}, []int64{10, 10},
		[]Constraint{con(100, 1, 1), con(20, 1, 0), con(20, 0, 1)})
	a, b := mustCount(t, base, 0), mustCount(t, red, 0)
	if a.Cmp(b) != 0 || a.Cmp(bi(121)) != 0 {
		t.Fatalf("base=%s redundant=%s want 121", a, b)
	}
}

func TestKthOutOfRange(t *testing.T) {
	p := mkProblem([]int64{0, 0}, []int64{1, 1}, nil) // 4 points
	s, _ := NewSolver(p, 0)
	for _, k := range []int64{0, -1, 5, 100} {
		if _, _, err := s.Kth(bi(k)); err != ErrOutOfRange {
			t.Fatalf("kth(%d): got %v want ErrOutOfRange", k, err)
		}
	}
}

func TestKthOrder(t *testing.T) {
	// [0,1]x[0,2], lex order: (0,0),(0,1),(0,2),(1,0),(1,1),(1,2)
	p := mkProblem([]int64{0, 0}, []int64{1, 2}, nil)
	s, _ := NewSolver(p, 0)
	want := [][]int64{{0, 0}, {0, 1}, {0, 2}, {1, 0}, {1, 1}, {1, 2}}
	for k := int64(1); k <= 6; k++ {
		s.Reset()
		pt, proof, err := s.Kth(bi(k))
		if err != nil {
			t.Fatalf("kth(%d): %v", k, err)
		}
		for i := 0; i < 2; i++ {
			if pt[i].Cmp(bi(want[k-1][i])) != 0 {
				t.Fatalf("kth(%d) = %v want %v", k, pt, want[k-1])
			}
		}
		// Ranking proof: sum of skipped counts + 1 must equal k.
		sum := new(big.Int)
		for _, st := range proof {
			sum.Add(sum, st.Skipped)
		}
		sum.Add(sum, bigOne)
		if sum.Cmp(bi(k)) != 0 {
			t.Fatalf("kth(%d): proof sums to %s", k, sum)
		}
	}
}

func TestBudgetExhausted(t *testing.T) {
	// Tight budget on a problem that must branch: expect "unknown".
	p := mkProblem([]int64{0, 0}, []int64{100, 100},
		[]Constraint{con(100, 1, 1)})
	s, _ := NewSolver(p, 3)
	if _, err := s.Count(); err != ErrBudgetExhausted {
		t.Fatalf("count: got %v want ErrBudgetExhausted", err)
	}
	s.Reset()
	if _, _, err := s.Kth(bi(5)); err != ErrBudgetExhausted {
		t.Fatalf("kth: got %v want ErrBudgetExhausted", err)
	}
}

func TestNoPointDroppedWhenBoundsLoose(t *testing.T) {
	// Constraint whose min-tail bound never prunes anything (redundant but
	// not detectable by the tighten step alone in deeper dims): the count
	// must still be exact.
	p := mkProblem([]int64{-5, -5}, []int64{5, 5},
		[]Constraint{con(1000, 3, -7), con(4, 1, 1)})
	pts, ok := bruteForce(p, 100000)
	if !ok {
		t.Fatal("box too large for test")
	}
	if got := mustCount(t, p, 0); got.Cmp(bi(int64(len(pts)))) != 0 {
		t.Fatalf("got %s want %d", got, len(pts))
	}
}

func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		dims := 1 + rng.Intn(3)
		lo := make([]int64, dims)
		hi := make([]int64, dims)
		for i := range lo {
			lo[i] = int64(rng.Intn(7) - 3)
			hi[i] = lo[i] + int64(rng.Intn(6))
		}
		var cons []Constraint
		for c := 0; c < rng.Intn(4); c++ {
			coeffs := make([]int64, dims)
			for i := range coeffs {
				coeffs[i] = int64(rng.Intn(7) - 3)
			}
			cons = append(cons, con(int64(rng.Intn(21)-10), coeffs...))
		}
		p := mkProblem(lo, hi, cons)
		pts, ok := bruteForce(p, 200000)
		if !ok {
			t.Fatal("test box too large")
		}
		s, err := NewSolver(p, 0)
		if err != nil {
			t.Fatalf("NewSolver: %v", err)
		}
		n, err := s.Count()
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if n.Cmp(bi(int64(len(pts)))) != 0 {
			t.Fatalf("trial %d: count=%s brute=%d dims=%v lo=%v hi=%v cons=%v",
				trial, n, len(pts), dims, lo, hi, cons)
		}
		// Check every rank on small counts, sampled ranks otherwise.
		total := int64(len(pts))
		step := int64(1)
		if total > 50 {
			step = total/50 + 1
		}
		for k := int64(1); k <= total; k += step {
			s.Reset()
			pt, proof, err := s.Kth(bi(k))
			if err != nil {
				t.Fatalf("trial %d kth(%d): %v", trial, k, err)
			}
			for i := 0; i < dims; i++ {
				if pt[i].Cmp(pts[k-1][i]) != 0 {
					t.Fatalf("trial %d kth(%d)=%v brute=%v", trial, k, pt, pts[k-1])
				}
			}
			sum := new(big.Int)
			for _, st := range proof {
				sum.Add(sum, st.Skipped)
			}
			sum.Add(sum, bigOne)
			if sum.Cmp(bi(k)) != 0 {
				t.Fatalf("trial %d kth(%d): proof sums to %s", trial, k, sum)
			}
		}
		// K just past the end must be out of range.
		s.Reset()
		if _, _, err := s.Kth(bi(total + 1)); err != ErrOutOfRange {
			t.Fatalf("trial %d kth(%d): got %v want ErrOutOfRange", trial, total+1, err)
		}
	}
}

func TestEqualityConstraint(t *testing.T) {
	// x + y == 4 over [0,6]^2: (0,4)..(4,0) => 5 points.
	p := mkProblem([]int64{0, 0}, []int64{6, 6},
		[]Constraint{con(4, 1, 1), con(-4, -1, -1)})
	if got := mustCount(t, p, 0); got.Cmp(bi(5)) != 0 {
		t.Fatalf("got %s want 5", got)
	}
	s, _ := NewSolver(p, 0)
	pt, _, err := s.Kth(bi(3))
	if err != nil {
		t.Fatalf("kth: %v", err)
	}
	if pt[0].Cmp(bi(2)) != 0 || pt[1].Cmp(bi(2)) != 0 {
		t.Fatalf("kth(3) = %v want [2 2]", pt)
	}
}
