package analyze

import (
	"math"
	"testing"

	"hashring-analyzer/hashfn"
	"hashring-analyzer/ring"
)

func TestChiSquarePValueKnownValues(t *testing.T) {
	// Reference critical values of the chi-square distribution.
	cases := []struct {
		df   int
		x    float64
		want float64
	}{
		{1, 3.8414588, 0.05},
		{1, 6.6348966, 0.01},
		{2, 5.9914645, 0.05},
		{9, 16.918978, 0.05},
		{9, 21.665994, 0.01},
		{30, 43.772972, 0.05},
	}
	for _, c := range cases {
		got := ChiSquarePValue(c.df, c.x)
		if math.Abs(got-c.want) > 1e-4 {
			t.Errorf("ChiSquarePValue(%d, %f) = %f, want %f", c.df, c.x, got, c.want)
		}
	}
}

func TestChiSquareCriticalRoundTrip(t *testing.T) {
	for _, df := range []int{1, 5, 9, 49} {
		crit := ChiSquareCritical(df, 0.01)
		if p := ChiSquarePValue(df, crit); math.Abs(p-0.01) > 1e-6 {
			t.Errorf("df=%d: critical value %f gives p=%f, want 0.01", df, crit, p)
		}
	}
}

func TestGammaPMatchesClosedForm(t *testing.T) {
	// P(1, x) = 1 - e^-x.
	for _, x := range []float64{0.1, 1, 2.5, 10} {
		want := 1 - math.Exp(-x)
		if got := gammaP(1, x); math.Abs(got-want) > 1e-12 {
			t.Errorf("gammaP(1, %f) = %f, want %f", x, got, want)
		}
	}
	// P(2, x) = 1 - e^-x (1 + x).
	for _, x := range []float64{0.5, 3, 8} {
		want := 1 - math.Exp(-x)*(1+x)
		if got := gammaP(2, x); math.Abs(got-want) > 1e-12 {
			t.Errorf("gammaP(2, %f) = %f, want %f", x, got, want)
		}
	}
}

func TestLoadStatsArithmetic(t *testing.T) {
	counts := []uint64{90, 100, 110}
	expected := []float64{100, 100, 100}
	st := NewLoadStats(300, counts, expected)
	if st.Mean != 100 {
		t.Errorf("Mean = %f, want 100", st.Mean)
	}
	wantStd := math.Sqrt((100.0 + 0 + 100.0) / 3)
	if math.Abs(st.StdDev-wantStd) > 1e-9 {
		t.Errorf("StdDev = %f, want %f", st.StdDev, wantStd)
	}
	if math.Abs(st.MaxMinRatio-110.0/90.0) > 1e-9 {
		t.Errorf("MaxMinRatio = %f, want %f", st.MaxMinRatio, 110.0/90.0)
	}
	// chi2 = (100/100) + 0 + (100/100) = 2
	if math.Abs(st.ChiSquare-2) > 1e-9 {
		t.Errorf("ChiSquare = %f, want 2", st.ChiSquare)
	}
	if st.DF != 2 {
		t.Errorf("DF = %d, want 2", st.DF)
	}
	if !st.Pass(0.01) {
		t.Errorf("chi2=2 with df=2 should pass at alpha=0.01 (p=%f)", st.PValue)
	}
}

func TestUniformHashPassesChiSquare(t *testing.T) {
	// MD5 and MurmurHash3 must be consistent with the ring's arc
	// proportions on a narrow (16-bit) ring with random keys.
	hashes := map[string]hashfn.Hash64{
		"md5":    hashfn.MD5Hash,
		"murmur": hashfn.Murmur3Hash,
	}
	nodes := []string{"n0", "n1", "n2", "n3", "n4", "n5", "n6", "n7"}
	for name, h := range hashes {
		r := ring.New(h, nodes, 100, 16)
		st := Run(r, RandomKeys(1), 16, 200000)
		if !st.Pass(0.001) {
			t.Errorf("%s: chi-square rejects uniform hash at 16 bits: chi2=%.2f df=%d p=%g",
				name, st.ChiSquare, st.DF, st.PValue)
		}
	}
}

func TestFNVSequentialKeysFailChiSquare(t *testing.T) {
	// The core defect detection: FNV-1a maps sequential counter keys
	// non-uniformly onto a truncated ring, while MD5 stays uniform.
	nodes := []string{"n0", "n1", "n2", "n3", "n4", "n5", "n6", "n7"}
	rFNV := ring.New(hashfn.FNV1aHash, nodes, 100, 16)
	stFNV := Run(rFNV, SequentialKeys, 8, 200000)
	if stFNV.Pass(0.001) {
		t.Errorf("FNV-1a defect not detected at 16 bits: chi2=%.2f p=%g", stFNV.ChiSquare, stFNV.PValue)
	}
	rMD5 := ring.New(hashfn.MD5Hash, nodes, 100, 16)
	stMD5 := Run(rMD5, SequentialKeys, 8, 200000)
	if !stMD5.Pass(0.001) {
		t.Errorf("MD5 should pass with sequential keys: chi2=%.2f p=%g", stMD5.ChiSquare, stMD5.PValue)
	}
	if stFNV.ChiSquare < 100*stMD5.ChiSquare {
		t.Errorf("expected FNV chi2 (%.1f) to dwarf MD5 chi2 (%.1f)", stFNV.ChiSquare, stMD5.ChiSquare)
	}
}

func TestWidthCurveIsolatesFNVDefect(t *testing.T) {
	nodes := []string{"n0", "n1", "n2", "n3", "n4", "n5", "n6", "n7"}
	widths := []uint{16, 32, 64}
	fnvCurve := WidthCurve(hashfn.FNV1aHash, nodes, 100, widths, SequentialKeys, 8, 100000)
	md5Curve := WidthCurve(hashfn.MD5Hash, nodes, 100, widths, SequentialKeys, 8, 100000)

	fnvFails := FailingWidths(fnvCurve, 0.01)
	if len(fnvFails) == 0 {
		t.Fatal("FNV-1a should fail at some truncated ring width")
	}
	if md5Fails := FailingWidths(md5Curve, 0.01); len(md5Fails) != 0 {
		t.Fatalf("MD5 should pass at every width, failed at %v", md5Fails)
	}
	// The variance curve must be monotone in spirit: the narrowest ring
	// shows a (much) larger reduced chi-square than MD5 at the same width.
	if fnvCurve[0].Stats.ReducedChiSquare() < 10*md5Curve[0].Stats.ReducedChiSquare() {
		t.Errorf("FNV reduced chi2 %.1f not clearly separated from MD5 %.1f at width 16",
			fnvCurve[0].Stats.ReducedChiSquare(), md5Curve[0].Stats.ReducedChiSquare())
	}
}

func TestFNVLayoutImbalanceWithRandomKeys(t *testing.T) {
	// Even with perfectly uniform random keys, FNV-1a clusters its
	// virtual-node positions (they are hashed from sequential vnode
	// labels), so the ring layout itself is heavily imbalanced.
	nodes := []string{"n0", "n1", "n2", "n3", "n4", "n5", "n6", "n7"}
	rFNV := ring.New(hashfn.FNV1aHash, nodes, 100, 64)
	stFNV := Run(rFNV, RandomKeys(2), 16, 200000)
	rMD5 := ring.New(hashfn.MD5Hash, nodes, 100, 64)
	stMD5 := Run(rMD5, RandomKeys(2), 16, 200000)
	if stMD5.MaxMinRatio > 2.0 {
		t.Errorf("MD5 max/min ratio = %.2f, want <= 2.0", stMD5.MaxMinRatio)
	}
	if stFNV.MaxMinRatio < 2*stMD5.MaxMinRatio {
		t.Errorf("FNV max/min ratio = %.2f, want >= 2x MD5's %.2f",
			stFNV.MaxMinRatio, stMD5.MaxMinRatio)
	}
}

func TestAvalancheDetectsFNVHighBitDeficiency(t *testing.T) {
	const (
		keyLen  = 8
		samples = 2000
	)
	av := AvalancheTest(hashfn.FNV1aHash, keyLen, samples, 99)
	def := DeficientOutputBitsProbe(av, keyLen-1, 0.25)
	if len(def) == 0 {
		t.Fatal("FNV-1a: expected deficient output bits for last-byte flips")
	}
	// The top output byte must be unreachable from last-byte entropy.
	topMean := av.MeanProb(keyLen-1, 56, 63)
	if topMean > 0.05 {
		t.Errorf("FNV-1a top output byte flip probability %.3f, want ~0", topMean)
	}

	// MD5 and MurmurHash3: every output bit must sit near 0.5 no matter
	// which input byte is flipped.
	for name, h := range map[string]hashfn.Hash64{
		"md5":    hashfn.MD5Hash,
		"murmur": hashfn.Murmur3Hash,
	} {
		av := AvalancheTest(h, keyLen, samples, 99)
		inBit, outBit, dev := av.MaxDeviation()
		if dev > 0.15 {
			t.Errorf("%s: max avalanche deviation %.3f at in=%d out=%d, want <= 0.15",
				name, dev, inBit, outBit)
		}
		if d := DeficientOutputBitsProbe(av, keyLen-1, 0.25); len(d) != 0 {
			t.Errorf("%s: unexpected deficient output bits %v", name, d)
		}
	}
}

// DeficientOutputBitsProbe adapts the method for older Go test style.
func DeficientOutputBitsProbe(a *Avalanche, inByte int, threshold float64) []int {
	return a.DeficientOutputBits(inByte, threshold)
}
