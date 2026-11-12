package taskpkg

import (
	"math"
	"testing"
)

const testEps = 1e-10

func approx(t *testing.T, got, want float64, tol float64, name string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %.12g, want %.12g (tol %.1e)", name, got, want, tol)
	}
}

func approxRGB(t *testing.T, got, want RGB, tol float64, name string) {
	t.Helper()
	for c := 0; c < 3; c++ {
		if math.Abs(got[c]-want[c]) > tol {
			t.Fatalf("%s[%d] = %.12g, want %.12g (tol %.1e)", name, c, got[c], want[c], tol)
		}
	}
}

func TestAnalyticSegmentAbsorbing(t *testing.T) {
	sigma := 0.7
	em := RGB{0.3, 0.6, 0.9}
	h := 2.5
	seg := analyticSegment(sigma, em, h)

	wantT := math.Exp(-sigma * h)
	wantK := (1 - wantT) / sigma
	approx(t, seg.T, wantT, testEps, "T")
	approxRGB(t, seg.C, RGB{em[0] * wantK, em[1] * wantK, em[2] * wantK}, testEps, "C")
}

func TestAnalyticSegmentZeroAbsorptionLimit(t *testing.T) {
	// sigma -> 0 limit: T = 1 and C = em*h, with no 0/0 instability.
	em := RGB{0.4, -0.2, 1.5}
	h := 3.0
	seg := analyticSegment(0, em, h)
	approx(t, seg.T, 1, testEps, "T at zero sigma")
	approxRGB(t, seg.C, RGB{em[0] * h, em[1] * h, em[2] * h}, testEps, "C at zero sigma")

	// Tiny optical depth must stay accurate: the generic path uses Expm1 and
	// must agree with the limit, avoiding a spurious flicker for short steps.
	tiny := analyticSegment(1e-14, em, h)
	approx(t, tiny.T, 1, 1e-12, "T at tiny sigma")
	approxRGB(t, tiny.C, seg.C, 1e-10, "C at tiny sigma")
}

func TestComposeMatchesContinuousSolution(t *testing.T) {
	sigma := 1.3
	em := RGB{0.2, 0.5, 0.8}
	h := 1.7
	full := analyticSegment(sigma, em, h)
	halved := compose(
		analyticSegment(sigma, em, h/2),
		analyticSegment(sigma, em, h/2),
	)
	approx(t, halved.T, full.T, testEps, "T")
	approxRGB(t, halved.C, full.C, testEps, "C")
}
