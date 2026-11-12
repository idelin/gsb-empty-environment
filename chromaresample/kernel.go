package chromaresample

import "math"

// Kernel is a separable, radially symmetric reconstruction kernel evaluated
// in input-pixel units. Support is the one-sided radius: the kernel is zero
// (or treated as zero) for |x| >= Support.
type Kernel interface {
	Support() float64
	Value(x float64) float64
}

// integratedKernel is implemented by kernels that provide an exact
// antiderivative. Analytic integration keeps piecewise-linear input such as
// ramps reconstructable without quadrature error.
type integratedKernel interface {
	Kernel
	// Integral returns the definite integral from a to b of Value.
	Integral(a, b float64) float64
}

// BoxKernel is the unit-area box kernel on [-1/2, 1/2). Integrating it over
// input cells yields exact area-overlap (pixel area) weights.
type BoxKernel struct{}

func (BoxKernel) Support() float64 { return 0.5 }

func (BoxKernel) Value(x float64) float64 {
	if x >= -0.5 && x < 0.5 {
		return 1
	}
	return 0
}

func (BoxKernel) Integral(a, b float64) float64 {
	lo := math.Max(a, -0.5)
	hi := math.Min(b, 0.5)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

// TriangleKernel is the unit-area linear (bilinear) kernel on [-1, 1].
type TriangleKernel struct{}

func (TriangleKernel) Support() float64 { return 1 }

func (TriangleKernel) Value(x float64) float64 {
	if x < 0 {
		x = -x
	}
	if x < 1 {
		return 1 - x
	}
	return 0
}

func (TriangleKernel) Integral(a, b float64) float64 {
	return triangleAntiderivative(b) - triangleAntiderivative(a)
}

func triangleAntiderivative(x float64) float64 {
	if x <= -1 {
		return 0
	}
	if x < 0 {
		// F(x) = x + x^2/2 + 1/2
		return x + 0.5*x*x + 0.5
	}
	if x < 1 {
		// F(x) = x - x^2/2 + 1/2
		return x - 0.5*x*x + 0.5
	}
	return 1
}

// LanczosKernel is the unit-area Lanczos kernel with parameter Taps (typically
// 2 or 3). It is integrated numerically.
type LanczosKernel struct {
	Taps int
}

func (k LanczosKernel) Support() float64 { return float64(k.Taps) }

func (k LanczosKernel) Value(x float64) float64 {
	if x <= -float64(k.Taps) || x >= float64(k.Taps) {
		return 0
	}
	return sinc(x) * sinc(x/float64(k.Taps))
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	pi := math.Pi * x
	return math.Sin(pi) / pi
}

// glNodes and glWeights are the 8-point Gauss-Legendre quadrature nodes and
// weights on [-1, 1], used to integrate kernels without an analytic
// antiderivative over each input cell.
var glNodes = [8]float64{
	-0.9602898564975363,
	-0.7966664774136267,
	-0.5255324099163290,
	-0.1834346424956498,
	0.1834346424956498,
	0.5255324099163290,
	0.7966664774136267,
	0.9602898564975363,
}

var glWeights = [8]float64{
	0.1012285362903763,
	0.2223810344533745,
	0.3137066458778873,
	0.3626837833783620,
	0.3626837833783620,
	0.3137066458778873,
	0.2223810344533745,
	0.1012285362903763,
}

// integrate computes the integral of k from a to b. It uses the analytic
// antiderivative when available and Gauss-Legendre quadrature otherwise.
func integrate(k Kernel, a, b float64) float64 {
	if ik, ok := k.(integratedKernel); ok {
		return ik.Integral(a, b)
	}
	mid := 0.5 * (a + b)
	half := 0.5 * (b - a)
	sum := 0.0
	for i := range glNodes {
		sum += glWeights[i] * k.Value(mid+half*glNodes[i])
	}
	return half * sum
}
