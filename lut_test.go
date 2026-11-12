package taskpkg

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func logicalNodeIndex(layout Layout, sizeR, sizeG, sizeB, r, g, b int) int {
	if layout == BlueFastest {
		return ((r*sizeG)+g)*sizeB + b
	}
	return ((b*sizeG)+g)*sizeR + r
}

func putNode(table []float64, layout Layout, sizeR, sizeG, sizeB, r, g, b int, c RGB) {
	off := logicalNodeIndex(layout, sizeR, sizeG, sizeB, r, g, b) * 3
	table[off+0] = c.R
	table[off+1] = c.G
	table[off+2] = c.B
}

func affineTable(sizeR, sizeG, sizeB int, layout Layout, lower, upper [3]float64,
	matrix [3][3]float64, offset [3]float64) []float64 {
	table := make([]float64, sizeR*sizeG*sizeB*3)
	for b := 0; b < sizeB; b++ {
		fb := fraction(b, sizeB)
		for g := 0; g < sizeG; g++ {
			fg := fraction(g, sizeG)
			for r := 0; r < sizeR; r++ {
				fr := fraction(r, sizeR)
				x := [3]float64{
					lower[0] + fr*(upper[0]-lower[0]),
					lower[1] + fg*(upper[1]-lower[1]),
					lower[2] + fb*(upper[2]-lower[2]),
				}
				var out [3]float64
				for row := 0; row < 3; row++ {
					out[row] = offset[row]
					for col := 0; col < 3; col++ {
						out[row] += matrix[row][col] * x[col]
					}
				}
				putNode(table, layout, sizeR, sizeG, sizeB, r, g, b,
					RGB{R: out[0], G: out[1], B: out[2]})
			}
		}
	}
	return table
}

func fraction(index, size int) float64 {
	if size <= 1 {
		return 0
	}
	return float64(index) / float64(size-1)
}

func randomTable(sizeR, sizeG, sizeB int, layout Layout, rng *rand.Rand) []float64 {
	table := make([]float64, sizeR*sizeG*sizeB*3)
	for i := range table {
		table[i] = rng.NormFloat64()
	}
	return table
}

func TestAffineRecovery(t *testing.T) {
	lower := [3]float64{-0.25, 0.5, 2.0}
	upper := [3]float64{1.75, 3.5, 9.0}
	matrix := [3][3]float64{
		{0.73, -0.21, 0.44},
		{0.15, 1.07, -0.33},
		{-0.48, 0.26, 0.91},
	}
	offset := [3]float64{0.11, -1.3, 0.7}

	cases := []struct {
		size   [3]int
		layout Layout
	}{
		{[3]int{2, 2, 2}, RedFastest},
		{[3]int{3, 5, 4}, RedFastest},
		{[3]int{6, 3, 2}, BlueFastest},
		{[3]int{2, 7, 5}, BlueFastest},
	}

	for _, tc := range cases {
		table := affineTable(tc.size[0], tc.size[1], tc.size[2], tc.layout,
			lower, upper, matrix, offset)
		lut, err := NewLUT(tc.size[0], tc.size[1], tc.size[2], table,
			WithLayout(tc.layout), WithDomain(lower, upper))
		if err != nil {
			t.Fatalf("NewLUT(%v): %v", tc.size, err)
		}

		rng := rand.New(rand.NewSource(101))
		for i := 0; i < 2000; i++ {
			x := [3]float64{
				lower[0] + rng.Float64()*(upper[0]-lower[0]),
				lower[1] + rng.Float64()*(upper[1]-lower[1]),
				lower[2] + rng.Float64()*(upper[2]-lower[2]),
			}
			want := affineRGB(matrix, offset, x)
			got, err := lut.Sample(x[0], x[1], x[2])
			if err != nil {
				t.Fatalf("Sample(%v): %v", x, err)
			}
			assertRGBClose(t, got, want, 1e-12, "affine recovery")
		}
	}
}

func affineRGB(matrix [3][3]float64, offset, x [3]float64) RGB {
	var out [3]float64
	for row := 0; row < 3; row++ {
		out[row] = offset[row]
		for col := 0; col < 3; col++ {
			out[row] += matrix[row][col] * x[col]
		}
	}
	return RGB{R: out[0], G: out[1], B: out[2]}
}

func assertRGBClose(t *testing.T, got, want RGB, tol float64, msg string) {
	t.Helper()
	if math.Abs(got.R-want.R) > tol || math.Abs(got.G-want.G) > tol || math.Abs(got.B-want.B) > tol {
		t.Fatalf("%s: got (%.15g,%.15g,%.15g), want (%.15g,%.15g,%.15g), tol %g",
			msg, got.R, got.G, got.B, want.R, want.G, want.B, tol)
	}
}

func TestAllCellCornersExact(t *testing.T) {
	const sizeR, sizeG, sizeB = 4, 3, 5
	layout := BlueFastest
	table := randomTable(sizeR, sizeG, sizeB, layout, rand.New(rand.NewSource(202)))
	lut, err := NewLUT(sizeR, sizeG, sizeB, table, WithLayout(layout))
	if err != nil {
		t.Fatal(err)
	}

	for cb := 0; cb < max(1, sizeB-1); cb++ {
		for cg := 0; cg < max(1, sizeG-1); cg++ {
			for cr := 0; cr < max(1, sizeR-1); cr++ {
				for mask := 0; mask < 8; mask++ {
					fr := float64((mask >> 0) & 1)
					fg := float64((mask >> 1) & 1)
					fb := float64((mask >> 2) & 1)
					got, err := lut.SampleCell(cr, cg, cb, fr, fg, fb)
					if err != nil {
						t.Fatalf("SampleCell(%d,%d,%d,%v,%v,%v): %v",
							cr, cg, cb, fr, fg, fb, err)
					}
					want := lut.node(cr+int(fr), cg+int(fg), cb+int(fb))
					if got != want {
						t.Fatalf("cell (%d,%d,%d) corner %d: got %#v, want %#v",
							cr, cg, cb, mask, got, want)
					}
				}
			}
		}
	}
}

func TestSampleHitsEveryGridNodeExactly(t *testing.T) {
	const sizeR, sizeG, sizeB = 7, 4, 6
	layout := RedFastest
	table := randomTable(sizeR, sizeG, sizeB, layout, rand.New(rand.NewSource(606)))
	lut, err := NewLUT(sizeR, sizeG, sizeB, table, WithLayout(layout))
	if err != nil {
		t.Fatal(err)
	}

	for b := 0; b < sizeB; b++ {
		for g := 0; g < sizeG; g++ {
			for r := 0; r < sizeR; r++ {
				got, err := lut.Sample(
					fraction(r, sizeR),
					fraction(g, sizeG),
					fraction(b, sizeB),
				)
				if err != nil {
					t.Fatalf("node (%d,%d,%d): %v", r, g, b, err)
				}
				want := lut.node(r, g, b)
				if got != want {
					t.Fatalf("node (%d,%d,%d): got %#v, want %#v", r, g, b, got, want)
				}
			}
		}
	}
}

func TestCellFaceContinuity(t *testing.T) {
	const sizeR, sizeG, sizeB = 4, 5, 3
	table := randomTable(sizeR, sizeG, sizeB, RedFastest, rand.New(rand.NewSource(303)))
	lut, err := NewLUT(sizeR, sizeG, sizeB, table)
	if err != nil {
		t.Fatal(err)
	}

	// Include equal coordinate pairs so the tie-break rules are exercised on
	// both sides of every shared face.
	coordinates := [][3]float64{
		{0, 0, 0},
		{0, 0.5, 0.5},
		{0.25, 0, 0.25},
		{0.5, 0.5, 0},
		{0.5, 0.5, 0.5},
		{1, 0, 1},
		{1, 1, 1},
		{0.875, 0.125, 0.375},
	}

	contact := func(label string, a, b RGB) {
		t.Helper()
		if math.Abs(a.R-b.R) > 1e-12 || math.Abs(a.G-b.G) > 1e-12 || math.Abs(a.B-b.B) > 1e-12 {
			t.Fatalf("%s: discontinuous across cell face: %#v vs %#v", label, a, b)
		}
	}

	for face := 1; face < sizeR-1; face++ {
		for cg := 0; cg < max(1, sizeG-1); cg++ {
			for cb := 0; cb < max(1, sizeB-1); cb++ {
				for _, q := range coordinates {
					left, err := lut.SampleCell(face-1, cg, cb, 1, q[1], q[2])
					if err != nil {
						t.Fatal(err)
					}
					right, err := lut.SampleCell(face, cg, cb, 0, q[1], q[2])
					if err != nil {
						t.Fatal(err)
					}
					contact("R face", left, right)
				}
			}
		}
	}

	for face := 1; face < sizeG-1; face++ {
		for cr := 0; cr < max(1, sizeR-1); cr++ {
			for cb := 0; cb < max(1, sizeB-1); cb++ {
				for _, q := range coordinates {
					left, err := lut.SampleCell(cr, face-1, cb, q[0], 1, q[2])
					if err != nil {
						t.Fatal(err)
					}
					right, err := lut.SampleCell(cr, face, cb, q[0], 0, q[2])
					if err != nil {
						t.Fatal(err)
					}
					contact("G face", left, right)
				}
			}
		}
	}

	for face := 1; face < sizeB-1; face++ {
		for cr := 0; cr < max(1, sizeR-1); cr++ {
			for cg := 0; cg < max(1, sizeG-1); cg++ {
				for _, q := range coordinates {
					left, err := lut.SampleCell(cr, cg, face-1, q[0], q[1], 1)
					if err != nil {
						t.Fatal(err)
					}
					right, err := lut.SampleCell(cr, cg, face, q[0], q[1], 0)
					if err != nil {
						t.Fatal(err)
					}
					contact("B face", left, right)
				}
			}
		}
	}
}

func TestTetrahedronOrderAndTies(t *testing.T) {
	cases := []struct {
		f    [3]float64
		want [3]int
	}{
		{[3]float64{0.9, 0.5, 0.1}, [3]int{0, 1, 2}},
		{[3]float64{0.9, 0.1, 0.5}, [3]int{0, 2, 1}},
		{[3]float64{0.5, 0.9, 0.1}, [3]int{1, 0, 2}},
		{[3]float64{0.1, 0.9, 0.5}, [3]int{1, 2, 0}},
		{[3]float64{0.5, 0.1, 0.9}, [3]int{2, 0, 1}},
		{[3]float64{0.1, 0.5, 0.9}, [3]int{2, 1, 0}},
		// Equal R/G/B values follow channel order: R, then G, then B.
		{[3]float64{0.5, 0.5, 0.1}, [3]int{0, 1, 2}},
		{[3]float64{0.1, 0.5, 0.5}, [3]int{1, 2, 0}},
		{[3]float64{0.5, 0.1, 0.5}, [3]int{0, 2, 1}},
		{[3]float64{0.5, 0.5, 0.5}, [3]int{0, 1, 2}},
	}
	for _, tc := range cases {
		if got := componentOrder(tc.f); got != tc.want {
			t.Errorf("componentOrder(%v) = %v, want %v", tc.f, got, tc.want)
		}
	}
}

func TestTetrahedralInterpolationIsNotTrilinear(t *testing.T) {
	// The R values are deliberately nonlinear in the node coordinates; G and
	// B do not affect this check. At f=(0.2,0.6,0.9), the tetrahedron
	// containing the point is
	// c000-c001-c011-c111.
	table := []float64{
		0, 0, 0,
		1, 0, 0,
		2, 0, 0,
		3, 0, 0,
		4, 0, 0,
		5, 0, 0,
		6, 0, 0,
		100, 0, 0,
	}
	lut, err := NewLUT(2, 2, 2, table)
	if err != nil {
		t.Fatal(err)
	}
	got, err := lut.Sample(0.2, 0.6, 0.9)
	if err != nil {
		t.Fatal(err)
	}

	// Barycentric tetrahedral weights: 1-b, b-g, g-r, r.
	wantTetraR := 0.1*0 + 0.3*4 + 0.4*6 + 0.2*100
	if math.Abs(got.R-wantTetraR) > 1e-12 {
		t.Fatalf("tetrahedral R = %v, want %v", got.R, wantTetraR)
	}

	// Explicit trilinear result for the same point. A trilinear
	// implementation would pass the affine/corner/face checks above but
	// produce this different value.
	var wantTrilinearR float64
	for b := 0; b < 2; b++ {
		wb := 1 - 0.9
		if b == 1 {
			wb = 0.9
		}
		for g := 0; g < 2; g++ {
			wg := 1 - 0.6
			if g == 1 {
				wg = 0.6
			}
			for r := 0; r < 2; r++ {
				wr := 1 - 0.2
				if r == 1 {
					wr = 0.2
				}
				idx := logicalNodeIndex(RedFastest, 2, 2, 2, r, g, b) * 3
				wantTrilinearR += wr * wg * wb * table[idx]
			}
		}
	}
	if math.Abs(got.R-wantTrilinearR) < 0.02 {
		t.Fatalf("tetrahedral and trilinear results are suspiciously close: %v vs %v",
			got.R, wantTrilinearR)
	}
}

func TestTetrahedralBarycentricWeights(t *testing.T) {
	table := randomTable(2, 2, 2, RedFastest, rand.New(rand.NewSource(707)))
	lut, err := NewLUT(2, 2, 2, table)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(708))
	for i := 0; i < 500; i++ {
		f := [3]float64{rng.Float64(), rng.Float64(), rng.Float64()}
		got, err := lut.SampleCell(0, 0, 0, f[0], f[1], f[2])
		if err != nil {
			t.Fatal(err)
		}

		order := componentOrder(f)
		w0 := 1 - f[order[0]]
		w1 := f[order[0]] - f[order[1]]
		w2 := f[order[1]] - f[order[2]]
		w3 := f[order[2]]
		n0 := lut.node(0, 0, 0)
		n1 := lut.node(axisNode(order[0], 0), axisNode(order[0], 1), axisNode(order[0], 2))
		n2 := lut.node(
			axisNode(order[0], 0)+axisNode(order[1], 0),
			axisNode(order[0], 1)+axisNode(order[1], 1),
			axisNode(order[0], 2)+axisNode(order[1], 2),
		)
		n3 := lut.node(1, 1, 1)
		want := RGB{
			R: w0*n0.R + w1*n1.R + w2*n2.R + w3*n3.R,
			G: w0*n0.G + w1*n1.G + w2*n2.G + w3*n3.G,
			B: w0*n0.B + w1*n1.B + w2*n2.B + w3*n3.B,
		}
		assertRGBClose(t, got, want, 1e-12, "barycentric tetrahedral weights")
	}
}

func axisNode(axis, component int) int {
	if axis == component {
		return 1
	}
	return 0
}

func TestBoundaryClampAndReject(t *testing.T) {
	sizeR, sizeG, sizeB := 2, 3, 4
	table := randomTable(sizeR, sizeG, sizeB, RedFastest, rand.New(rand.NewSource(404)))

	clamped, err := NewLUT(sizeR, sizeG, sizeB, table)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range [][3]float64{
		{-10, 5, 0.5},
		{0.5, -10, 5},
		{5, 0.5, -10},
	} {
		got, err := clamped.Sample(x[0], x[1], x[2])
		if err != nil {
			t.Fatalf("clamped Sample(%v): %v", x, err)
		}
		want, err := clamped.Sample(
			clamp01(x[0]), clamp01(x[1]), clamp01(x[2]))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("clamp Sample(%v): got %#v, want %#v", x, got, want)
		}
	}

	rejected, err := NewLUT(sizeR, sizeG, sizeB, table, Reject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rejected.Sample(1.01, 0.5, 0.5); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("reject Sample error = %v, want ErrOutOfRange", err)
	}
	if _, err := rejected.Sample(0.5, -0.01, 0.5); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("reject Sample error = %v, want ErrOutOfRange", err)
	}
	if _, err := rejected.Sample(math.NaN(), 0.5, 0.5); !errors.Is(err, ErrNonFinite) {
		t.Fatalf("NaN Sample error = %v, want ErrNonFinite", err)
	}
	if _, err := rejected.Sample(0, 0, 0); err != nil {
		t.Fatalf("exact boundary should be accepted: %v", err)
	}
	if _, err := rejected.Sample(1, 1, 1); err != nil {
		t.Fatalf("exact boundary should be accepted: %v", err)
	}
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

func TestConstructorValidationAndTableForms(t *testing.T) {
	validTable := []float64{
		0, 0, 0,
		1, 0, 0,
		0, 1, 0,
		0, 0, 1,
		1, 1, 0,
		1, 0, 1,
		0, 1, 1,
		1, 1, 1,
	}

	if _, err := NewLUT(0, 2, 2, validTable); !errors.Is(err, ErrInvalidLUT) {
		t.Errorf("zero dimension error = %v, want ErrInvalidLUT", err)
	}
	if _, err := NewLUT(2, 2, 2, validTable[:23]); !errors.Is(err, ErrInvalidLUT) {
		t.Errorf("short table error = %v, want ErrInvalidLUT", err)
	}
	if _, err := NewLUT(2, 2, 2, "not a table"); !errors.Is(err, ErrInvalidLUT) {
		t.Errorf("wrong table type error = %v, want ErrInvalidLUT", err)
	}
	bad := append([]float64(nil), validTable...)
	bad[7] = math.NaN()
	if _, err := NewLUT(2, 2, 2, bad); !errors.Is(err, ErrInvalidLUT) {
		t.Errorf("NaN table error = %v, want ErrInvalidLUT", err)
	}
	if _, err := NewLUT(2, 2, 2, validTable, Layout(9)); !errors.Is(err, ErrInvalidLUT) {
		t.Errorf("bad layout error = %v, want ErrInvalidLUT", err)
	}
	if _, err := NewLUT(2, 2, 2, validTable,
		WithDomain([3]float64{1, 0, 0}, [3]float64{1, 1, 1})); !errors.Is(err, ErrInvalidLUT) {
		t.Errorf("bad domain error = %v, want ErrInvalidLUT", err)
	}

	// The table is copied and all supported table forms produce the same LUT.
	source := append([]float64(nil), validTable...)
	lut64, err := NewLUT(2, 2, 2, source)
	if err != nil {
		t.Fatal(err)
	}
	source[0] = 99
	got, err := lut64.Sample(0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.R != 0 {
		t.Fatalf("LUT retained caller table mutation: %#v", got)
	}

	table32 := make([]float32, len(validTable))
	for i, v := range validTable {
		table32[i] = float32(v)
	}
	triples32 := make([][3]float32, 8)
	triples64 := make([][3]float64, 8)
	colors := make([]RGB, 8)
	for i := 0; i < 8; i++ {
		triples32[i] = [3]float32{float32(validTable[i*3]), float32(validTable[i*3+1]), float32(validTable[i*3+2])}
		triples64[i] = [3]float64{validTable[i*3], validTable[i*3+1], validTable[i*3+2]}
		colors[i] = RGB{
			R: validTable[i*3],
			G: validTable[i*3+1],
			B: validTable[i*3+2],
		}
	}
	for name, table := range map[string]any{
		"float32":  table32,
		"[3]f32":   triples32,
		"[3]f64":   triples64,
		"[]RGB":    colors,
		"NewLUT32": table32,
	} {
		var lut *LUT
		var err error
		if name == "NewLUT32" {
			lut, err = NewLUT32(2, 2, 2, table32)
		} else {
			lut, err = NewLUT(2, 2, 2, table)
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := lut.Sample(1, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if got.R != 1 || got.G != 1 || got.B != 1 {
			t.Fatalf("%s corner: %#v", name, got)
		}
	}

	// Compile-time check for the array-dimension constructors.
	for _, ctor := range []func() (*LUT, error){
		func() (*LUT, error) { return NewLUT3D([3]int{2, 2, 2}, validTable) },
		func() (*LUT, error) { return NewGrid([3]int{2, 2, 2}, validTable) },
		func() (*LUT, error) { return New(2, 2, 2, validTable) },
	} {
		lut, err := ctor()
		if err != nil {
			t.Fatal(err)
		}
		if lut.Dimensions() != [3]int{2, 2, 2} {
			t.Fatalf("dimensions = %v", lut.Dimensions())
		}
	}
}

func TestSingleSampleAxesAreConstant(t *testing.T) {
	// One-sample axes are legal and behave as constants along that axis.
	table := []float64{
		0.1, 0.2, 0.3,
		0.4, 0.5, 0.6,
	}
	lut, err := NewLUT(1, 2, 1, table)
	if err != nil {
		t.Fatal(err)
	}
	a, err := lut.Sample(0.2, 0, 0.8)
	if err != nil {
		t.Fatal(err)
	}
	b, err := lut.Sample(0.7, 0, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("single-sample axes not constant: %#v vs %#v", a, b)
	}
	if reflect.DeepEqual(a, RGB{}) {
		t.Fatalf("unexpected zero output: %#v", a)
	}
}
