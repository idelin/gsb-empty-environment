package taskpkg

import (
	"math/rand"
	"testing"
)

// TestExhaustiveStress 用更大样本量与更宽尺寸范围复核 DP 与全枚举一致。
func TestExhaustiveStress(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	for trial := 0; trial < 500; trial++ {
		a, b, m := makeRandomCaseWide(rng)
		all := enumerateSeams(m)
		got, err := Find(a, b, m, nil, nil)
		if len(all) == 0 {
			if err != ErrDisconnected {
				t.Fatalf("trial %d: 期望 ErrDisconnected，得到 %v", trial, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: 意外报错 %v", trial, err)
		}
		want, wantCost := bruteForceBest(a, b, m, nil, 0, false)
		if !equalInts(got, want) {
			t.Fatalf("trial %d: 缝不一致 got=%v want=%v", trial, got, want)
		}
		if gotCost := refSeamCost(a, b, m, got, 0, false, nil); gotCost != wantCost {
			t.Fatalf("trial %d: 代价不一致 got=%v want=%v", trial, gotCost, wantCost)
		}
	}
}

func makeRandomCaseWide(rng *rand.Rand) (*Frame, *Frame, *Mask) {
	// 包含 1x1、单行、单列等退化形状。
	width := 1 + rng.Intn(6)
	height := 1 + rng.Intn(6)
	a := NewFrame(width, height)
	b := NewFrame(width, height)
	m := NewMask(width, height)
	for i := range a.Pix {
		a.Pix[i] = float64(rng.Intn(7)) - 2
		b.Pix[i] = float64(rng.Intn(7)) - 2
	}
	for i := range m.Pix {
		m.Pix[i] = rng.Intn(10) < 7
	}
	return a, b, m
}
