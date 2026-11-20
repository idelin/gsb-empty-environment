package prescreen

import "math/bits"

// AffineMap is y = M x + b over GF(2). Row i holds the coefficients of
// output bit i; Bias is added after the linear part.
type AffineMap struct {
	Rows [Width]Vec
	Bias Vec
}

// NewMap validates and builds an affine map from its output rows and bias.
func NewMap(rows []Vec, bias Vec) (AffineMap, error) {
	if len(rows) != Width {
		return AffineMap{}, ErrMalformedMap
	}
	var m AffineMap
	for i, r := range rows {
		if uint64(r) > fullMask {
			return AffineMap{}, ErrMalformedMap
		}
		m.Rows[i] = r
	}
	if uint64(bias) > fullMask {
		return AffineMap{}, ErrMalformedMap
	}
	m.Bias = bias
	return m, nil
}

// IdentityMap returns x -> x.
func IdentityMap() AffineMap {
	var m AffineMap
	for i := 0; i < Width; i++ {
		m.Rows[i] = Vec(1) << i
	}
	return m
}

func (m AffineMap) linear(v Vec) Vec {
	var out Vec
	for i := 0; i < Width; i++ {
		if bits.OnesCount64(uint64(v&m.Rows[i]))&1 == 1 {
			out |= Vec(1) << i
		}
	}
	return out
}

// Rank returns the rank of the linear part of the map.
func (m AffineMap) Rank() int {
	rows := make([]Vec, Width)
	copy(rows, m.Rows[:])
	return len(rrefVec(rows))
}

// Apply pushes an affine set through the map. Rank-deficient maps collapse
// distinct logical blocks onto the same physical block; the image direction
// basis is re-ranked so aliases are counted only once.
func (m AffineMap) Apply(af Affine) Affine {
	if af.Empty {
		return EmptyAffine()
	}
	dir := make([]Vec, 0, len(af.Dir))
	for _, b := range af.Dir {
		dir = append(dir, m.linear(b))
	}
	return NewAffine(dir, m.linear(af.Offset)^m.Bias)
}

// Compose returns m2 ∘ m1, i.e. first m1 then m2.
func Compose(m2, m1 AffineMap) AffineMap {
	var out AffineMap
	for i := 0; i < Width; i++ {
		out.Rows[i] = m2.linear(m1.Rows[i])
	}
	out.Bias = m2.linear(m1.Bias) ^ m2.Bias
	return out
}
