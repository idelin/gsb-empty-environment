package compositor

import "math"

// Mode 是 W3C Compositing and Blending Level 1 定义的混合模式。
// 混合在 source-over 合成的基础上进行:
//
//	αo = αs + αb·(1-αs)
//	co = αs·(1-αb)·Cs + αs·αb·B(Cb, Cs) + (1-αs)·cb
//
// 其中 Cs、Cb 为非预乘色,cb 为预乘背景色,B 为逐通道(可分离)
// 或逐像素(不可分离)的混合函数。
type Mode int

const (
	ModeNormal Mode = iota
	ModeMultiply
	ModeScreen
	ModeOverlay
	ModeDarken
	ModeLighten
	ModeColorDodge
	ModeColorBurn
	ModeHardLight
	ModeSoftLight
	ModeDifference
	ModeExclusion
	ModeHue
	ModeSaturation
	ModeColor
	ModeLuminosity
)

// Blend 按混合模式 m 将预乘源色 s 合成到预乘背景色 b 上。
// ModeNormal 等价于 Over;αs 为 0 时结果恒为背景。
func (m Mode) Blend(s, b Color) Color {
	if m == ModeNormal {
		return Over(s, b)
	}
	as, ab := s.A, b.A
	if as <= 0 {
		return b
	}
	sr, sg, sb := s.unpremul()
	br, bg, bb := b.unpremul()
	bs := [3]float64{sr, sg, sb}
	bd := [3]float64{br, bg, bb}
	mixed := m.mix(bd, bs)
	spill := as * (1 - ab) // 背景透明区域回退到源色
	keep := 1 - as         // 源透明区域保留背景
	return Color{
		R: spill*bs[0] + as*ab*mixed[0] + keep*b.R,
		G: spill*bs[1] + as*ab*mixed[1] + keep*b.G,
		B: spill*bs[2] + as*ab*mixed[2] + keep*b.B,
		A: as + ab*(1-as),
	}
}

// mix 计算 B(Cb, Cs),输入为非预乘 RGB 三元组。
func (m Mode) mix(cb, cs [3]float64) [3]float64 {
	switch m {
	case ModeHue:
		return setLum(setSat(cs, sat(cb)), lum(cb))
	case ModeSaturation:
		return setLum(setSat(cb, sat(cs)), lum(cb))
	case ModeColor:
		return setLum(cs, lum(cb))
	case ModeLuminosity:
		return setLum(cb, lum(cs))
	}
	f := m.separable()
	return [3]float64{f(cb[0], cs[0]), f(cb[1], cs[1]), f(cb[2], cs[2])}
}

func (m Mode) separable() func(cb, cs float64) float64 {
	switch m {
	case ModeMultiply:
		return func(cb, cs float64) float64 { return cb * cs }
	case ModeScreen:
		return func(cb, cs float64) float64 { return cb + cs - cb*cs }
	case ModeOverlay:
		return func(cb, cs float64) float64 { return hardLight(cs, cb) }
	case ModeDarken:
		return func(cb, cs float64) float64 { return min(cb, cs) }
	case ModeLighten:
		return func(cb, cs float64) float64 { return max(cb, cs) }
	case ModeColorDodge:
		return colorDodge
	case ModeColorBurn:
		return colorBurn
	case ModeHardLight:
		return hardLight
	case ModeSoftLight:
		return softLight
	case ModeDifference:
		return func(cb, cs float64) float64 {
			if cb > cs {
				return cb - cs
			}
			return cs - cb
		}
	case ModeExclusion:
		return func(cb, cs float64) float64 { return cb + cs - 2*cb*cs }
	default: // ModeNormal
		return func(cb, cs float64) float64 { return cs }
	}
}

func hardLight(cb, cs float64) float64 {
	if cs <= 0.5 {
		return cb * 2 * cs
	}
	return cb + (2*cs-1)*(1-cb) // screen(cb, 2cs-1)
}

func colorDodge(cb, cs float64) float64 {
	if cb == 0 {
		return 0
	}
	if cs >= 1 {
		return 1
	}
	return min(1, cb/(1-cs))
}

func colorBurn(cb, cs float64) float64 {
	if cb >= 1 {
		return 1
	}
	if cs <= 0 {
		return 0
	}
	return 1 - min(1, (1-cb)/cs)
}

func softLight(cb, cs float64) float64 {
	if cs <= 0.5 {
		return cb - (1-2*cs)*cb*(1-cb)
	}
	var d float64
	if cb <= 0.25 {
		d = ((16*cb-12)*cb + 4) * cb
	} else {
		d = math.Sqrt(cb)
	}
	return cb + (2*cs-1)*(d-cb)
}

// 不可分离模式的辅助函数,按 W3C 规范定义。

func lum(c [3]float64) float64 { return 0.3*c[0] + 0.59*c[1] + 0.11*c[2] }

func sat(c [3]float64) float64 {
	return max(c[0], c[1], c[2]) - min(c[0], c[1], c[2])
}

func clipColor(c [3]float64) [3]float64 {
	l := lum(c)
	n := min(c[0], c[1], c[2])
	x := max(c[0], c[1], c[2])
	if n < 0 {
		for i := range c {
			c[i] = l + (c[i]-l)*l/(l-n)
		}
	}
	if x > 1 {
		for i := range c {
			c[i] = l + (c[i]-l)*(1-l)/(x-l)
		}
	}
	return c
}

func setLum(c [3]float64, l float64) [3]float64 {
	d := l - lum(c)
	return clipColor([3]float64{c[0] + d, c[1] + d, c[2] + d})
}

func setSat(c [3]float64, s float64) [3]float64 {
	// 通道按值排序:min/mid/max;max>min 时按比例重设饱和度。
	idx := [3]int{0, 1, 2}
	if c[idx[0]] > c[idx[1]] {
		idx[0], idx[1] = idx[1], idx[0]
	}
	if c[idx[1]] > c[idx[2]] {
		idx[1], idx[2] = idx[2], idx[1]
	}
	if c[idx[0]] > c[idx[1]] {
		idx[0], idx[1] = idx[1], idx[0]
	}
	mn, mx := c[idx[0]], c[idx[2]]
	var out [3]float64
	if mx > mn {
		out[idx[1]] = (c[idx[1]] - mn) * s / (mx - mn)
		out[idx[2]] = s
	}
	return out
}
