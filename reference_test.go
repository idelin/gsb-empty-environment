package compositor

// 本文件是测试专用的参考实现:严格按 W3C Compositing and Blending
// Level 1 的规范公式,在非预乘 alpha 空间逐像素计算,与被测的
// 预乘实现相互独立,用于交叉验证。

import "math"

// npColor 是非预乘 RGBA,各通道位于 [0,1]。
type npColor struct {
	r, g, b, a float64
}

func toNP(c Color) npColor {
	r, g, b := c.unpremul()
	return npColor{r, g, b, c.A}
}

func fromNP(c npColor) Color {
	return Color{R: c.r * c.a, G: c.g * c.a, B: c.b * c.a, A: c.a}
}

// refPD 按 Porter-Duff 通用式 co = αs·Fs·Cs + αb·Fb·Cb 计算。
func refPD(op Op, s, d npColor) npColor {
	var fs, fb float64
	switch op {
	case OpClear:
		fs, fb = 0, 0
	case OpSrc:
		fs, fb = 1, 0
	case OpDst:
		fs, fb = 0, 1
	case OpOver:
		fs, fb = 1, 1-s.a
	case OpDstOver:
		fs, fb = 1-d.a, 1
	case OpIn:
		fs, fb = d.a, 0
	case OpDstIn:
		fs, fb = 0, s.a
	case OpOut:
		fs, fb = 1-d.a, 0
	case OpDstOut:
		fs, fb = 0, 1-s.a
	case OpAtop:
		fs, fb = d.a, 1-s.a
	case OpDstAtop:
		fs, fb = 1-d.a, s.a
	case OpXor:
		fs, fb = 1-d.a, 1-s.a
	case OpPlus:
		fs, fb = 1, 1
	}
	out := npColor{
		r: s.a*fs*s.r + d.a*fb*d.r,
		g: s.a*fs*s.g + d.a*fb*d.g,
		b: s.a*fs*s.b + d.a*fb*d.b,
		a: s.a*fs + d.a*fb,
	}
	if op == OpPlus {
		// plus 在预乘空间钳制;换算到非预乘空间等价于先钳制预乘值。
		pm := Color{out.r, out.g, out.b, out.a}.Clamped()
		return toNP(pm)
	}
	if out.a > 0 {
		out.r /= out.a
		out.g /= out.a
		out.b /= out.a
	}
	return out
}

// refBlend 按规范混合公式逐像素计算:
//
//	αo = αs + αb·(1-αs)
//	co = αs·(1-αb)·Cs + αs·αb·B(Cb, Cs) + (1-αs)·αb·Cb
func refBlend(m Mode, s, d npColor) npColor {
	ao := s.a + d.a*(1-s.a)
	cs := [3]float64{s.r, s.g, s.b}
	cb := [3]float64{d.r, d.g, d.b}
	mixed := refMix(m, cb, cs)
	var out npColor
	out.a = ao
	co := [3]float64{}
	for i := 0; i < 3; i++ {
		co[i] = s.a*(1-d.a)*cs[i] + s.a*d.a*mixed[i] + (1-s.a)*d.a*cb[i]
	}
	if ao > 0 {
		out.r = co[0] / ao
		out.g = co[1] / ao
		out.b = co[2] / ao
	}
	return out
}

func refMix(m Mode, cb, cs [3]float64) [3]float64 {
	switch m {
	case ModeHue:
		return refSetLum(refSetSat(cs, refSat(cb)), refLum(cb))
	case ModeSaturation:
		return refSetLum(refSetSat(cb, refSat(cs)), refLum(cb))
	case ModeColor:
		return refSetLum(cs, refLum(cb))
	case ModeLuminosity:
		return refSetLum(cb, refLum(cs))
	}
	var out [3]float64
	for i := 0; i < 3; i++ {
		out[i] = refSep(m, cb[i], cs[i])
	}
	return out
}

func refSep(m Mode, cb, cs float64) float64 {
	switch m {
	case ModeMultiply:
		return cb * cs
	case ModeScreen:
		return cb + cs - cb*cs
	case ModeOverlay:
		return refHardLight(cs, cb)
	case ModeDarken:
		return min(cb, cs)
	case ModeLighten:
		return max(cb, cs)
	case ModeColorDodge:
		if cb == 0 {
			return 0
		}
		if cs >= 1 {
			return 1
		}
		return min(1, cb/(1-cs))
	case ModeColorBurn:
		if cb >= 1 {
			return 1
		}
		if cs <= 0 {
			return 0
		}
		return 1 - min(1, (1-cb)/cs)
	case ModeHardLight:
		return refHardLight(cb, cs)
	case ModeSoftLight:
		if cs <= 0.5 {
			return cb - (1-2*cs)*cb*(1-cb)
		}
		var dd float64
		if cb <= 0.25 {
			dd = ((16*cb-12)*cb + 4) * cb
		} else {
			dd = math.Sqrt(cb)
		}
		return cb + (2*cs-1)*(dd-cb)
	case ModeDifference:
		return math.Abs(cb - cs)
	case ModeExclusion:
		return cb + cs - 2*cb*cs
	default: // ModeNormal
		return cs
	}
}

func refHardLight(cb, cs float64) float64 {
	if cs <= 0.5 {
		return cb * 2 * cs
	}
	return cb + (2*cs-1)*(1-cb)
}

func refLum(c [3]float64) float64 { return 0.3*c[0] + 0.59*c[1] + 0.11*c[2] }

func refSat(c [3]float64) float64 {
	return max(c[0], c[1], c[2]) - min(c[0], c[1], c[2])
}

func refClipColor(c [3]float64) [3]float64 {
	l := refLum(c)
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

func refSetLum(c [3]float64, l float64) [3]float64 {
	d := l - refLum(c)
	return refClipColor([3]float64{c[0] + d, c[1] + d, c[2] + d})
}

func refSetSat(c [3]float64, s float64) [3]float64 {
	mn := min(c[0], c[1], c[2])
	mx := max(c[0], c[1], c[2])
	var out [3]float64
	for i := 0; i < 3; i++ {
		switch {
		case c[i] == mn:
			out[i] = 0
		case c[i] == mx:
			if mx > mn {
				out[i] = s
			}
		default:
			if mx > mn {
				out[i] = (c[i] - mn) * s / (mx - mn)
			}
		}
	}
	return out
}

const eps = 1e-9

func colorClose(a, b Color, tol float64) bool {
	return math.Abs(a.R-b.R) <= tol &&
		math.Abs(a.G-b.G) <= tol &&
		math.Abs(a.B-b.B) <= tol &&
		math.Abs(a.A-b.A) <= tol
}
