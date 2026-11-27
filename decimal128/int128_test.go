package decimal128

import (
	"math/big"
	"math/rand"
	"testing"
)

func u128FromBig(b *big.Int) u128 {
	return u128{lo: b.Uint64(), hi: new(big.Int).Rsh(b, 64).Uint64()}
}

func u256FromBig(b *big.Int) u256 {
	var x u256
	for i := 0; i < 4; i++ {
		x.l[i] = new(big.Int).And(b, new(big.Int).SetUint64(^uint64(0))).Uint64()
		b.Rsh(b, 64)
	}
	return x
}

func u256ToBig(x u256) *big.Int {
	b := new(big.Int)
	for i := 3; i >= 0; i-- {
		b.Lsh(b, 64)
		b.Or(b, new(big.Int).SetUint64(x.l[i]))
	}
	return b
}

func TestU128MulDiv(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		a := u128FromBig(new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), 128)))
		b := u128FromBig(new(big.Int).Rand(rng, new(big.Int).Lsh(big.NewInt(1), 128)))
		full := new(big.Int).Mul(u256ToBig(a.to256()), u256ToBig(b.to256()))
		mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
		want := new(big.Int).And(full, mask)
		got := a.mul(b)
		if u256ToBig(got.to256()).Cmp(want) != 0 {
			t.Fatalf("mul mismatch: %v * %v", a, b)
		}
		if b.isZero() {
			continue
		}
		q, r := a.div128(b)
		ab := new(big.Int).And(u256ToBig(a.to256()), mask)
		wantQ, wantR := new(big.Int).QuoRem(ab, u256ToBig(b.to256()), new(big.Int))
		if u256ToBig(q.to256()).Cmp(wantQ) != 0 || u256ToBig(r.to256()).Cmp(wantR) != 0 {
			t.Fatalf("div mismatch: %v / %v", a, b)
		}
	}
}

func TestU256MulDiv(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	for i := 0; i < 500; i++ {
		ab := new(big.Int).Rand(rng, limit)
		bb := new(big.Int).Rand(rng, limit)
		a := u256FromBig(new(big.Int).Set(ab))
		b := u256FromBig(new(big.Int).Set(bb))
		want := new(big.Int).Mul(ab, bb)
		got := a.mul(b)
		if u256ToBig(got).Cmp(want) != 0 {
			t.Fatalf("u256 mul mismatch")
		}
		if bb.Sign() == 0 {
			continue
		}
		q, r := a.div(b)
		wantQ, wantR := new(big.Int).QuoRem(ab, bb, new(big.Int))
		if u256ToBig(q).Cmp(wantQ) != 0 || u256ToBig(r).Cmp(wantR) != 0 {
			t.Fatalf("u256 div mismatch: %v / %v", ab, bb)
		}
		reconstruct := u256ToBig(q).Mul(u256ToBig(q), bb)
		reconstruct.Add(reconstruct, u256ToBig(r))
		if reconstruct.Cmp(ab) != 0 {
			t.Fatalf("u256 div reconstruction failed")
		}
	}

	// Deterministic cross-64-boundary cases.
	edge := u128{hi: 1, lo: 0} // 2^64
	if got, _ := edge.div128(u128{lo: 2}); got.lo != 1<<63 || got.hi != 0 {
		t.Fatalf("2^64 / 2 failed: %+v", got)
	}
	sum, carry := u128{hi: 0, lo: ^uint64(0)}.add(u128{lo: 1}, 0)
	if carry != 0 || sum.hi != 1 || sum.lo != 0 {
		t.Fatalf("64-bit carry failed: %+v carry=%d", sum, carry)
	}
	diff, borrow := u128{hi: 1, lo: 0}.sub128(u128{lo: 1})
	if borrow != 0 || diff.hi != 0 || diff.lo != ^uint64(0) {
		t.Fatalf("64-bit borrow failed: %+v", diff)
	}
}

func TestSigned128(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	limit := new(big.Int).Lsh(big.NewInt(1), 120)
	for i := 0; i < 1000; i++ {
		ab := new(big.Int).Rand(rng, limit)
		bb := new(big.Int).Rand(rng, limit)
		if rng.Intn(2) == 0 {
			ab.Neg(ab)
		}
		if rng.Intn(2) == 0 {
			bb.Neg(bb)
		}
		a := fromMag(u128FromBig(new(big.Int).Abs(ab)), ab.Sign() < 0)
		b := fromMag(u128FromBig(new(big.Int).Abs(bb)), bb.Sign() < 0)
		want := new(big.Int).Add(ab, bb)
		got := addSigned(a, b)
		gotBig := new(big.Int)
		if got.hi < 0 {
			gotBig.Set(u256ToBig(got.neg().mag().to256())).Neg(gotBig)
		} else {
			gotBig.Set(u256ToBig(got.mag().to256()))
		}
		if gotBig.Cmp(want) != 0 {
			t.Fatalf("signed add mismatch: %v + %v", ab, bb)
		}
		if bb.Sign() == 0 {
			continue
		}
		q, r := divmodSigned(a, b)
		wantQ, wantR := new(big.Int).QuoRem(ab, bb, new(big.Int))
		qBig := signedToBig(q)
		rBig := signedToBig(r)
		if qBig.Cmp(wantQ) != 0 || rBig.Cmp(wantR) != 0 {
			t.Fatalf("signed divmod mismatch: %v / %v got q=%v r=%v want q=%v r=%v",
				ab, bb, qBig, rBig, wantQ, wantR)
		}
		if rBig.Sign() != 0 && rBig.Sign() != ab.Sign() {
			t.Fatalf("remainder sign must match dividend")
		}
	}
}

func signedToBig(v i128) *big.Int {
	b := u256ToBig(v.mag().to256())
	if v.hi < 0 {
		b.Neg(b)
	}
	return b
}

func TestPow10Tables(t *testing.T) {
	ten := big.NewInt(10)
	for k := 0; k <= 38; k++ {
		want := new(big.Int).Exp(ten, big.NewInt(int64(k)), nil)
		if u256ToBig(pow10u128[k].to256()).Cmp(want) != 0 {
			t.Fatalf("pow10u128[%d] wrong", k)
		}
	}
	for k := 0; k <= 77; k++ {
		want := new(big.Int).Exp(ten, big.NewInt(int64(k)), nil)
		if u256ToBig(pow10u256[k]).Cmp(want) != 0 {
			t.Fatalf("pow10u256[%d] wrong", k)
		}
	}
}
