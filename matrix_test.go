package netsec

import (
	"errors"
	"testing"
)

func TestMatrixValidate(t *testing.T) {
	valid := Matrix{Row: [][]Rational{
		{{1, 4}, {3, 4}},
		{{1, 6}, {5, 6}},
	}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid matrix rejected: %v", err)
	}

	cases := []struct {
		name string
		m    Matrix
		want error
	}{
		{"empty", Matrix{}, ErrEmptyMatrix},
		{"ragged", Matrix{Row: [][]Rational{{{1, 2}}, {{1, 2}, {1, 2}}}}, ErrRaggedMatrix},
		{"bad denominator", Matrix{Row: [][]Rational{{{1, 0}, {1, 1}}, {{0, 1}, {1, 1}}}}, ErrBadDenominator},
		{"negative numerator", Matrix{Row: [][]Rational{{{-1, 2}, {3, 2}}, {{1, 2}, {1, 2}}}}, ErrNegativeEntry},
		{"row not normalized", Matrix{Row: [][]Rational{{{1, 3}, {1, 3}}, {{1, 2}, {1, 2}}}}, ErrRowNotNormalized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.m.Validate(), tc.want) {
				t.Fatalf("Validate() = %v, want %v", tc.m.Validate(), tc.want)
			}
		})
	}
}

func TestResolveRowWeights(t *testing.T) {
	// 分母 4、6、12 的最小公倍数为 12：1/4=3/12，1/6=2/12，7/12 保持不变。
	row := []Rational{{1, 4}, {1, 6}, {7, 12}}
	got, err := resolveRow(row)
	if err != nil {
		t.Fatalf("resolveRow: %v", err)
	}
	if got.total != 12 {
		t.Fatalf("total = %d, want 12", got.total)
	}
	wantWeights := []int64{3, 2, 7}
	for i, w := range wantWeights {
		if got.weights[i] != w {
			t.Fatalf("weight[%d] = %d, want %d", i, got.weights[i], w)
		}
	}
}

func TestDrawBoundariesAndZeroWeight(t *testing.T) {
	row := []Rational{{1, 2}, {0, 1}, {1, 2}}
	got, err := resolveRow(row)
	if err != nil {
		t.Fatalf("resolveRow: %v", err)
	}
	// 权重 [1,0,1]：x=0 落到符号 0，x=1 落到符号 2，符号 1 永不出现。
	if symbol := got.draw(&fixedSource{v: 0}); symbol != 0 {
		t.Fatalf("x=0 drew %d, want 0", symbol)
	}
	if symbol := got.draw(&fixedSource{v: 1}); symbol != 2 {
		t.Fatalf("x=1 drew %d, want 2", symbol)
	}
}

func TestLCMOverflow(t *testing.T) {
	if _, err := lcm([]int64{1 << 62, 3}); !errors.Is(err, ErrWeightsTooLarge) {
		t.Fatalf("lcm overflow not detected, got %v", err)
	}
}

func TestFingerprintStableAndSensitive(t *testing.T) {
	prr := Matrix{Row: [][]Rational{{{3, 4}, {1, 4}}, {{1, 4}, {3, 4}}}}
	irr := Matrix{Row: [][]Rational{{{2, 3}, {1, 3}}, {{1, 3}, {2, 3}}}}

	fp1, err := Fingerprint("v1", prr, irr)
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	fp2, _ := Fingerprint("v1", prr, irr)
	if fp1 != fp2 {
		t.Fatal("fingerprint not deterministic")
	}
	if fp3, _ := Fingerprint("v2", prr, irr); fp3 == fp1 {
		t.Fatal("fingerprint must change with param version")
	}
	changed := Matrix{Row: [][]Rational{{{1, 2}, {1, 2}}, {{1, 4}, {3, 4}}}}
	if fp4, _ := Fingerprint("v1", changed, irr); fp4 == fp1 {
		t.Fatal("fingerprint must change with permanent matrix")
	}
	if fp5, _ := Fingerprint("v1", prr, changed); fp5 == fp1 {
		t.Fatal("fingerprint must change with instantaneous matrix")
	}
}

type fixedSource struct{ v int }

func (s *fixedSource) Intn(int) int { return s.v }
