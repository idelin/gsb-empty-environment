package vecpool

import "math"

// Operator 是向量化算子：从 in 读取 N 个元素，将结果写入 out。
// 实现必须保证不触发任何堆分配（不使用闭包捕获、不创建切片、
// 不把值装箱进 interface{}）。
type Operator interface {
	Process(in, out *Chunk)
	Name() string
}

// ---- 10 个复杂算子实现 ----

// AddScalar 标量加法：out[i] = in[i] + k
type AddScalar struct{ K float64 }

func (o *AddScalar) Name() string { return "AddScalar" }
func (o *AddScalar) Process(in, out *Chunk) {
	out.N = in.N
	for i := 0; i < in.N; i++ {
		out.Data[i] = in.Data[i] + o.K
	}
}

// MulScalar 标量乘法：out[i] = in[i] * k
type MulScalar struct{ K float64 }

func (o *MulScalar) Name() string { return "MulScalar" }
func (o *MulScalar) Process(in, out *Chunk) {
	out.N = in.N
	for i := 0; i < in.N; i++ {
		out.Data[i] = in.Data[i] * o.K
	}
}

// SubScalar 标量减法：out[i] = in[i] - k
type SubScalar struct{ K float64 }

func (o *SubScalar) Name() string { return "SubScalar" }
func (o *SubScalar) Process(in, out *Chunk) {
	out.N = in.N
	for i := 0; i < in.N; i++ {
		out.Data[i] = in.Data[i] - o.K
	}
}

// DivScalar 标量除法：out[i] = in[i] / k
type DivScalar struct{ K float64 }

func (o *DivScalar) Name() string { return "DivScalar" }
func (o *DivScalar) Process(in, out *Chunk) {
	out.N = in.N
	for i := 0; i < in.N; i++ {
		out.Data[i] = in.Data[i] / o.K
	}
}

// AbsOp 绝对值：out[i] = |in[i]|
type AbsOp struct{}

func (o *AbsOp) Name() string { return "Abs" }
func (o *AbsOp) Process(in, out *Chunk) {
	out.N = in.N
	for i := 0; i < in.N; i++ {
		out.Data[i] = math.Abs(in.Data[i])
	}
}

// NegOp 取负：out[i] = -in[i]
type NegOp struct{}

func (o *NegOp) Name() string { return "Neg" }
func (o *NegOp) Process(in, out *Chunk) {
	out.N = in.N
	for i := 0; i < in.N; i++ {
		out.Data[i] = -in.Data[i]
	}
}

// SqrtOp 平方根（对负数取绝对值后再开方，保证数值稳定）：
// out[i] = sqrt(|in[i]|)
type SqrtOp struct{}

func (o *SqrtOp) Name() string { return "Sqrt" }
func (o *SqrtOp) Process(in, out *Chunk) {
	out.N = in.N
	for i := 0; i < in.N; i++ {
		out.Data[i] = math.Sqrt(math.Abs(in.Data[i]))
	}
}

// SquareOp 平方：out[i] = in[i] * in[i]
type SquareOp struct{}

func (o *SquareOp) Name() string { return "Square" }
func (o *SquareOp) Process(in, out *Chunk) {
	out.N = in.N
	for i := 0; i < in.N; i++ {
		v := in.Data[i]
		out.Data[i] = v * v
	}
}

// ClampOp 区间截断：out[i] = min(max(in[i], lo), hi)
type ClampOp struct{ Lo, Hi float64 }

func (o *ClampOp) Name() string { return "Clamp" }
func (o *ClampOp) Process(in, out *Chunk) {
	out.N = in.N
	for i := 0; i < in.N; i++ {
		v := in.Data[i]
		if v < o.Lo {
			v = o.Lo
		} else if v > o.Hi {
			v = o.Hi
		}
		out.Data[i] = v
	}
}

// ScaleShiftOp 仿射变换（融合乘加）：out[i] = in[i]*scale + shift
type ScaleShiftOp struct{ Scale, Shift float64 }

func (o *ScaleShiftOp) Name() string { return "ScaleShift" }
func (o *ScaleShiftOp) Process(in, out *Chunk) {
	out.N = in.N
	for i := 0; i < in.N; i++ {
		out.Data[i] = in.Data[i]*o.Scale + o.Shift
	}
}
