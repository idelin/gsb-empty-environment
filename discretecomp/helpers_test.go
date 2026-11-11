package discretecomp

import "math/big"

func rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

func dist(pairs ...string) Distribution {
	d := Distribution{}
	for i := 0; i < len(pairs); i += 2 {
		r, ok := new(big.Rat).SetString(pairs[i+1])
		if !ok {
			panic("bad rational " + pairs[i+1])
		}
		d[pairs[i]] = r
	}
	return d
}

// bernoulli returns P, Q where Q(1) = 3/2 * P(1), giving clean ratios.
func bernoulli(p *big.Rat) (Distribution, Distribution) {
	one := big.NewRat(1, 1)
	pDist := Distribution{
		"0": new(big.Rat).Sub(one, p),
		"1": new(big.Rat).Set(p),
	}
	q1 := new(big.Rat).Mul(p, rat(3, 2))
	qDist := Distribution{
		"0": new(big.Rat).Sub(one, q1),
		"1": q1,
	}
	return pDist, qDist
}
