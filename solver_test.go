package main

import (
	"fmt"
	"math/rand"
	"testing"
)

func i64(v int64) *int64 { return &v }

func priceOf(out Output, id string) int64 {
	for _, p := range out.Prices {
		if p.ID == id {
			return p.Price
		}
	}
	panic("missing price for " + id)
}

// validateSolution 校验输出价格满足全部约束，且统计字段自洽。
func validateSolution(t *testing.T, in *Input, out Output) {
	t.Helper()
	if !out.Feasible {
		t.Fatal("期望可行，但求解器返回不可行")
	}
	idx := make(map[string]int)
	p := make(map[string]int64)
	for i, it := range in.Items {
		idx[it.ID] = i
	}
	for _, pe := range out.Prices {
		p[pe.ID] = pe.Price
		it := in.Items[idx[pe.ID]]
		if pe.Original != it.Price || pe.Locked != it.Locked {
			t.Errorf("商品 %s 的原始信息不一致", pe.ID)
		}
		if it.Locked && pe.Price != it.Price {
			t.Errorf("锁定商品 %s 的价格被修改", pe.ID)
		}
		if pe.Changed != (pe.Price != it.Price) {
			t.Errorf("商品 %s 的 changed 标记不一致", pe.ID)
		}
	}
	for bi, b := range in.Bounds {
		d := p[b.To] - p[b.From]
		if b.Min != nil && d < *b.Min {
			t.Errorf("第 %d 条界限被违反: price[%s]-price[%s]=%d < %d", bi, b.To, b.From, d, *b.Min)
		}
		if b.Max != nil && d > *b.Max {
			t.Errorf("第 %d 条界限被违反: price[%s]-price[%s]=%d > %d", bi, b.To, b.From, d, *b.Max)
		}
	}
	cnt := 0
	var total int64
	for _, pe := range out.Prices {
		if pe.Changed {
			cnt++
			total += abs64(pe.Price - pe.Original)
		}
	}
	if cnt != out.ModifiedCount {
		t.Errorf("modifiedCount=%d 与实际 %d 不符", out.ModifiedCount, cnt)
	}
	if total != out.TotalAbsChange {
		t.Errorf("totalAbsChange=%d 与实际 %d 不符", out.TotalAbsChange, total)
	}
}

func TestNoChangeNeeded(t *testing.T) {
	in := Input{
		Items: []Item{
			{ID: "A", Price: 5},
			{ID: "B", Price: 8, Locked: true},
		},
		Bounds: []Bound{
			{From: "A", To: "B", Min: i64(2), Max: i64(5)},
		},
	}
	out := Solve(&in)
	validateSolution(t, &in, out)
	if out.ModifiedCount != 0 || out.TotalAbsChange != 0 {
		t.Fatalf("期望零修改，得到 count=%d total=%d", out.ModifiedCount, out.TotalAbsChange)
	}
}

func TestSimpleForcedChange(t *testing.T) {
	in := Input{
		Items: []Item{
			{ID: "A", Price: 0, Locked: true},
			{ID: "B", Price: 0},
		},
		Bounds: []Bound{
			{From: "A", To: "B", Min: i64(3)},
		},
	}
	out := Solve(&in)
	validateSolution(t, &in, out)
	if out.ModifiedCount != 1 || out.TotalAbsChange != 3 || priceOf(out, "B") != 3 {
		t.Fatalf("期望 B 改为 3，得到 %+v", out)
	}
}

// TestLexicographicOrder 验证"先最少修改数、后最小总改价"的词典序：
// 全局最小总改价方案是 X=3,Y=-3,Z=3（改 3 件，总改价 9），
// 但正确答案是改 2 件的 X=6,Z=6,Y=0（总改价 12）。
func TestLexicographicOrder(t *testing.T) {
	in := Input{
		Items: []Item{
			{ID: "L", Price: 0, Locked: true},
			{ID: "X", Price: 0},
			{ID: "Y", Price: 0},
			{ID: "Z", Price: 0},
		},
		Bounds: []Bound{
			{From: "Y", To: "X", Min: i64(6)},  // X - Y >= 6
			{From: "Y", To: "Z", Min: i64(6)},  // Z - Y >= 6
			{From: "L", To: "Y", Min: i64(-3)}, // Y >= -3
		},
	}
	out := Solve(&in)
	validateSolution(t, &in, out)
	if out.ModifiedCount != 2 {
		t.Fatalf("修改数应为 2（不能为了总改价 9 而改 3 件），得到 %d", out.ModifiedCount)
	}
	if out.TotalAbsChange != 12 {
		t.Fatalf("两件修改下的最小总改价应为 12，得到 %d", out.TotalAbsChange)
	}
	if priceOf(out, "X") != 6 || priceOf(out, "Z") != 6 || priceOf(out, "Y") != 0 {
		t.Fatalf("价格应为 X=6,Y=0,Z=6，得到 %+v", out.Prices)
	}
}

// TestSecondaryObjective 存在多个最大团（{A}、{B}、{C} 各自为团），
// 保留 A 不动时总改价最小（B=-4、C=1，共 5）。
func TestSecondaryObjective(t *testing.T) {
	in := Input{
		Items: []Item{
			{ID: "A", Price: 0},
			{ID: "B", Price: 0},
			{ID: "C", Price: 0},
		},
		Bounds: []Bound{
			{From: "B", To: "A", Min: i64(4)}, // A - B >= 4
			{From: "A", To: "C", Min: i64(1)}, // C - A >= 1
			{From: "B", To: "C", Min: i64(3)}, // C - B >= 3
		},
	}
	out := Solve(&in)
	validateSolution(t, &in, out)
	if out.ModifiedCount != 2 || out.TotalAbsChange != 5 {
		t.Fatalf("期望改 2 件共 5，得到 count=%d total=%d", out.ModifiedCount, out.TotalAbsChange)
	}
	if priceOf(out, "A") != 0 || priceOf(out, "B") != -4 || priceOf(out, "C") != 1 {
		t.Fatalf("价格应为 A=0,B=-4,C=1，得到 %+v", out.Prices)
	}
}

func TestInfeasibleLockedConflict(t *testing.T) {
	in := Input{
		Items: []Item{
			{ID: "A", Price: 0, Locked: true},
			{ID: "B", Price: 3, Locked: true},
		},
		Bounds: []Bound{
			{From: "A", To: "B", Max: i64(2)}, // B - A <= 2，与锁定值 3-0 矛盾
		},
	}
	out := Solve(&in)
	if out.Feasible {
		t.Fatal("期望不可行")
	}
	if out.Evidence == nil || out.Evidence.Type != "negative-cycle" {
		t.Fatalf("缺少负环证据: %+v", out)
	}
	if out.Evidence.CycleSum != -1 {
		t.Fatalf("负环权值和应为 -1，得到 %d", out.Evidence.CycleSum)
	}
	if len(out.Evidence.Constraints) != 3 {
		t.Fatalf("负环应含 3 条约束，得到 %v", out.Evidence.Constraints)
	}
}

func TestInfeasiblePairOnly(t *testing.T) {
	in := Input{
		Items: []Item{
			{ID: "A", Price: 0},
			{ID: "B", Price: 0},
		},
		Bounds: []Bound{
			{From: "B", To: "A", Min: i64(5)}, // A - B >= 5
			{From: "A", To: "B", Min: i64(3)}, // B - A >= 3
		},
	}
	out := Solve(&in)
	if out.Feasible {
		t.Fatal("期望不可行")
	}
	if out.Evidence == nil || out.Evidence.CycleSum != -8 {
		t.Fatalf("负环权值和应为 -8，得到 %+v", out.Evidence)
	}
}

func TestAllLockedFeasible(t *testing.T) {
	in := Input{
		Items: []Item{
			{ID: "A", Price: 1, Locked: true},
			{ID: "B", Price: 4, Locked: true},
		},
		Bounds: []Bound{
			{From: "A", To: "B", Min: i64(2), Max: i64(5)},
		},
	}
	out := Solve(&in)
	validateSolution(t, &in, out)
	if out.ModifiedCount != 0 {
		t.Fatalf("全锁定可行时不应有修改，得到 %d", out.ModifiedCount)
	}
}

// TestChain18 十八件商品的链式约束：i0 锁定为 0，i_{k+1}-i_k ∈ [1,2]，
// 原价全 0，则 i1..i17 全部被迫修改，最优为 p_k=k，总改价 153。
func TestChain18(t *testing.T) {
	in := Input{}
	in.Items = append(in.Items, Item{ID: "i0", Price: 0, Locked: true})
	for k := 1; k < 18; k++ {
		in.Items = append(in.Items, Item{ID: fmt.Sprintf("i%d", k), Price: 0})
		in.Bounds = append(in.Bounds, Bound{
			From: fmt.Sprintf("i%d", k-1), To: fmt.Sprintf("i%d", k),
			Min: i64(1), Max: i64(2),
		})
	}
	out := Solve(&in)
	validateSolution(t, &in, out)
	if out.ModifiedCount != 17 || out.TotalAbsChange != 153 {
		t.Fatalf("期望改 17 件共 153，得到 count=%d total=%d", out.ModifiedCount, out.TotalAbsChange)
	}
	for k := 0; k < 18; k++ {
		if got := priceOf(out, fmt.Sprintf("i%d", k)); got != int64(k) {
			t.Fatalf("i%d 应为 %d，得到 %d", k, k, got)
		}
	}
}

// bruteForce 暴力枚举 [-limit,limit] 内的全部整数价格向量，
// 返回词典序（修改数，总改价）最优，用于小规模对拍。
func bruteForce(in *Input, limit int64) (feasible bool, count int, total int64) {
	n := len(in.Items)
	idx := make(map[string]int, n)
	for i, it := range in.Items {
		idx[it.ID] = i
	}
	p := make([]int64, n)
	bestCount := n + 1
	var bestTotal int64
	var rec func(i, c int, t int64)
	rec = func(i, c int, t int64) {
		if c > bestCount || (c == bestCount && t >= bestTotal && bestCount <= n) {
			return
		}
		if i == n {
			for _, b := range in.Bounds {
				d := p[idx[b.To]] - p[idx[b.From]]
				if b.Min != nil && d < *b.Min {
					return
				}
				if b.Max != nil && d > *b.Max {
					return
				}
			}
			bestCount, bestTotal = c, t
			return
		}
		if in.Items[i].Locked {
			p[i] = in.Items[i].Price
			rec(i+1, c, t)
			return
		}
		for v := -limit; v <= limit; v++ {
			p[i] = v
			nc, nt := c, t
			if v != in.Items[i].Price {
				nc++
				nt += abs64(v - in.Items[i].Price)
			}
			rec(i+1, nc, nt)
		}
	}
	rec(0, 0, 0)
	if bestCount > n {
		return false, 0, 0
	}
	return true, bestCount, bestTotal
}

func randomInput(r *rand.Rand, n int, maxPrice, maxBound int64) Input {
	in := Input{}
	for i := 0; i < n; i++ {
		in.Items = append(in.Items, Item{
			ID:     fmt.Sprintf("p%d", i),
			Price:  r.Int63n(2*maxPrice+1) - maxPrice,
			Locked: r.Float64() < 0.3,
		})
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j || r.Float64() < 0.5 {
				continue
			}
			lo := r.Int63n(2*maxBound+1) - maxBound
			hi := r.Int63n(2*maxBound+1) - maxBound
			if lo > hi {
				lo, hi = hi, lo
			}
			b := Bound{From: fmt.Sprintf("p%d", i), To: fmt.Sprintf("p%d", j)}
			switch r.Intn(4) {
			case 0:
				b.Min = i64(lo)
			case 1:
				b.Max = i64(hi)
			default:
				b.Min = i64(lo)
				b.Max = i64(hi)
			}
			in.Bounds = append(in.Bounds, b)
		}
	}
	return in
}

// TestBruteForceRandom 小规模随机实例与暴力枚举对拍。
func TestBruteForceRandom(t *testing.T) {
	cases := []struct {
		n                  int
		maxPrice, maxBound int64
		limit              int64
		iters              int
		seed               int64
	}{
		{3, 4, 3, 13, 400, 1},
		{4, 3, 2, 11, 150, 2},
		{5, 2, 1, 6, 60, 3},
	}
	for _, tc := range cases {
		r := rand.New(rand.NewSource(tc.seed))
		for it := 0; it < tc.iters; it++ {
			in := randomInput(r, tc.n, tc.maxPrice, tc.maxBound)
			out := Solve(&in)
			bfFeasible, bfCount, bfTotal := bruteForce(&in, tc.limit)
			if out.Feasible != bfFeasible {
				t.Fatalf("n=%d iter=%d: 可行性不一致 solver=%v brute=%v\n输入: %+v",
					tc.n, it, out.Feasible, bfFeasible, in)
			}
			if !out.Feasible {
				if out.Evidence == nil || out.Evidence.CycleSum >= 0 {
					t.Fatalf("n=%d iter=%d: 不可行但证据非法: %+v", tc.n, it, out.Evidence)
				}
				continue
			}
			validateSolution(t, &in, out)
			if out.ModifiedCount != bfCount || out.TotalAbsChange != bfTotal {
				t.Fatalf("n=%d iter=%d: solver=(%d,%d) brute=(%d,%d)\n输入: %+v",
					tc.n, it, out.ModifiedCount, out.TotalAbsChange, bfCount, bfTotal, in)
			}
		}
	}
}
