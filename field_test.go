package netsec

import (
	"math/big"
	"math/rand"
	"testing"
)

func randElem(f *Field, rng *rand.Rand) *big.Int {
	v := new(big.Int)
	for i := 0; i < f.N(); i++ {
		if rng.Intn(2) == 1 {
			v.SetBit(v, i, 1)
		}
	}
	return v
}

// 8 位小域：穷举全部元素验证乘法群阶与 Frobenius 自洽。
func TestField8Exhaustive(t *testing.T) {
	f := Field8()
	e255 := big.NewInt(255)
	e256 := big.NewInt(256)
	for i := 0; i < 256; i++ {
		a := f.FromUint(uint64(i))
		if got := f.Pow(a, e256); !f.Eq(got, a) {
			t.Fatalf("a^256 != a, a=%d got=%v", i, got)
		}
		if i != 0 {
			if got := f.Pow(a, e255); !f.Eq(got, f.One()) {
				t.Fatalf("a^255 != 1, a=%d got=%v", i, got)
			}
			if got := f.Mul(a, f.Inv(a)); !f.Eq(got, f.One()) {
				t.Fatalf("a*inv(a) != 1, a=%d", i)
			}
		}
	}
}

// 128 位域：抽样验证域公理与 Frobenius 自洽。
func TestField128Properties(t *testing.T) {
	f := Field128()
	rng := rand.New(rand.NewSource(1))
	e := new(big.Int).Lsh(big.NewInt(1), 128)
	em1 := new(big.Int).Sub(e, big.NewInt(1))
	for trial := 0; trial < 20; trial++ {
		a, b, c := randElem(f, rng), randElem(f, rng), randElem(f, rng)
		if !f.Eq(f.Mul(a, b), f.Mul(b, a)) {
			t.Fatal("乘法不满足交换律")
		}
		if !f.Eq(f.Mul(f.Mul(a, b), c), f.Mul(a, f.Mul(b, c))) {
			t.Fatal("乘法不满足结合律")
		}
		if !f.Eq(f.Mul(a, f.Add(b, c)), f.Add(f.Mul(a, b), f.Mul(a, c))) {
			t.Fatal("乘法对加法不满足分配律")
		}
		if got := f.Pow(a, e); !f.Eq(got, a) {
			t.Fatal("a^(2^128) != a")
		}
		if a.Sign() != 0 {
			if got := f.Pow(a, em1); !f.Eq(got, f.One()) {
				t.Fatal("非零元 a^(2^128-1) != 1")
			}
			if got := f.Mul(a, f.Inv(a)); !f.Eq(got, f.One()) {
				t.Fatal("a*inv(a) != 1")
			}
		}
	}
}
