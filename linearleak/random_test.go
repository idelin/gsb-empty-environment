package linearleak

import (
	"math/rand"
	"testing"
)

// TestRandomAgainstBruteForce 用随机方程对照穷举真值，覆盖不同维度、
// 系数符号与公开位组合。
func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))

	for iter := 0; iter < 400; iter++ {
		n := 1 + rng.Intn(12)
		m := rng.Intn(n+3) + 1
		eqs := make([]Equation, m)
		coeff := make([][]int64, m)
		rhs := make([]int64, m)

		// 先随机一个“真值”赋值，让多数系统保持可行。
		truth := make([]int, n)
		for j := range truth {
			truth[j] = rng.Intn(2)
		}
		for i := 0; i < m; i++ {
			row := make([]int64, n)
			sum := int64(0)
			for j := range row {
				if rng.Intn(2) == 0 {
					continue
				}
				row[j] = int64(rng.Intn(5) - 2) // 系数 ∈ [-2,2]
				sum += row[j] * int64(truth[j])
			}
			if rng.Intn(8) == 0 {
				sum += int64(rng.Intn(3) + 1) // 偶尔制造矛盾
			}
			eqs[i] = Equation{Coeff: row, RHS: sum}
			coeff[i] = row
			rhs[i] = sum
		}

		var public []BitValue
		seed := map[int]int{}
		for j := 0; j < n; j++ {
			if rng.Intn(4) == 0 {
				public = append(public, BitValue{Index: j, Value: truth[j]})
				seed[j] = truth[j]
			}
		}

		rep, err := Analyze(n, eqs, public, Budget{})
		if err != nil {
			t.Fatalf("iter %d: unexpected error: %v", iter, err)
		}

		secretCount := n - len(public)
		zeroOK, oneOK, feasible := bruteForce(secretCount, reduceCoeff(coeff, public), reduceRHS(coeff, rhs, public, n), nil)

		if !feasible {
			if rep.Status != StatusInconsistent {
				t.Fatalf("iter %d: brute force infeasible but status = %s", iter, rep.Status)
			}
			continue
		}

		originalOf := secretIndexMap(public, n)
		gotForced := map[int]int{}
		for _, f := range rep.Findings {
			gotForced[f.Bit] = f.Value
			local := originalIndexToLocal(originalOf, f.Bit)
			switch f.Value {
			case 0:
				if oneOK[local] || !zeroOK[local] {
					t.Fatalf("iter %d: finding %+v contradicted by brute force", iter, f)
				}
			case 1:
				if zeroOK[local] || !oneOK[local] {
					t.Fatalf("iter %d: finding %+v contradicted by brute force", iter, f)
				}
			}
		}

		for local, original := range originalOf {
			forced := -1
			if zeroOK[local] && !oneOK[local] {
				forced = 0
			} else if oneOK[local] && !zeroOK[local] {
				forced = 1
			}
			if forced >= 0 {
				if got, ok := gotForced[original]; !ok || got != forced {
					t.Fatalf("iter %d: bit %d forced to %d by brute force, report = %+v", iter, original, forced, rep)
				}
			}
		}

		if rep.Status == StatusUnknown {
			t.Fatalf("iter %d: default budget must resolve n=%d systems", iter, n)
		}
	}
}

func reduceCoeff(coeff [][]int64, public []BitValue) [][]int64 {
	pub := map[int]bool{}
	for _, pv := range public {
		pub[pv.Index] = true
	}
	out := make([][]int64, len(coeff))
	for i, row := range coeff {
		for j, c := range row {
			if !pub[j] {
				out[i] = append(out[i], c)
			}
		}
	}
	return out
}

func reduceRHS(coeff [][]int64, rhs []int64, public []BitValue, n int) []int64 {
	pubVal := map[int]int{}
	for _, pv := range public {
		pubVal[pv.Index] = pv.Value
	}
	out := make([]int64, len(rhs))
	for i := range rhs {
		out[i] = rhs[i]
		for j := 0; j < n; j++ {
			if v, ok := pubVal[j]; ok {
				out[i] -= coeff[i][j] * int64(v)
			}
		}
	}
	return out
}

// secretIndexMap 返回 局部秘密下标 -> 原始下标。
func secretIndexMap(public []BitValue, n int) map[int]int {
	pub := map[int]bool{}
	for _, pv := range public {
		pub[pv.Index] = true
	}
	out := map[int]int{}
	local := 0
	for j := 0; j < n; j++ {
		if !pub[j] {
			out[local] = j
			local++
		}
	}
	return out
}

func originalIndexToLocal(originalOf map[int]int, original int) int {
	for local, j := range originalOf {
		if j == original {
			return local
		}
	}
	panic("linearleak test: bit not found in secret mapping")
}
