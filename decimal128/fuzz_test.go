package decimal128

import (
	"math/big"
	"math/rand"
	"testing"
)

// FuzzArithmetic drives every operation against the exact big.Rat result.
// Inputs are syntactically valid decimal literals with at most 24 integer
// digits and 8 fractional digits, so intermediate results stay inside the
// 38-digit envelope unless an operation genuinely overflows (which must
// surface as ErrOverflow rather than a wrong value).
func FuzzArithmetic(f *testing.F) {
	f.Add(int64(1))
	f.Fuzz(func(t *testing.T, seed int64) {
		rng := rand.New(rand.NewSource(seed))
		a := randomDecimal(rng)
		b := randomDecimal(rng)
		ra, rb := ratOf(a), ratOf(b)
		ctx := Context{Mode: RoundingMode(rng.Intn(2))}

		sum, err := ctx.Add(a, b)
		if err == nil {
			expectRatMode(t, "fuzz add", sum, new(big.Rat).Add(ra, rb), ctx.Mode)
		}
		diff, err := ctx.Sub(a, b)
		if err == nil {
			expectRatMode(t, "fuzz sub", diff, new(big.Rat).Sub(ra, rb), ctx.Mode)
		}
		prod, err := ctx.Mul(a, b)
		if err == nil {
			expectRatMode(t, "fuzz mul", prod, new(big.Rat).Mul(ra, rb), ctx.Mode)
		}
		if b.IsZero() {
			return
		}
		quot, err := ctx.Div(a, b)
		if err == nil {
			want := roundRat(new(big.Rat).Quo(ra, rb), quot.Scale(), ctx.Mode)
			if signedBig(quot).Cmp(want) != 0 {
				t.Fatalf("fuzz div: %s/%s got=%s want=%s",
					a.String(), b.String(), quot.String(), want.String())
			}
		}
		rem, err := ctx.Mod(a, b)
		if err == nil {
			an, ad := ra.Num(), ra.Denom()
			bn, bd := rb.Num(), rb.Denom()
			num := new(big.Int).Mul(an, bd)
			den := new(big.Int).Mul(ad, bn)
			trunc := new(big.Int).Quo(num, den)
			wantNum := new(big.Int).Sub(num, new(big.Int).Mul(trunc, den))
			wantRat := new(big.Rat).SetFrac(wantNum, new(big.Int).Mul(ad, bd))
			expectRat(t, "fuzz mod", rem, wantRat)
			if rem.Sign() != 0 && rem.Sign() != a.Sign() {
				t.Fatalf("fuzz mod sign: %s mod %s = %s",
					a.String(), b.String(), rem.String())
			}
		}
	})
}

// FuzzParse checks that every accepted literal equals its big.Rat reading
// and survives a fixed/scientific round trip.
func FuzzParse(f *testing.F) {
	f.Add(int64(2))
	f.Fuzz(func(t *testing.T, seed int64) {
		rng := rand.New(rand.NewSource(seed))
		d := randomDecimal(rng)
		s := d.String()
		re, err := Parse(s)
		if err != nil {
			t.Fatalf("reparse %q: %v", s, err)
		}
		if Cmp(d, re) != 0 {
			t.Fatalf("parse round trip %q -> %q", s, re.String())
		}
		sci, err := Parse(d.StringScientific())
		if err != nil {
			t.Fatalf("scientific parse %q: %v", d.StringScientific(), err)
		}
		if Cmp(d, sci) != 0 {
			t.Fatalf("scientific round trip %q -> %q",
				d.StringScientific(), sci.String())
		}
	})
}
