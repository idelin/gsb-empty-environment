package taskpkg

import "testing"

// uniformGrid builds an nx*ny*nz node grid with constant node coefficients.
func uniformGrid(xs, ys, zs []float64, sigma float64, em RGB) *Grid {
	n := len(xs) * len(ys) * len(zs)
	sigmas := make([]float64, n)
	ems := make([]RGB, n)
	for i := range sigmas {
		sigmas[i] = sigma
		ems[i] = em
	}
	g, err := NewGrid(xs, ys, zs, sigmas, ems)
	if err != nil {
		panic(err)
	}
	return g
}

func linNodes(a, b float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = a + (b-a)*float64(i)/float64(n-1)
	}
	return out
}

func TestTrilinearInterpolation(t *testing.T) {
	xs := []float64{0, 1}
	ys := []float64{0, 2}
	zs := []float64{0, 4}
	nx, ny, nz := 2, 2, 2
	sigmas := make([]float64, nx*ny*nz)
	ems := make([]RGB, nx*ny*nz)
	// Corner values encode their own index so interpolation is checkable.
	for k := 0; k < nz; k++ {
		for j := 0; j < ny; j++ {
			for i := 0; i < nx; i++ {
				idx := (k*ny+j)*nx + i
				sigmas[idx] = float64(i) + 2*float64(j) + 4*float64(k)
				ems[idx] = RGB{float64(i), float64(j), float64(k)}
			}
		}
	}
	g, err := NewGrid(xs, ys, zs, sigmas, ems)
	if err != nil {
		t.Fatal(err)
	}

	// Point at parametric cell coordinates (0.25, 0.5, 0.75).
	p := Vec3{0.25, 1.0, 3.0}
	fx, fy, fz := 0.25, 0.5, 0.75
	wantSigma := fx + 2*fy + 4*fz
	got := g.CoeffAt(p)
	approx(t, got.Sigma, wantSigma, testEps, "sigma")
	approxRGB(t, got.Em, RGB{fx, fy, fz}, testEps, "em")

	// Node values must be reproduced exactly at the corners.
	corner := g.CoeffAt(Vec3{1, 2, 4})
	approx(t, corner.Sigma, 7, testEps, "corner sigma")
	approxRGB(t, corner.Em, RGB{1, 1, 1}, testEps, "corner em")
}

func TestLocateBoundaryAndClamp(t *testing.T) {
	xs := []float64{0, 1, 3, 10}
	// Exact interior node returns the high cell with frac 0; looking it up
	// from either adjacent cell yields identical trilinear node values.
	idx, frac := locateAxis(xs, 3)
	if idx != 2 || frac != 0 {
		t.Fatalf("interior node: idx=%d frac=%v", idx, frac)
	}
	idx, frac = locateAxis(xs, 0)
	if idx != 0 || frac != 0 {
		t.Fatalf("low node: idx=%d frac=%v", idx, frac)
	}
	idx, frac = locateAxis(xs, 10)
	if idx != 2 || frac != 1 {
		t.Fatalf("high node: idx=%d frac=%v", idx, frac)
	}
	idx, frac = locateAxis(xs, -5)
	if idx != 0 || frac != 0 {
		t.Fatalf("below range: idx=%d frac=%v", idx, frac)
	}
	idx, frac = locateAxis(xs, 20)
	if idx != 2 || frac != 1 {
		t.Fatalf("above range: idx=%d frac=%v", idx, frac)
	}
}

func TestNewGridRejectsBadInput(t *testing.T) {
	good := []float64{0, 1}
	if _, err := NewGrid([]float64{1, 1}, good, good,
		make([]float64, 8), make([]RGB, 8)); err == nil {
		t.Fatal("expected error for non-increasing coordinates")
	}
	sigmas := []float64{-1, 0, 0, 0, 0, 0, 0, 0}
	if _, err := NewGrid(good, good, good, sigmas, make([]RGB, 8)); err == nil {
		t.Fatal("expected error for negative absorption")
	}
	if _, err := NewGrid(good, good, good, make([]float64, 4), make([]RGB, 8)); err == nil {
		t.Fatal("expected error for mismatched slice lengths")
	}
}
