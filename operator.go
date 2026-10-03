package vecpool

import "math"

// Operator is a single vectorized execution operator. Process consumes the
// input chunk, writes its result into a chunk acquired from the pool,
// returns the consumed input chunk to the pool, and returns the output
// chunk. Implementations must not allocate.
type Operator interface {
	Process(in *Chunk, pool *Pool) *Chunk
	Name() string
}

// transform is a helper for element-wise operators that preserve length.
type transform struct {
	name string
	fn   func(dst, src []float64)
}

func (t transform) Name() string { return t.name }

func (t transform) Process(in *Chunk, pool *Pool) *Chunk {
	out := pool.Get()
	out.Len = in.Len
	t.fn(out.Data[:out.Len], in.Data[:in.Len])
	pool.Put(in)
	return out
}

// AddScalar adds v to every element.
func AddScalar(v float64) Operator {
	return transform{"AddScalar", func(dst, src []float64) {
		for i, x := range src {
			dst[i] = x + v
		}
	}}
}

// MulScalar multiplies every element by v.
func MulScalar(v float64) Operator {
	return transform{"MulScalar", func(dst, src []float64) {
		for i, x := range src {
			dst[i] = x * v
		}
	}}
}

// Square squares every element.
func Square() Operator {
	return transform{"Square", func(dst, src []float64) {
		for i, x := range src {
			dst[i] = x * x
		}
	}}
}

// Sqrt applies math.Sqrt element-wise.
func Sqrt() Operator {
	return transform{"Sqrt", func(dst, src []float64) {
		for i, x := range src {
			dst[i] = math.Sqrt(x)
		}
	}}
}

// Abs applies math.Abs element-wise.
func Abs() Operator {
	return transform{"Abs", func(dst, src []float64) {
		for i, x := range src {
			dst[i] = math.Abs(x)
		}
	}}
}

// Sin applies math.Sin element-wise.
func Sin() Operator {
	return transform{"Sin", func(dst, src []float64) {
		for i, x := range src {
			dst[i] = math.Sin(x)
		}
	}}
}

// Log1pAbs applies math.Log1p(math.Abs(x)) element-wise.
func Log1pAbs() Operator {
	return transform{"Log1pAbs", func(dst, src []float64) {
		for i, x := range src {
			dst[i] = math.Log1p(math.Abs(x))
		}
	}}
}

// Clamp limits every element to [lo, hi].
func Clamp(lo, hi float64) Operator {
	return transform{"Clamp", func(dst, src []float64) {
		for i, x := range src {
			if x < lo {
				x = lo
			} else if x > hi {
				x = hi
			}
			dst[i] = x
		}
	}}
}

// SubtractMean subtracts the chunk's mean from every element (two passes
// over the vector, still allocation-free).
func SubtractMean() Operator {
	return transform{"SubtractMean", func(dst, src []float64) {
		var sum float64
		for _, x := range src {
			sum += x
		}
		mean := sum / float64(len(src))
		for i, x := range src {
			dst[i] = x - mean
		}
	}}
}

// FilterGreaterThan keeps only elements > threshold. It is a compacting
// operator: the output chunk may be shorter than the input.
type FilterGreaterThan struct{ Threshold float64 }

func (f FilterGreaterThan) Name() string { return "FilterGreaterThan" }

func (f FilterGreaterThan) Process(in *Chunk, pool *Pool) *Chunk {
	out := pool.Get()
	n := 0
	for _, x := range in.Data[:in.Len] {
		if x > f.Threshold {
			out.Data[n] = x
			n++
		}
	}
	out.Len = n
	pool.Put(in)
	return out
}
