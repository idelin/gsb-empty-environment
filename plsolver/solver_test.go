package plsolver

import (
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

func rat(p, q int64) *big.Rat { return big.NewRat(p, q) }

// ---------- 暴力枚举参考实现（独立核对用） ----------

func bruteEval(terms []Term, lin, x *big.Rat) *big.Rat {
	v := new(big.Rat).Mul(lin, x)
	for _, t := range terms {
		d := new(big.Rat).Sub(x, t.A)
		v.Add(v, new(big.Rat).Mul(t.W, d.Abs(d)))
	}
	return v
}

// bruteForce 用全部断点枚举求最小值与全部最优区间，与 Solver.Solve 相互独立。
func bruteForce(terms []Term, lin, L, R *big.Rat) (*big.Rat, []Interval) {
	if L.Cmp(R) == 0 {
		return bruteEval(terms, lin, L), []Interval{{L, R}}
	}
	bpSet := map[string]*big.Rat{L.RatString(): L, R.RatString(): R}
	for _, t := range terms {
		if t.A.Cmp(L) > 0 && t.A.Cmp(R) < 0 {
			bpSet[t.A.RatString()] = t.A
		}
	}
	bps := make([]*big.Rat, 0, len(bpSet))
	for _, r := range bpSet {
		bps = append(bps, r)
	}
	sort.Slice(bps, func(i, j int) bool { return bps[i].Cmp(bps[j]) < 0 })

	var min *big.Rat
	for _, bp := range bps {
		v := bruteEval(terms, lin, bp)
		if min == nil || v.Cmp(min) < 0 {
			min = v
		}
	}

	var opts []Interval
	for k := 0; k+1 < len(bps); k++ {
		lo, hi := bps[k], bps[k+1]
		vLo := bruteEval(terms, lin, lo)
		vHi := bruteEval(terms, lin, hi)
		mid := new(big.Rat).Add(lo, hi)
		mid.Mul(mid, big.NewRat(1, 2))
		vMid := bruteEval(terms, lin, mid)
		switch {
		case vLo.Cmp(min) == 0 && vHi.Cmp(min) == 0 && vMid.Cmp(min) == 0:
			opts = append(opts, Interval{lo, hi}) // 平坦段
		case vLo.Cmp(min) == 0:
			opts = append(opts, Interval{lo, lo})
		case vHi.Cmp(min) == 0:
			opts = append(opts, Interval{hi, hi})
		}
	}
	merged := opts[:0]
	for _, iv := range opts {
		if n := len(merged); n > 0 && merged[n-1].Hi.Cmp(iv.Lo) >= 0 {
			if iv.Hi.Cmp(merged[n-1].Hi) > 0 {
				merged[n-1].Hi = iv.Hi
			}
			continue
		}
		merged = append(merged, iv)
	}
	return min, merged
}

func checkIntervals(t *testing.T, got, want []Interval) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("interval count: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i].Lo.Cmp(want[i].Lo) != 0 || got[i].Hi.Cmp(want[i].Hi) != 0 {
			t.Fatalf("interval %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

// ---------- 针对性用例 ----------

// 平坦段：f = |x| + |x-1| 在 [0,1] 上斜率为 0，整段最优。
func TestFlatSegment(t *testing.T) {
	s := NewSolver(rat(-1, 1), rat(2, 1))
	s.AddTerm(rat(1, 1), rat(0, 1))
	s.AddTerm(rat(1, 1), rat(1, 1))
	sol := s.Solve()
	if sol.Min.Cmp(rat(1, 1)) != 0 {
		t.Fatalf("min = %v, want 1", sol.Min)
	}
	checkIntervals(t, sol.Intervals, []Interval{{rat(0, 1), rat(1, 1)}})
	// 斜率证据：[-1,0]:-2, [0,1]:0, [1,2]:2
	wantSlopes := []int64{-2, 0, 2}
	if len(sol.Segments) != 3 {
		t.Fatalf("segments = %d, want 3", len(sol.Segments))
	}
	for i, sg := range sol.Segments {
		if sg.Slope.Cmp(rat(wantSlopes[i], 1)) != 0 {
			t.Fatalf("segment %d slope = %v, want %d", i, sg.Slope, wantSlopes[i])
		}
	}
}

// 端点最优：f = x 在 [0,1] 最小值在 x=0；f = -x 最小值在 x=1。
func TestEndpointOptimum(t *testing.T) {
	s := NewSolver(rat(0, 1), rat(1, 1))
	s.AddLinear(rat(1, 1))
	sol := s.Solve()
	if sol.Min.Sign() != 0 {
		t.Fatalf("min = %v, want 0", sol.Min)
	}
	checkIntervals(t, sol.Intervals, []Interval{{rat(0, 1), rat(0, 1)}})

	s2 := NewSolver(rat(0, 1), rat(1, 1))
	s2.AddLinear(rat(-1, 1))
	sol2 := s2.Solve()
	if sol2.Min.Cmp(rat(-1, 1)) != 0 {
		t.Fatalf("min = %v, want -1", sol2.Min)
	}
	checkIntervals(t, sol2.Intervals, []Interval{{rat(1, 1), rat(1, 1)}})
}

// 负权重：f = -|x| 在 [-1,1] 上非凸，两个端点同为最优（两个孤立点区间）。
func TestNegativeWeightTwoEndpoints(t *testing.T) {
	s := NewSolver(rat(-1, 1), rat(1, 1))
	s.AddTerm(rat(-1, 1), rat(0, 1))
	sol := s.Solve()
	if sol.Min.Cmp(rat(-1, 1)) != 0 {
		t.Fatalf("min = %v, want -1", sol.Min)
	}
	checkIntervals(t, sol.Intervals, []Interval{
		{rat(-1, 1), rat(-1, 1)},
		{rat(1, 1), rat(1, 1)},
	})
}

// 重复断点合并：两个断点都在 0，权重 1 和 2，合并后斜率跳变 2*(1+2)=6。
func TestDuplicateBreakpoints(t *testing.T) {
	s := NewSolver(rat(-2, 1), rat(2, 1))
	s.AddTerm(rat(1, 1), rat(0, 1))
	s.AddTerm(rat(2, 1), rat(0, 1))
	sol := s.Solve()
	if len(sol.Segments) != 2 {
		t.Fatalf("segments = %d, want 2 (duplicates merged)", len(sol.Segments))
	}
	if sol.Segments[0].Slope.Cmp(rat(-3, 1)) != 0 || sol.Segments[1].Slope.Cmp(rat(3, 1)) != 0 {
		t.Fatalf("slopes = %v, %v, want -3, 3", sol.Segments[0].Slope, sol.Segments[1].Slope)
	}
	if sol.Min.Sign() != 0 {
		t.Fatalf("min = %v, want 0", sol.Min)
	}
	checkIntervals(t, sol.Intervals, []Interval{{rat(0, 1), rat(0, 1)}})
}

// 增删项：删除一项后结果应与重新构建的求解器一致。
func TestAddRemoveTerms(t *testing.T) {
	s := NewSolver(rat(-3, 1), rat(3, 1))
	s.AddLinear(rat(1, 2))
	id1 := s.AddTerm(rat(2, 1), rat(-1, 1))
	s.AddTerm(rat(-3, 1), rat(1, 1))
	s.AddTerm(rat(1, 1), rat(2, 1))
	before := s.Solve()

	if !s.RemoveTerm(id1) {
		t.Fatal("RemoveTerm returned false")
	}
	if s.RemoveTerm(id1) {
		t.Fatal("RemoveTerm twice returned true")
	}

	s2 := NewSolver(rat(-3, 1), rat(3, 1))
	s2.AddLinear(rat(1, 2))
	s2.AddTerm(rat(-3, 1), rat(1, 1))
	s2.AddTerm(rat(1, 1), rat(2, 1))
	after := s.Solve()
	want := s2.Solve()
	if after.Min.Cmp(want.Min) != 0 {
		t.Fatalf("after remove: min = %v, want %v", after.Min, want.Min)
	}
	checkIntervals(t, after.Intervals, want.Intervals)
	if before.Min.Cmp(after.Min) == 0 && len(before.Intervals) == len(after.Intervals) {
		t.Log("note: removal did not change the solution (acceptable but weak test)")
	}
}

// ---------- 随机用例：与暴力枚举参考核对 ----------

func TestRandomVsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 500; iter++ {
		n := 1 + rng.Intn(8)
		L := rat(int64(rng.Intn(5)-6), int64(1+rng.Intn(3)))
		R := rat(int64(rng.Intn(5)+1), int64(1+rng.Intn(3)))
		if L.Cmp(R) > 0 {
			L, R = R, L
		}
		lin := rat(int64(rng.Intn(11)-5), int64(1+rng.Intn(4)))

		s := NewSolver(L, R)
		s.AddLinear(lin)
		var terms []Term
		for i := 0; i < n; i++ {
			w := rat(int64(rng.Intn(9)-4), int64(1+rng.Intn(3))) // 允许负权重
			a := rat(int64(rng.Intn(9)-4), int64(1+rng.Intn(3))) // 小值域，易撞重复断点
			s.AddTerm(w, a)
			terms = append(terms, Term{w, a})
		}

		sol := s.Solve()
		refMin, refOpts := bruteForce(terms, lin, L, R)

		if sol.Min.Cmp(refMin) != 0 {
			t.Fatalf("iter %d: min = %v, brute force = %v\n%s", iter, sol.Min, refMin, sol.Report())
		}
		if len(sol.Intervals) != len(refOpts) {
			t.Fatalf("iter %d: intervals = %v, brute force = %v\n%s", iter, sol.Intervals, refOpts, sol.Report())
		}
		for i := range sol.Intervals {
			if sol.Intervals[i].Lo.Cmp(refOpts[i].Lo) != 0 || sol.Intervals[i].Hi.Cmp(refOpts[i].Hi) != 0 {
				t.Fatalf("iter %d: intervals = %v, brute force = %v\n%s", iter, sol.Intervals, refOpts, sol.Report())
			}
		}

		// 核对斜率证据：每段斜率须等于割线斜率，且等于按定义独立计算的斜率。
		for _, sg := range sol.Segments {
			dx := new(big.Rat).Sub(sg.Hi, sg.Lo)
			secant := new(big.Rat).Quo(new(big.Rat).Sub(sg.FHi, sg.FLo), dx)
			if sg.Slope.Cmp(secant) != 0 {
				t.Fatalf("iter %d: segment %v slope != secant %v", iter, sg, secant)
			}
			indep := new(big.Rat).Set(lin)
			for _, tm := range terms {
				if tm.A.Cmp(sg.Lo) <= 0 {
					indep.Add(indep, tm.W)
				} else if tm.A.Cmp(sg.Hi) >= 0 {
					indep.Sub(indep, tm.W)
				}
			}
			if sg.Slope.Cmp(indep) != 0 {
				t.Fatalf("iter %d: segment %v slope != independent %v", iter, sg, indep)
			}
			// 端点函数值与直接求值一致。
			if sg.FLo.Cmp(bruteEval(terms, lin, sg.Lo)) != 0 || sg.FHi.Cmp(bruteEval(terms, lin, sg.Hi)) != 0 {
				t.Fatalf("iter %d: segment %v endpoint values mismatch", iter, sg)
			}
		}
	}
}
