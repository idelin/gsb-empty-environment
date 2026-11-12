package netsec

import (
	"math/big"
	"math/rand"
	"testing"
)

// universe enumerates every well-formed descriptor for a W-bit domain:
// base in [0,M), scale in [0,M), offset in [-M,M), length in [1,2D+1].
func universe(w int) []Descriptor {
	m := int64(1) << w
	d := m / 2 // smallest power-of-two D below M
	maxLen := 2*d + 1
	descs := make([]Descriptor, 0)
	idx := 0
	for base := int64(0); base < m; base++ {
		for scale := int64(0); scale < m; scale++ {
			for offset := -m; offset < m; offset++ {
				for length := int64(1); length <= maxLen; length++ {
					descs = append(descs, Descriptor{
						Name:   nameAt(idx),
						Base:   big.NewInt(base),
						Offset: big.NewInt(offset),
						Scale:  big.NewInt(scale),
						Length: length,
					})
					idx++
				}
			}
		}
	}
	return descs
}

func nameAt(i int) string {
	return "d" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func protectedFor(d int64) []Region {
	if d >= 4 {
		return []Region{{Name: "boot", Start: 0, End: 2}, {Name: "cfg", Start: uint64(d - 2), End: uint64(d)}}
	}
	return []Region{{Name: "boot", Start: 0, End: 1}}
}

func oracleFrom(w int, d int64) oracleD {
	return oracleD{
		bits: w,
		m:    new(big.Int).Lsh(big.NewInt(1), uint(w)),
		d:    big.NewInt(d),
	}
}

// checkSingle compares one descriptor's full report against the byte oracle.
func checkSingle(t *testing.T, w int, d int64, desc Descriptor, mode Mode) {
	t.Helper()
	o := oracleFrom(w, d)
	c := newTestCompiler(t, uint64(d), w, mode)
	report, err := c.Compile([]Descriptor{desc}, protectedFor(d))

	start, end, _, _, underflow, repWrap, domainWrap, nonInjective := oracleFlags(&desc, o)
	legal := oracleStrictLegal(&desc, o)

	if mode == Strict {
		if legal {
			if err != nil {
				t.Fatalf("w=%d d=%d %+v: unexpected error %v", w, d, desc, err)
			}
			res := report.Results[0]
			if res.Underflow || res.RepresentationWrap || res.DomainWrap || res.NonInjective {
				t.Fatalf("w=%d %+v: strict result must carry no wrap flags: %+v", w, desc, res)
			}
			if res.Start.Cmp(start) != 0 || res.End.Cmp(end) != 0 {
				t.Fatalf("w=%d %+v: exact values mismatch", w, desc)
			}
		} else {
			if err == nil {
				t.Fatalf("w=%d %+v: strict mode must reject wrapping descriptor", w, desc)
			}
			return
		}
	} else {
		if err != nil {
			t.Fatalf("w=%d %+v: modular error %v", w, desc, err)
		}
		res := report.Results[0]
		if res.Underflow != underflow || res.RepresentationWrap != repWrap ||
			res.DomainWrap != domainWrap || res.NonInjective != nonInjective {
			t.Fatalf("w=%d %+v: flags got {u=%v r=%v dom=%v ni=%v} want {u=%v r=%v dom=%v ni=%v}",
				w, desc, res.Underflow, res.RepresentationWrap, res.DomainWrap, res.NonInjective,
				underflow, repWrap, domainWrap, nonInjective)
		}
		if res.Start.Cmp(start) != 0 || res.End.Cmp(end) != 0 {
			t.Fatalf("w=%d %+v: exact values mismatch", w, desc)
		}
	}

	wantMask, wantChains := oracleMask([]Descriptor{desc}, o)
	gotChains := maskFromSegments(report.WriteSet, int(d))
	if !chainsEqual(gotChains, wantChains) {
		t.Fatalf("w=%d d=%d mode=%d %+v: write set mismatch\n got=%v\nwant=%v\nmask=%v",
			w, d, mode, desc, gotChains, wantChains, wantMask)
	}
	checkViolations(t, d, report, wantChains)
}

func checkViolations(t *testing.T, d int64, report *Report, wantChains [][]string) {
	t.Helper()
	var want []Violation
	for _, pr := range protectedFor(d) {
		var cur *Violation
		for p := pr.Start; p < pr.End; p++ {
			chain := wantChains[p]
			if len(chain) == 0 {
				cur = nil
				continue
			}
			if cur != nil && cur.End == p && equalChain(cur.Descriptors, chain) {
				cur.End = p + 1
				continue
			}
			v := Violation{Region: pr.Name, Start: p, End: p + 1, Descriptors: append([]string(nil), chain...)}
			want = append(want, v)
			cur = &want[len(want)-1]
		}
	}
	if len(want) != len(report.Violations) {
		t.Fatalf("violations got %+v want %+v", report.Violations, want)
	}
	for i := range want {
		g := report.Violations[i]
		if g.Region != want[i].Region || g.Start != want[i].Start || g.End != want[i].End ||
			!equalChain(g.Descriptors, want[i].Descriptors) {
			t.Fatalf("violation %d got %+v want %+v", i, g, want[i])
		}
	}
}

// TestExhaustiveW2Single enumerates the whole W=2 universe in both modes.
func TestExhaustiveW2Single(t *testing.T) {
	if got := len(universe(2)); got != 640 {
		t.Fatalf("W2 universe size = %d, want 640", got)
	}
	for _, mode := range []Mode{Strict, ModularDecode} {
		for _, d := range []int64{1, 2, 4} {
			for _, desc := range universe(2) {
				checkSingle(t, 2, d, desc, mode)
			}
		}
	}
}

// TestExhaustiveW3Single covers every W=3 descriptor tuple (30k per mode).
func TestExhaustiveW3Single(t *testing.T) {
	descs := universe(3)
	if len(descs) != 9_216 {
		t.Fatalf("W3 universe size = %d", len(descs))
	}
	for _, mode := range []Mode{Strict, ModularDecode} {
		for _, d := range []int64{1, 2, 4, 8} {
			for _, desc := range descs {
				checkSingle(t, 3, d, desc, mode)
			}
		}
	}
}

// TestSampledW4Single samples the W=4 universe where full enumeration is too
// large, still exercising every wrap combination.
func TestSampledW4Single(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for _, mode := range []Mode{Strict, ModularDecode} {
		for _, d := range []int64{2, 8, 16} {
			for n := 0; n < 4000; n++ {
				desc := sampleDescriptor(4, rng)
				checkSingle(t, 4, d, desc, mode)
			}
		}
	}
}

func sampleDescriptor(w int, rng *rand.Rand) Descriptor {
	m := int64(1) << w
	d := m / 2
	return Descriptor{
		Name:   "s",
		Base:   big.NewInt(rng.Int63n(m)),
		Offset: big.NewInt(rng.Int63n(2*m) - m),
		Scale:  big.NewInt(rng.Int63n(m)),
		Length: rng.Int63n(2*d+1) + 1,
	}
}
