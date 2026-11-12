package netsec

import (
	"math/big"
	"math/rand"
	"testing"
)

// TestExhaustiveW2Pairs enumerates every descriptor pair over the W=2
// universe, proving alias normalization and cross-descriptor provenance.
func TestExhaustiveW2Pairs(t *testing.T) {
	descs := universe(2)
	for _, mode := range []Mode{Strict, ModularDecode} {
		for _, d := range []int64{1, 2, 4} {
			for i := range descs {
				for j := i; j < len(descs); j++ {
					a := descs[i]
					b := descs[j]
					a.Name = "a"
					b.Name = "b"
					checkPair(t, 2, d, a, b, mode)
				}
			}
		}
	}
}

// TestSampledW4Pairs samples overlapping and aliasing pairs on a larger
// reduced domain where pair enumeration is infeasible.
func TestSampledW4Pairs(t *testing.T) {
	rng := rand.New(rand.NewSource(0x4e455453))
	for _, mode := range []Mode{Strict, ModularDecode} {
		for _, d := range []int64{2, 8, 16} {
			for n := 0; n < 1500; n++ {
				a := sampleDescriptor(4, rng)
				b := sampleDescriptor(4, rng)
				a.Name = "a"
				b.Name = "b"
				checkPair(t, 4, d, a, b, mode)
			}
		}
	}
}

func checkPair(t *testing.T, w int, d int64, a, b Descriptor, mode Mode) {
	t.Helper()
	o := oracleFrom(w, d)
	c := newTestCompiler(t, uint64(d), w, mode)
	pair := []Descriptor{a, b}
	report, err := c.Compile(pair, protectedFor(d))

	legalA := oracleStrictLegal(&a, o)
	legalB := oracleStrictLegal(&b, o)
	if mode == Strict {
		if legalA && legalB {
			if err != nil {
				t.Fatalf("w=%d d=%d pair %+v %+v: unexpected error %v", w, d, a, b, err)
			}
		} else {
			if err == nil {
				t.Fatalf("w=%d pair %+v %+v: strict mode must reject wrapping descriptor", w, a, b)
			}
			return
		}
	} else if err != nil {
		t.Fatalf("w=%d modular pair error %v", w, err)
	}

	_, wantChains := oracleMask(pair, o)
	gotChains := maskFromSegments(report.WriteSet, int(d))
	if !chainsEqual(gotChains, wantChains) {
		t.Fatalf("w=%d d=%d mode=%d pair %+v %+v: alias write set mismatch\n got=%v\nwant=%v",
			w, d, mode, a, b, gotChains, wantChains)
	}

	// Non-injective cross-descriptor mapping must be reflected in chains
	// that contain both names on at least one physical byte.
	aliased := false
	for _, chain := range wantChains {
		if len(chain) == 2 {
			aliased = true
		}
	}
	gotAlias := false
	for _, seg := range report.WriteSet {
		if len(seg.Descriptors) == 2 {
			gotAlias = true
		}
	}
	if aliased != gotAlias {
		t.Fatalf("w=%d pair %+v %+v: alias detection got %v want %v", w, a, b, gotAlias, aliased)
	}

	checkViolations(t, d, report, wantChains)
}

// TestNonInjectiveLongRun forces a single descriptor longer than the image:
// every physical byte is covered and the result must flag NonInjective.
func TestNonInjectiveLongRun(t *testing.T) {
	const w = 3
	const d int64 = 4
	desc := Descriptor{
		Name:   "long",
		Base:   big.NewInt(1),
		Offset: big.NewInt(0),
		Scale:  big.NewInt(0),
		Length: d + 3,
	}
	c := newTestCompiler(t, uint64(d), w, ModularDecode)
	report, err := c.Compile([]Descriptor{desc}, protectedFor(d))
	if err != nil {
		t.Fatal(err)
	}
	if !report.Results[0].NonInjective {
		t.Fatal("long run must be flagged non-injective")
	}
	if len(report.WriteSet) != 1 || report.WriteSet[0].Start != 0 || report.WriteSet[0].End != uint64(d) {
		t.Fatalf("long run must cover whole image, got %+v", report.WriteSet)
	}
}
