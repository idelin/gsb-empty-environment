package compositor

// Op 是 Porter-Duff 合成算子,输入输出均为预乘 alpha 颜色。
// 通用形式为 co = αs·Fs·Cs + αb·Fb·Cb(对 RGB 与 A 同时成立,
// 其中 Cs、Cb 为非预乘色,等价地直接作用于预乘分量)。
type Op int

const (
	OpClear   Op = iota // 0
	OpSrc               // s
	OpDst               // d
	OpOver              // s + d·(1-αs)
	OpDstOver           // d + s·(1-αb)
	OpIn                // s·αb
	OpDstIn             // d·αs
	OpOut               // s·(1-αb)
	OpDstOut            // d·(1-αs)
	OpAtop              // s·αb + d·(1-αs)
	OpDstAtop           // d·αs + s·(1-αb)
	OpXor               // s·(1-αb) + d·(1-αs)
	OpPlus              // s + d,结果钳制到 [0,1](与浏览器一致)
)

// Composite 用 op 将源色 s 合成到目标色 d 上。
func (op Op) Composite(s, d Color) Color {
	switch op {
	case OpClear:
		return Color{}
	case OpSrc:
		return s
	case OpDst:
		return d
	case OpOver:
		return Over(s, d)
	case OpDstOver:
		return Over(d, s)
	case OpIn:
		return s.Scale(d.A)
	case OpDstIn:
		return d.Scale(s.A)
	case OpOut:
		return s.Scale(1 - d.A)
	case OpDstOut:
		return d.Scale(1 - s.A)
	case OpAtop:
		return add(s.Scale(d.A), d.Scale(1-s.A))
	case OpDstAtop:
		return add(d.Scale(s.A), s.Scale(1-d.A))
	case OpXor:
		return add(s.Scale(1-d.A), d.Scale(1-s.A))
	case OpPlus:
		return add(s, d).Clamped()
	default:
		return Color{}
	}
}

// Over 是 source-over:co = s + d·(1-αs),是混合模式的默认合成算子。
func Over(s, d Color) Color {
	f := 1 - s.A
	return Color{
		R: s.R + d.R*f,
		G: s.G + d.G*f,
		B: s.B + d.B*f,
		A: s.A + d.A*f,
	}
}

func add(a, b Color) Color {
	return Color{R: a.R + b.R, G: a.G + b.G, B: a.B + b.B, A: a.A + b.A}
}
