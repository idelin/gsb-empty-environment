package compositor

import "testing"

// fillPremul8 用确定性的伪随机像素填充图像,保证满足预乘不变式
// (R,G,B <= A),避免写入钳制掩盖量化误差。
func fillPremul8(im *Image8, seed uint8) {
	for p := 0; p < im.W*im.H; p++ {
		a := uint8((int(seed) + p*41) % 256)
		im.Pix[4*p+3] = a
		for c := 0; c < 3; c++ {
			v := uint8((int(seed)*7 + p*13 + c*59) % 256)
			im.Pix[4*p+c] = v * a / 255
		}
	}
}

var allModes = []Mode{
	ModeNormal, ModeMultiply, ModeScreen, ModeOverlay, ModeDarken,
	ModeLighten, ModeColorDodge, ModeColorBurn, ModeHardLight,
	ModeSoftLight, ModeDifference, ModeExclusion, ModeHue,
	ModeSaturation, ModeColor, ModeLuminosity,
}

func TestBlendMatchesReference(t *testing.T) {
	srcs := premulCases()
	dsts := premulCases()
	for _, m := range allModes {
		for _, s := range srcs {
			for _, d := range dsts {
				got := m.Blend(s, d)
				want := fromNP(refBlend(m, toNP(s), toNP(d)))
				if !colorClose(got, want, eps) {
					t.Fatalf("mode=%d s=%+v d=%+v: got %+v, want %+v", m, s, d, got, want)
				}
			}
		}
	}
}

func TestBlendTransparentEdges(t *testing.T) {
	opaque := Color{0.8, 0.4, 0.2, 1}
	semi := Color{0.3, 0.15, 0.45, 0.5}
	clear := Color{}
	for _, m := range allModes {
		// 源全透明:结果恒为背景。
		if got := m.Blend(clear, semi); !colorClose(got, semi, eps) {
			t.Fatalf("mode=%d 透明源应保留背景: got %+v want %+v", m, got, semi)
		}
		// 背景全透明:结果恒为源。
		if got := m.Blend(semi, clear); !colorClose(got, semi, eps) {
			t.Fatalf("mode=%d 透明背景应透出源: got %+v want %+v", m, got, semi)
		}
		// 双方全透明:结果全透明。
		if got := m.Blend(clear, clear); got != (Color{}) {
			t.Fatalf("mode=%d 双透明应为透明: got %+v", m, got)
		}
		// 不透明源与背景:结果 alpha 为 1。
		if got := m.Blend(opaque, opaque); got.A != 1 {
			t.Fatalf("mode=%d 不透明合成 alpha 应为 1: got %+v", m, got)
		}
	}
}

// TestEightBitVsFloatSingleLayer 验证单层合成时两条路径的误差界:
// 8 位路径仅在写入 dst 时量化一次,误差 <= 0.5/255(加浮点噪声容差)。
func TestEightBitVsFloatSingleLayer(t *testing.T) {
	const tol = 0.5/255 + 1e-9
	src := NewImage8(4, 4)
	fillPremul8(src, 11)
	for _, m := range allModes {
		f := NewImageF(4, 4)
		b := NewImage8(4, 4)
		fillPremul8(b, 53)
		// 两条路径从完全相同的 dst 像素出发。
		for i := range f.Pix {
			f.Pix[i] = b.At(i%4, i/4)
		}
		layer := Layer{Src: src, Mode: m, Opa: 0.8}
		DrawLayer(f, layer)
		DrawLayer(b, layer)
		for i := range f.Pix {
			got := b.At(i%4, i/4)
			if !colorClose(got, f.Pix[i], tol) {
				t.Fatalf("mode=%d px=%d: 8位 %+v 浮点 %+v 超出误差界 %v",
					m, i, got, f.Pix[i], tol)
			}
		}
	}
}

func TestImage8QuantizationBound(t *testing.T) {
	im := NewImage8(1, 1)
	for _, v := range []float64{0, 0.1, 0.5, 0.9, 1, 1.0 / 255, 254.0 / 255} {
		im.Set(0, 0, Color{v, v, v, 1})
		got := im.At(0, 0)
		if d := got.R - v; d < -0.5/255-1e-12 || d > 0.5/255+1e-12 {
			t.Fatalf("量化误差越界: in=%v out=%v", v, got.R)
		}
	}
	// 溢出写入被钳制。
	im.Set(0, 0, Color{1.4, -0.2, 0.5, 2})
	if got := im.At(0, 0); got != (Color{1, 0, 128.0 / 255, 1}) {
		t.Fatalf("钳制失败: %+v", got)
	}
}
