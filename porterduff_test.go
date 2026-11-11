package compositor

import "testing"

var allOps = []Op{
	OpClear, OpSrc, OpDst, OpOver, OpDstOver, OpIn, OpDstIn,
	OpOut, OpDstOut, OpAtop, OpDstAtop, OpXor, OpPlus,
}

// 覆盖全透明、半透明、全不透明,以及 8 位量化边界。
var testAlphas = []float64{0, 1.0 / 255, 0.25, 0.5, 0.75, 254.0 / 255, 1}

var testChannels = []float64{0, 0.13, 0.5, 0.9, 1}

func premulCases() []Color {
	var out []Color
	for _, a := range testAlphas {
		for _, r := range testChannels {
			for _, g := range testChannels {
				// 预乘颜色:分量不超过 alpha。
				out = append(out, Color{R: r * a, G: g * a, B: 0.4 * a, A: a})
			}
		}
	}
	return out
}

func TestPorterDuffMatchesReference(t *testing.T) {
	srcs := premulCases()
	dsts := premulCases()
	for _, op := range allOps {
		for _, s := range srcs {
			for _, d := range dsts {
				got := op.Composite(s, d)
				want := fromNP(refPD(op, toNP(s), toNP(d)))
				if !colorClose(got, want, eps) {
					t.Fatalf("op=%d s=%+v d=%+v: got %+v, want %+v", op, s, d, got, want)
				}
			}
		}
	}
}

func TestPlusOverflowClamps(t *testing.T) {
	cases := []struct{ s, d Color }{
		{Color{0.6, 0.6, 0.6, 0.8}, Color{0.6, 0.6, 0.6, 0.8}}, // 和 >1
		{Color{1, 1, 1, 1}, Color{1, 1, 1, 1}},
		{Color{0.5, 0.5, 0.5, 0.5}, Color{0.5, 0.5, 0.5, 0.5}}, // 恰好 1
	}
	for _, c := range cases {
		got := OpPlus.Composite(c.s, c.d)
		for _, v := range []float64{got.R, got.G, got.B, got.A} {
			if v < 0 || v > 1 {
				t.Fatalf("plus(%+v,%+v) 溢出未钳制: %+v", c.s, c.d, got)
			}
		}
	}
}

func TestOverAssociativitySample(t *testing.T) {
	// source-over 满足结合律,抽查组渲染可依赖该性质。
	a := Color{0.2, 0.1, 0.3, 0.5}
	b := Color{0.4, 0.2, 0.1, 0.6}
	c := Color{0.1, 0.3, 0.2, 0.4}
	if !colorClose(Over(a, Over(b, c)), Over(Over(a, b), c), eps) {
		t.Fatal("Over 不满足结合律")
	}
}
