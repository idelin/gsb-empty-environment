package netsec

import (
	"math/big"
	"math/rand"
	"testing"
)

func rootsAsUint(roots []*big.Int) []uint64 {
	out := make([]uint64, len(roots))
	for i, r := range roots {
		out[i] = r.Uint64()
	}
	return out
}

// 8 位小域：随机多项式的 Roots 与全域穷举求值逐一核对。
func TestRoots8AgainstBruteForce(t *testing.T) {
	f := Field8()
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 400; trial++ {
		deg := rng.Intn(7) // 0..6 次
		p := make(Poly, deg+1)
		for i := range p {
			p[i] = f.FromUint(uint64(rng.Intn(256)))
		}
		p = f.polyNorm(p)
		var want []uint64
		for x := 0; x < 256; x++ {
			if f.PolyEval(p, f.FromUint(uint64(x))).Sign() == 0 {
				want = append(want, uint64(x))
			}
		}
		got := rootsAsUint(f.Roots(p))
		if len(got) != len(want) {
			t.Fatalf("trial %d: 根数不符 got=%v want=%v", trial, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("trial %d: 根不符 got=%v want=%v", trial, got, want)
			}
		}
	}
}

// 特殊形态：零多项式、常数、重复根、H=0。
func TestRootsSpecialForms(t *testing.T) {
	for _, f := range []*Field{Field8(), Field128()} {
		if got := f.Roots(nil); got != nil {
			t.Fatalf("n=%d 零多项式应无根报告", f.N())
		}
		if got := f.Roots(f.polyConst(f.One())); got != nil {
			t.Fatalf("n=%d 常数非零多项式应无根", f.N())
		}
		// X^2：0 是二重根，只报告一次。
		x2 := Poly{f.Zero(), f.Zero(), f.One()}
		got := rootsAsUint(f.Roots(x2))
		if len(got) != 1 || got[0] != 0 {
			t.Fatalf("n=%d X^2 的根应为 [0]，got=%v", f.N(), got)
		}
		// X^2 + X：根 0 与 1。
		got = rootsAsUint(f.Roots(Poly{f.Zero(), f.One(), f.One()}))
		if len(got) != 2 || got[0] != 0 || got[1] != 1 {
			t.Fatalf("n=%d X^2+X 的根应为 [0 1]，got=%v", f.N(), got)
		}
		// (X+a)^2：a 为二重根。
		a := f.FromUint(0x2a)
		a2 := f.Square(a)
		got = rootsAsUint(f.Roots(Poly{a2, f.Zero(), f.One()}))
		if len(got) != 1 || got[0] != a.Uint64() {
			t.Fatalf("n=%d (X+a)^2 的根应为 [a]，got=%v", f.N(), got)
		}
	}
}

// 128 位域：由已知根构造多项式（含重复根），核对 Roots 精确还原。
func TestRoots128Constructed(t *testing.T) {
	f := Field128()
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 30; trial++ {
		k := 1 + rng.Intn(4)
		want := map[string]bool{}
		p := Poly{f.One()}
		for i := 0; i < k; i++ {
			r := randElem(f, rng)
			want[r.String()] = true
			// 随机把该因子重复 1~3 次，制造重根。
			for rep := 0; rep < 1+rng.Intn(3); rep++ {
				p = f.PolyMul(p, Poly{r, f.One()})
			}
		}
		got := f.Roots(p)
		if len(got) != len(want) {
			t.Fatalf("trial %d: 根数不符 got=%d want=%d", trial, len(got), len(want))
		}
		for _, r := range got {
			if !want[r.String()] {
				t.Fatalf("trial %d: 多出根 %x", trial, r)
			}
		}
	}
}
