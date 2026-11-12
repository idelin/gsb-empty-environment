package netsec

import (
	"math/big"
	"testing"
)

// oracleD is a reduced firmware address domain used by the exhaustive tests:
// M=2^bits is the integer representation modulus, D is the physical image
// size (D divides M in modular mode).
type oracleD struct {
	bits int
	m    *big.Int
	d    *big.Int
}

// exactStart mirrors the package arithmetic independently: base+offset*scale.
func exactStart(desc *Descriptor) *big.Int {
	return new(big.Int).Add(desc.Base, new(big.Int).Mul(desc.Offset, desc.Scale))
}

// oracleFlags recomputes every wrap flag directly from first principles.
func oracleFlags(desc *Descriptor, o oracleD) (start, end, physStart, physEnd *big.Int, underflow, repWrap, domainWrap, nonInjective bool) {
	start = exactStart(desc)
	end = new(big.Int).Add(start, big.NewInt(desc.Length))
	length := big.NewInt(desc.Length)

	underflow = start.Sign() < 0
	repWrap = !underflow && (start.Cmp(o.m) >= 0 || end.Cmp(o.m) > 0)

	physStart = new(big.Int).Mod(new(big.Int).Mod(start, o.m), o.d)
	physEnd = new(big.Int).Add(physStart, length)
	domainWrap = start.Sign() < 0 || start.Cmp(o.d) >= 0 || physEnd.Cmp(o.d) > 0
	nonInjective = length.Cmp(o.d) > 0
	return
}

// oracleStrictLegal reports whether strict mode must accept the descriptor.
func oracleStrictLegal(desc *Descriptor, o oracleD) bool {
	start := exactStart(desc)
	end := new(big.Int).Add(start, big.NewInt(desc.Length))
	return start.Sign() >= 0 && start.Cmp(o.d) < 0 && end.Cmp(o.d) <= 0
}

// oracleMask builds the physical write mask and per-byte descriptor chains by
// simulating every written byte through both moduli one byte at a time.
func oracleMask(descs []Descriptor, o oracleD) ([]bool, [][]string) {
	size := int(o.d.Int64())
	mask := make([]bool, size)
	chains := make([][]string, size)
	for idx := range descs {
		desc := &descs[idx]
		name := desc.Name
		start := exactStart(desc)
		for k := int64(0); k < desc.Length; k++ {
			logical := new(big.Int).Add(start, big.NewInt(k))
			rep := new(big.Int).Mod(logical, o.m)
			phys := new(big.Int).Mod(rep, o.d).Int64()
			mask[phys] = true
			chains[phys] = appendUnique(chains[phys], name)
		}
	}
	for i := range chains {
		sortStrings(chains[i])
	}
	return mask, chains
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// maskFromSegments converts the canonical write set back into a byte mask.
func maskFromSegments(segs []Segment, size int) [][]string {
	chains := make([][]string, size)
	for _, seg := range segs {
		for p := seg.Start; p < seg.End; p++ {
			chains[p] = append([]string(nil), seg.Descriptors...)
		}
	}
	return chains
}

func chainsEqual(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

func newTestCompiler(t *testing.T, d uint64, bits int, mode Mode) *Compiler {
	t.Helper()
	opts := []Option{withRepresentationBits(uint(bits))}
	if mode == ModularDecode {
		opts = append(opts, WithModularDecode())
	}
	c, err := New(d, opts...)
	if err != nil {
		t.Fatalf("New(%d, bits=%d): %v", d, bits, err)
	}
	return c
}
