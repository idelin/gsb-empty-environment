package hashring

import (
	"math"
	"testing"
)

var testNodes = []string{
	"node-00", "node-01", "node-02", "node-03", "node-04",
	"node-05", "node-06", "node-07", "node-08", "node-09",
}

const (
	testVNodes  = 160
	testSamples = 200000
	testSeed    = 42
)

func TestMurmur3KnownVectors(t *testing.T) {
	// Reference values from the MurmurHash3 x86_32 spec (seed 0).
	cases := []struct {
		in   string
		want uint32
	}{
		{"", 0},
		{"hello", 0x248bfa47},
		{"hello, world", 0x149bbb7f},
	}
	for _, c := range cases {
		if got := Murmur3Hash([]byte(c.in)); got != c.want {
			t.Errorf("Murmur3Hash(%q) = %#08x, want %#08x", c.in, got, c.want)
		}
	}
}

func TestFNV1aKnownVectors(t *testing.T) {
	// Reference values from the FNV spec.
	if got := FNV1aHash([]byte("a")); got != 0xe40c292c {
		t.Errorf("FNV1aHash(\"a\") = %#08x, want 0xe40c292c", got)
	}
	if got := FNV1aHash([]byte("foobar")); got != 0xbf9cf968 {
		t.Errorf("FNV1aHash(\"foobar\") = %#08x, want 0xbf9cf968", got)
	}
}

func TestRingCountsSumToSamples(t *testing.T) {
	for _, nh := range DefaultHashes() {
		ring := NewRing(testNodes, testVNodes, nh.Func)
		counts := CountKeys(ring, nh.Func, 10000, testSeed, RandomKeys)
		sum := 0
		for _, c := range counts {
			if c < 0 {
				t.Fatalf("%s: negative count", nh.Name)
			}
			sum += c
		}
		if sum != 10000 {
			t.Errorf("%s: counts sum to %d, want 10000", nh.Name, sum)
		}
	}
}

func TestRingSharesSumToOne(t *testing.T) {
	for _, nh := range DefaultHashes() {
		ring := NewRing(testNodes, testVNodes, nh.Func)
		total := 0.0
		for _, s := range ring.Shares() {
			total += s
		}
		if math.Abs(total-1) > 1e-9 {
			t.Errorf("%s: shares sum to %f, want 1", nh.Name, total)
		}
	}
}

// TestHighQualityHashesAreUniform verifies MD5 and MurmurHash3 pass the
// chi-square uniformity test (against ring arc shares) for random keys.
func TestHighQualityHashesAreUniform(t *testing.T) {
	for _, nh := range DefaultHashes()[:2] { // MD5, MurmurHash3
		rep := Analyze(nh.Name, nh.Func, testNodes, testVNodes, testSamples, testSeed, RandomKeys)
		t.Logf("%-20s stddev=%.2f cv=%.3f%% max/min=%.4f chi2=%.2f p=%.4f",
			rep.HashName, rep.StdDev, rep.CV*100, rep.MaxMinRatio, rep.Chi2, rep.PValue)
		if !rep.Uniform {
			t.Errorf("%s: rejected uniformity (p=%.4f)", nh.Name, rep.PValue)
		}
		if rep.MaxMinRatio > 1.5 {
			t.Errorf("%s: max/min ratio %.4f too high", nh.Name, rep.MaxMinRatio)
		}
	}
}

// TestFNVSequentialKeyDeficiency exposes FNV-1a's weak avalanche with
// realistic sequential keys on the ring.
func TestFNVSequentialKeyDeficiency(t *testing.T) {
	reps := map[string]Report{}
	for _, nh := range DefaultHashes() {
		reps[nh.Name] = Analyze(nh.Name, nh.Func, testNodes, testVNodes, testSamples, testSeed, SequentialKeys)
		r := reps[nh.Name]
		t.Logf("%-20s chi2=%10.2f p=%.3e max/min=%.4f", nh.Name, r.Chi2, r.PValue, r.MaxMinRatio)
	}
	fnv, mur := reps["FNV-1a-32"], reps["MurmurHash3-x86_32"]
	if fnv.Uniform {
		t.Errorf("FNV sequential: expected non-uniform verdict, got p=%.3e", fnv.PValue)
	}
	if fnv.Chi2 < 5*mur.Chi2 {
		t.Errorf("FNV sequential chi2 (%.1f) not 5x Murmur's (%.1f)", fnv.Chi2, mur.Chi2)
	}
}

// TestFNVLowBitWidthAvalancheDeficiency is the core diagnostic: the
// bit-width deviation curve must expose FNV-1a's weak low-bit avalanche,
// while MD5 and MurmurHash3 stay flat near zero at every width.
func TestFNVLowBitWidthAvalancheDeficiency(t *testing.T) {
	widths := []int{4, 8, 16, 32}
	curves := map[string][]WidthPoint{}
	for _, nh := range DefaultHashes() {
		curves[nh.Name] = WidthCurve(nh.Func, 5000, widths, testSeed)
		for _, wp := range curves[nh.Name] {
			t.Logf("%-20s width=%2d meanDev=%.4f maxDev=%.4f badBits=%d",
				nh.Name, wp.Width, wp.MeanDev, wp.MaxDev, wp.BadBits)
		}
	}
	fnv4 := curves["FNV-1a-32"][0] // width 4
	if fnv4.MaxDev < 0.2 {
		t.Errorf("FNV at 4 bits: expected strong avalanche deficiency, got maxDev=%.4f", fnv4.MaxDev)
	}
	for _, name := range []string{"MD5-32", "MurmurHash3-x86_32"} {
		for _, wp := range curves[name] {
			if wp.MaxDev > 0.1 {
				t.Errorf("%s at width %d: maxDev=%.4f, want <= 0.1", name, wp.Width, wp.MaxDev)
			}
		}
	}
	// FNV's deficiency must be worst at small widths and fade at 32 bits.
	fnv32 := curves["FNV-1a-32"][3]
	if fnv4.MeanDev < 3*fnv32.MeanDev {
		t.Errorf("FNV meanDev at 4 bits (%.4f) not 3x its 32-bit value (%.4f)",
			fnv4.MeanDev, fnv32.MeanDev)
	}
}

// TestAvalanche quantifies the strict avalanche criterion per hash.
func TestAvalanche(t *testing.T) {
	reps := map[string]AvalancheReport{}
	for _, nh := range DefaultHashes() {
		reps[nh.Name] = Avalanche(nh.Func, 5000, testSeed)
		av := reps[nh.Name]
		t.Logf("%-20s meanDev=%.4f maxDev=%.4f (bit %d) badBits=%d/32",
			nh.Name, av.MeanDev, av.MaxDev, av.WorstBit, av.BadBits)
	}
	if av := reps["MD5-32"]; av.BadBits > 1 {
		t.Errorf("MD5 avalanche: %d bad bits, want <= 1", av.BadBits)
	}
	if av := reps["MurmurHash3-x86_32"]; av.BadBits > 1 {
		t.Errorf("Murmur avalanche: %d bad bits, want <= 1", av.BadBits)
	}
	fnv := reps["FNV-1a-32"]
	if fnv.BadBits < 4 {
		t.Errorf("FNV avalanche: only %d bad bits, expected >= 4", fnv.BadBits)
	}
	// FNV's low output bits are the weakest (multiply propagates upward only).
	if fnv.WorstBit > 15 {
		t.Errorf("FNV worst avalanche bit = %d, expected in low half (<=15)", fnv.WorstBit)
	}
}

func TestChiSquarePValueKnownValues(t *testing.T) {
	// chi2=16.919 with df=9 is the classic alpha=0.05 critical value.
	if p := ChiSquarePValue(16.919, 9); math.Abs(p-0.05) > 1e-3 {
		t.Errorf("ChiSquarePValue(16.919, 9) = %.5f, want ~0.05", p)
	}
	if p := ChiSquarePValue(0, 9); p != 1 {
		t.Errorf("ChiSquarePValue(0, 9) = %v, want 1", p)
	}
}
