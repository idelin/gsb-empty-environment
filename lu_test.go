package matdiag

import (
	"math"
	"testing"
)

// 行交换：首列首元为 0，必须换行才能继续。
func TestFactorRequiresRowSwap(t *testing.T) {
	a := [][]float64{{0, 1, 2}, {1, 0, 1}, {2, 1, 0}}
	b := []float64{8, 4, 4} // 精确解 x = [1, 2, 3]
	f := Factor(a)
	if f.Singular {
		t.Fatal("不应判定为奇异")
	}
	if f.Perm[0] == 0 {
		t.Fatalf("第一步应发生行交换，Perm = %v", f.Perm)
	}
	x := f.Solve(b)
	want := []float64{1, 2, 3}
	for i := range want {
		if math.Abs(x[i]-want[i]) > 1e-14 {
			t.Fatalf("x[%d] = %v, 期望 %v", i, x[i], want[i])
		}
	}
}

// 真奇异：第二行是第一行的 2 倍，消元后必出现精确零主元。
func TestSingularExactZeroPivot(t *testing.T) {
	a := [][]float64{{1, 2}, {2, 4}}
	f := Factor(a)
	if !f.Singular {
		t.Fatal("应判定为真奇异")
	}
	if f.SingularAt != 1 {
		t.Fatalf("SingularAt = %d, 期望 1", f.SingularAt)
	}
}

// 尺度无关性：1e-12·I 的主元绝对值极小（1e-12），任何固定绝对阈值
// （如 1e-8）都会误判为奇异；但相对判据下它完全健康（cond = 1）。
func TestScaleRelativeNotAbsolute(t *testing.T) {
	a := [][]float64{{1e-12, 0, 0}, {0, 1e-12, 0}, {0, 0, 1e-12}}
	f := Factor(a)
	if f.Singular {
		t.Fatal("1e-12·I 不应判定为奇异：判据必须是尺度无关的")
	}
	if r := f.MinPivotRatio(); math.Abs(r-1) > 1e-12 {
		t.Fatalf("MinPivotRatio = %v, 期望 1", r)
	}
	rep := Diagnose(a, []float64{1e-12, 2e-12, 3e-12})
	if rep.Singular || rep.NearSingular || rep.PoorlyScaled {
		t.Fatalf("1e-12·I 不应有任何告警: %+v", rep)
	}
	if rep.Cond > 1+1e-9 {
		t.Fatalf("cond 应为 1，得到 %v", rep.Cond)
	}
}

// 近奇异但非奇异：cond ≈ 4e9，应与真奇异分开报告。
func TestNearSingularDistinctFromSingular(t *testing.T) {
	a := [][]float64{{1, 1}, {1, 1 + 1e-9}}
	rep := Diagnose(a, []float64{2, 2 + 1e-9})
	if rep.Singular {
		t.Fatal("近奇异不应报告为真奇异")
	}
	if !rep.NearSingular {
		t.Fatalf("cond=%.3e 应报告为近奇异", rep.Cond)
	}
	if rep.PoorlyScaled {
		t.Fatal("该矩阵均衡后仍病态，不应归为尺度问题")
	}
}
