package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// Rat 精确有理数，JSON 中以字符串表示（支持 "a/b"、整数、小数）。
type Rat struct{ R *big.Rat }

func (x Rat) String() string {
	if x.R == nil {
		return "0"
	}
	return x.R.RatString()
}

func (x Rat) MarshalJSON() ([]byte, error) { return json.Marshal(x.String()) }

func (x *Rat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), "\"")
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return fmt.Errorf("无效有理数 %q", s)
	}
	x.R = r
	return nil
}

func radd(a, b *big.Rat) *big.Rat { return new(big.Rat).Add(a, b) }
func rsub(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }
func rmul(a, b *big.Rat) *big.Rat { return new(big.Rat).Mul(a, b) }
func rquo(a, b *big.Rat) *big.Rat { return new(big.Rat).Quo(a, b) }

func ratFromInt(v int64) *big.Rat { return new(big.Rat).SetInt64(v) }

func maxRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

// roundHalfEven 统一半偶舍入：取最近整数，恰为 .5 时取偶数。
func roundHalfEven(x *big.Rat) *big.Int {
	num := x.Num()
	den := x.Denom()
	q := new(big.Int)
	r := new(big.Int)
	q.QuoRem(num, den, r) // 向零取整
	if r.Sign() != 0 && num.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
		r.Add(r, den)
	}
	// 此时 x = q + r/den，且 0 <= r < den
	twice := new(big.Int).Lsh(r, 1)
	switch twice.Cmp(den) {
	case -1:
		return q
	case 1:
		return q.Add(q, big.NewInt(1))
	}
	if q.Bit(0) == 0 {
		return q
	}
	return q.Add(q, big.NewInt(1))
}
