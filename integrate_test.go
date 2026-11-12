package taskpkg

import (
	"math"
	"strconv"
	"testing"
)

func uniformSolution(sigma float64, em RGB, length float64) (RGB, float64) {
	trans := math.Exp(-sigma * length)
	k := (1 - trans) / sigma
	return RGB{em[0] * k, em[1] * k, em[2] * k}, trans
}

func TestIntegrateUniformMediumAnalytic(t *testing.T) {
	const length = 4.0
	sigma := 0.8
	em := RGB{0.3, 0.6, 0.9}
	g := uniformGrid(linNodes(0, length, 9), linNodes(0, 1, 2), linNodes(0, 1, 2), sigma, em)
	in, err := NewIntegrator(g, Options{MaxSteps: 20000, ColorTol: 1e-9, TransTol: 1e-9})
	if err != nil {
		t.Fatal(err)
	}

	res := in.Integrate(Vec3{-1, 0.5, 0.5}, Vec3{1, 0, 0})
	if !res.Hit {
		t.Fatal("ray should hit the grid")
	}
	wantColor, wantTrans := uniformSolution(sigma, em, length)
	approxRGB(t, res.Color, wantColor, 1e-9, "color")
	approx(t, res.Trans, wantTrans, 1e-9, "trans")
	if res.Exhausted {
		t.Fatalf("should not exhaust budget, used %d", res.Steps)
	}
	if res.Err > 1e-7 {
		t.Fatalf("reported error too large: %v", res.Err)
	}
}

func TestIntegrateZeroAbsorption(t *testing.T) {
	const length = 3.0
	em := RGB{0.2, 0.4, 0.1}
	g := uniformGrid(linNodes(0, length, 5), linNodes(0, 1, 2), linNodes(0, 1, 2), 0, em)
	in, _ := NewIntegrator(g, Options{MaxSteps: 5000})

	res := in.Integrate(Vec3{0, 0.5, 0.5}, Vec3{1, 0, 0})
	approxRGB(t, res.Color, RGB{em[0] * length, em[1] * length, em[2] * length}, 1e-9, "color")
	approx(t, res.Trans, 1, 1e-12, "trans")
}

func TestIntegrateEmptyVoxels(t *testing.T) {
	// Every voxel empty: vacuum contributes nothing and transmittance stays 1.
	g := uniformGrid(linNodes(0, 5, 6), linNodes(0, 1, 2), linNodes(0, 1, 2), 0, RGB{})
	in, _ := NewIntegrator(g, Options{MaxSteps: 1000})
	res := in.Integrate(Vec3{-1, 0.5, 0.5}, Vec3{1, 0, 0})
	approxRGB(t, res.Color, RGB{}, 1e-12, "color")
	approx(t, res.Trans, 1, 1e-12, "trans")
	if res.Exhausted {
		t.Fatal("empty grid must not consume the budget")
	}

	// A homogeneous emitting region behind a vacuum prefix must match the
	// analytic solution over just the emitting path length: vacuum has unit
	// transmittance and zero emission, so it contributes nothing.
	sigma := 0.5
	em := RGB{0.7, 0.2, 0.4}
	emitLen := 3.0
	g2 := uniformGrid(linNodes(0, emitLen, 4), linNodes(0, 1, 2), linNodes(0, 1, 2), sigma, em)
	in2, _ := NewIntegrator(g2, Options{MaxSteps: 10000, ColorTol: 1e-9, TransTol: 1e-9})
	// Start 2 units of vacuum before the grid entry.
	res2 := in2.Integrate(Vec3{-2, 0.5, 0.5}, Vec3{1, 0, 0})
	wantColor, wantTrans := uniformSolution(sigma, em, emitLen)
	approxRGB(t, res2.Color, wantColor, 1e-8, "partial color")
	approx(t, res2.Trans, wantTrans, 1e-8, "partial trans")
}

func TestGridResolutionConvergence(t *testing.T) {
	// A smooth gradient must converge as the node grid is refined. Coarse
	// grids cannot represent the continuous field, so their piecewise-linear
	// approximation is allowed a larger discrepancy that shrinks with nodes.
	sigma := 0.6
	em := RGB{0.5, 0.3, 0.2}
	length := 5.0

	build := func(nodes int) *Grid {
		xs := linNodes(0, length, nodes)
		n := nodes * 2 * 2
		sigmas := make([]float64, n)
		ems := make([]RGB, n)
		for i := 0; i < nodes; i++ {
			// Smoothly varying coefficients along x, constant in y and z.
			x := xs[i]
			s := sigma * (1 + 0.4*math.Sin(x))
			e := RGB{em[0] * (1 + 0.2*x/length),
				em[1] * (1 + 0.2*x/length),
				em[2] * (1 + 0.2*x/length)}
			for k := 0; k < 2; k++ {
				for j := 0; j < 2; j++ {
					idx := (k*2+j)*nodes + i
					sigmas[idx] = s
					ems[idx] = e
				}
			}
		}
		g, err := NewGrid(xs, linNodes(0, 1, 2), linNodes(0, 1, 2), sigmas, ems)
		if err != nil {
			panic(err)
		}
		return g
	}

	reference, err := NewIntegrator(build(65), Options{MaxSteps: 200000, ColorTol: 1e-11, TransTol: 1e-11})
	if err != nil {
		t.Fatal(err)
	}
	ref := reference.Integrate(Vec3{0, 0.5, 0.5}, Vec3{1, 0, 0})

	for _, tc := range []struct {
		nodes int
		tol   float64
	}{
		{3, 0.08},
		{5, 0.03},
		{9, 8e-3},
		{17, 2e-3},
		{33, 6e-4},
	} {
		in, _ := NewIntegrator(build(tc.nodes), Options{MaxSteps: 200000, ColorTol: 1e-9, TransTol: 1e-9})
		res := in.Integrate(Vec3{0, 0.5, 0.5}, Vec3{1, 0, 0})
		approxRGB(t, res.Color, ref.Color, tc.tol, "color nodes="+strconv.Itoa(tc.nodes))
		approx(t, res.Trans, ref.Trans, tc.tol, "trans nodes="+strconv.Itoa(tc.nodes))
		if res.Exhausted {
			t.Fatalf("nodes=%d exhausted budget", tc.nodes)
		}
	}
}

func TestStepSizeInvariance(t *testing.T) {
	// On a fixed grid, changing the effective sampling step (driven by the
	// adaptive tolerance and depth limit) must not change the converged
	// brightness. This directly guards against the step-change flicker.
	sigma := 0.6
	em := RGB{0.5, 0.3, 0.2}
	length := 5.0
	build := func() *Grid {
		const nodes = 33
		xs := linNodes(0, length, nodes)
		n := nodes * 2 * 2
		sigmas := make([]float64, n)
		ems := make([]RGB, n)
		for i := 0; i < nodes; i++ {
			x := xs[i]
			s := sigma * (1 + 0.4*math.Sin(x))
			e := RGB{em[0] * (1 + 0.2*x/length),
				em[1] * (1 + 0.2*x/length),
				em[2] * (1 + 0.2*x/length)}
			for k := 0; k < 2; k++ {
				for j := 0; j < 2; j++ {
					idx := (k*2+j)*nodes + i
					sigmas[idx] = s
					ems[idx] = e
				}
			}
		}
		g, err := NewGrid(xs, linNodes(0, 1, 2), linNodes(0, 1, 2), sigmas, ems)
		if err != nil {
			panic(err)
		}
		return g
	}

	// Reference run: tight tolerance, deep subdivision.
	refIntegrator, _ := NewIntegrator(build(), Options{MaxSteps: 200000, ColorTol: 1e-11, TransTol: 1e-11})
	ref := refIntegrator.Integrate(Vec3{0, 0.5, 0.5}, Vec3{1, 0, 0})

	// Looser tolerances and a shallow depth cap mean larger effective steps;
	// results must stay inside the integrator's own error envelope.
	for _, opt := range []Options{
		{MaxSteps: 200000, ColorTol: 1e-3, TransTol: 1e-4},
		{MaxSteps: 200000, ColorTol: 1e-2, TransTol: 1e-3},
		{MaxSteps: 200000, ColorTol: 1e-2, TransTol: 1e-3, MaxDepth: 1},
	} {
		in, err := NewIntegrator(build(), opt)
		if err != nil {
			t.Fatal(err)
		}
		res := in.Integrate(Vec3{0, 0.5, 0.5}, Vec3{1, 0, 0})
		for c := 0; c < 3; c++ {
			if math.Abs(res.Color[c]-ref.Color[c]) > res.ColorErr[c]+1e-9 {
				t.Fatalf("channel %d outside reported error envelope", c)
			}
		}
		if math.Abs(res.Trans-ref.Trans) > res.TransErr+1e-9 {
			t.Fatal("transmittance outside reported error envelope")
		}
		if res.Err > 2e-2 {
			t.Fatalf("unexpectedly large error for non-exhausted run: %v", res.Err)
		}
	}
}

func TestNonUniformGridBoundaries(t *testing.T) {
	// Deliberately irregular spacing; a constant medium still reproduces the
	// analytic solution, proving boundary crossings use exact node positions.
	sigma := 1.1
	em := RGB{0.35, 0.55, 0.75}
	xs := []float64{0, 0.3, 1.2, 1.25, 3.0, 4.8, 5.0}
	g := uniformGrid(xs, linNodes(0, 2, 3), linNodes(0, 2, 3), sigma, em)
	in, _ := NewIntegrator(g, Options{MaxSteps: 20000, ColorTol: 1e-10, TransTol: 1e-10})

	res := in.Integrate(Vec3{0, 1, 1}, Vec3{1, 0, 0})
	wantColor, wantTrans := uniformSolution(sigma, em, 5.0)
	approxRGB(t, res.Color, wantColor, 1e-9, "color")
	approx(t, res.Trans, wantTrans, 1e-9, "trans")

	// Ray starting inside and ending inside spans exactly the interior length.
	res2 := in.Integrate(Vec3{1.25, 1, 1}, Vec3{1, 0, 0})
	wantColor2, wantTrans2 := uniformSolution(sigma, em, 5.0-1.25)
	approxRGB(t, res2.Color, wantColor2, 1e-9, "interior color")
	approx(t, res2.Trans, wantTrans2, 1e-9, "interior trans")

	// Reverse direction must give the same total for a constant medium.
	back := in.Integrate(Vec3{5, 1, 1}, Vec3{-1, 0, 0})
	approxRGB(t, back.Color, wantColor, 1e-9, "backward color")
	approx(t, back.Trans, wantTrans, 1e-9, "backward trans")
}

func TestBudgetExhaustionReportsRemainder(t *testing.T) {
	// Dense, strongly varying grid with a tiny budget forces exhaustion.
	const length = 4.0
	nodes := 41
	xs := linNodes(0, length, nodes)
	n := nodes * 2 * 2
	sigmas := make([]float64, n)
	ems := make([]RGB, n)
	for i := 0; i < nodes; i++ {
		x := xs[i]
		s := 1.0 + 0.9*math.Sin(3*x)
		for k := 0; k < 2; k++ {
			for j := 0; j < 2; j++ {
				idx := (k*2+j)*nodes + i
				sigmas[idx] = s
				ems[idx] = RGB{0.8 + 0.2*math.Cos(x), 0.3, 0.1}
			}
		}
	}
	g, err := NewGrid(xs, linNodes(0, 1, 2), linNodes(0, 1, 2), sigmas, ems)
	if err != nil {
		t.Fatal(err)
	}

	in, _ := NewIntegrator(g, Options{MaxSteps: 5, ColorTol: 1e-12, TransTol: 1e-12})
	res := in.Integrate(Vec3{0, 0.5, 0.5}, Vec3{1, 0, 0})
	if !res.Exhausted {
		t.Fatal("expected budget exhaustion")
	}
	if res.Steps > 5 {
		t.Fatalf("used %d steps, budget is 5", res.Steps)
	}
	if res.Err <= 0 {
		t.Fatal("exhausted run must report a positive remaining error")
	}
	if res.TransErr < 0 || res.TransErr > 1+1e-12 {
		t.Fatalf("transmittance error out of range: %v", res.TransErr)
	}

	// The well-converged answer must lie inside the claimed error envelope.
	full, _ := NewIntegrator(g, Options{MaxSteps: 200000, ColorTol: 1e-11, TransTol: 1e-11})
	converged := full.Integrate(Vec3{0, 0.5, 0.5}, Vec3{1, 0, 0})
	for c := 0; c < 3; c++ {
		if math.Abs(res.Color[c]-converged.Color[c]) > res.ColorErr[c]+1e-9 {
			t.Fatalf("channel %d outside error bound: %.6e vs bound %.6e",
				c, math.Abs(res.Color[c]-converged.Color[c]), res.ColorErr[c])
		}
	}
	if math.Abs(res.Trans-converged.Trans) > res.TransErr+1e-9 {
		t.Fatalf("transmittance outside error bound: %.6e vs %.6e",
			math.Abs(res.Trans-converged.Trans), res.TransErr)
	}
}

func TestDirectionMagnitudeInvariance(t *testing.T) {
	// Scaling the direction vector changes only the ray parameterization,
	// never the physical chord or the integrated radiance/transmittance.
	sigma := 0.9
	em := RGB{0.25, 0.5, 0.75}
	g := uniformGrid(linNodes(0, 3, 4), linNodes(0, 2, 3), linNodes(0, 2, 3), sigma, em)
	in, _ := NewIntegrator(g, Options{MaxSteps: 50000, ColorTol: 1e-10, TransTol: 1e-10})

	base := in.Integrate(Vec3{0, 0.5, 1}, Vec3{1, 0, 0})
	for _, scale := range []float64{0.25, 0.5, 2, 4} {
		res := in.Integrate(Vec3{0, 0.5, 1}, Vec3{scale, 0, 0})
		approxRGB(t, res.Color, base.Color, 1e-9, "color scale="+strconv.FormatFloat(scale, 'g', -1, 64))
		approx(t, res.Trans, base.Trans, 1e-9, "trans scale="+strconv.FormatFloat(scale, 'g', -1, 64))
	}
}

func TestBackgroundAndMiss(t *testing.T) {
	sigma := 0.5
	em := RGB{0.1, 0.1, 0.1}
	g := uniformGrid(linNodes(0, 2, 3), linNodes(0, 1, 2), linNodes(0, 1, 2), sigma, em)
	bg := RGB{0.4, 0.6, 0.8}
	in, _ := NewIntegrator(g, Options{MaxSteps: 1000, Background: bg})

	res := in.Integrate(Vec3{0, 0.5, 0.5}, Vec3{1, 0, 0})
	wantEm, wantTrans := uniformSolution(sigma, em, 2.0)
	want := RGB{
		wantEm[0] + wantTrans*bg[0],
		wantEm[1] + wantTrans*bg[1],
		wantEm[2] + wantTrans*bg[2],
	}
	approxRGB(t, res.Color, want, 1e-9, "background color")

	miss := in.Integrate(Vec3{5, 5, 5}, Vec3{1, 0, 0})
	if miss.Hit {
		t.Fatal("ray should miss")
	}
	approx(t, miss.Trans, 1, testEps, "miss transmittance")
	approxRGB(t, miss.Color, RGB{}, testEps, "miss color")

	zero := in.Integrate(Vec3{0, 0, 0}, Vec3{})
	if zero.Hit {
		t.Fatal("zero direction must not hit")
	}
}

func TestDiagonalCrossingNonUniform(t *testing.T) {
	// A diagonal ray crosses boundaries on all three axes. With a constant
	// medium the answer must still equal the analytic solution over the
	// geometric chord, which only holds if every boundary is located exactly.
	sigma := 0.9
	em := RGB{0.25, 0.5, 0.75}
	xs := []float64{0, 0.4, 1.1, 2.0, 3.0}
	ys := []float64{0, 0.7, 1.6, 2.4, 3.0}
	zs := []float64{0, 1.3, 2.2, 3.0}
	g := uniformGrid(xs, ys, zs, sigma, em)
	in, _ := NewIntegrator(g, Options{MaxSteps: 50000, ColorTol: 1e-10, TransTol: 1e-10})

	origin := Vec3{0, 0, 0}
	dir := Vec3{1, 1, 1}
	res := in.Integrate(origin, dir)
	chord := 3 * math.Sqrt(3) // parameter span 3 times |dir| = sqrt(3)
	wantColor, wantTrans := uniformSolution(sigma, em, chord)
	approxRGB(t, res.Color, wantColor, 1e-8, "diagonal color")
	approx(t, res.Trans, wantTrans, 1e-8, "diagonal trans")

	// Offset start so entry happens through a non-axis-aligned interior point.
	res2 := in.Integrate(Vec3{-1, -0.5, -2}, Vec3{1, 1, 1})
	// Parameter entry at max(1, 0.5, 2) = 2 and exit at min(4, 3.5, 5) = 3.5.
	chord2 := 1.5 * math.Sqrt(3) // parameter span 1.5 times |dir|
	wantColor2, wantTrans2 := uniformSolution(sigma, em, chord2)
	approxRGB(t, res2.Color, wantColor2, 1e-8, "offset diagonal color")
	approx(t, res2.Trans, wantTrans2, 1e-8, "offset diagonal trans")
}

func TestTransmittanceMonotone(t *testing.T) {
	// Adding more absorbing material along a ray can only lower transmittance.
	sigma := 0.9
	em := RGB{0.2, 0.2, 0.2}
	g := uniformGrid(linNodes(0, 3, 4), linNodes(0, 1, 2), linNodes(0, 1, 2), sigma, em)
	in, _ := NewIntegrator(g, Options{MaxSteps: 5000})

	prev := 1.0
	for _, length := range []float64{0.5, 1.0, 2.0, 3.0} {
		res := in.Integrate(Vec3{3 - length, 0.5, 0.5}, Vec3{1, 0, 0})
		if res.Trans > prev+1e-12 {
			t.Fatalf("transmittance increased: %.6f > %.6f", res.Trans, prev)
		}
		if res.Trans < 0 || res.Trans > 1 {
			t.Fatalf("transmittance out of [0,1]: %.6f", res.Trans)
		}
		prev = res.Trans
	}
}
