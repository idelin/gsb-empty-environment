package ratbez

import (
	"fmt"
	"math/big"
)

// SampleCheck is an AUXILIARY sanity check, NOT part of the certificate and
// NOT a completeness proof: it evaluates R exactly (arbitrary-precision
// rational arithmetic) at n+1 uniformly spaced points of [0,1] and verifies
// that every sampled value lies inside [MinLo, MaxHi]. A violation indicates
// a bug, but passing proves nothing by itself.
func SampleCheck(c *Cert, n int) error {
	if n < 1 {
		return fmt.Errorf("need n >= 1")
	}
	N, D := buildND(c.P, c.W)
	g := GCD(N, D)
	N1, _ := QuoRem(N, g)
	D1, _ := QuoRem(D, g)
	for k := 0; k <= n; k++ {
		t := big.NewRat(int64(k), int64(n))
		v := new(big.Rat).Quo(N1.Eval(t), D1.Eval(t))
		if c.Constant {
			if v.Cmp(c.ConstValue) != 0 {
				return fmt.Errorf("sample t=%s: value %s != constant %s", t.RatString(), v.RatString(), c.ConstValue.RatString())
			}
			continue
		}
		if v.Cmp(c.MinLo) < 0 || v.Cmp(c.MaxHi) > 0 {
			return fmt.Errorf("sample t=%s: value %s outside certified [%s, %s]",
				t.RatString(), v.RatString(), c.MinLo.RatString(), c.MaxHi.RatString())
		}
	}
	return nil
}
