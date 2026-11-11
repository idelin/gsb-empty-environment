package compositor

// Color 是预乘 alpha 的 RGBA 颜色,各通道通常位于 [0,1]。
// 预乘不变式为 R,G,B <= A;OpPlus 等算子在钳制前可能暂时产生
// 超出 [0,1] 的值。
type Color struct {
	R, G, B, A float64
}

// Scale 将颜色各通道(含 alpha)乘以 f,用于图层/组透明度与遮罩。
func (c Color) Scale(f float64) Color {
	return Color{R: c.R * f, G: c.G * f, B: c.B * f, A: c.A * f}
}

// Clamped 返回各通道钳制到 [0,1] 后的颜色。
func (c Color) Clamped() Color {
	return Color{clamp01(c.R), clamp01(c.G), clamp01(c.B), clamp01(c.A)}
}

// unpremul 返回非预乘的 RGB 分量;alpha 为 0 时颜色定义为 0。
func (c Color) unpremul() (r, g, b float64) {
	if c.A <= 0 {
		return 0, 0, 0
	}
	return c.R / c.A, c.G / c.A, c.B / c.A
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
