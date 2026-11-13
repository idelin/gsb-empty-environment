package netsec

import "math/big"

// innerScalar returns <a, b> in Z_q.
func innerScalar(g Group, a, b []Scalar) Scalar {
	acc := g.ScalarFromBig(new(big.Int))
	for i := range a {
		acc = acc.Add(a[i].Mul(b[i]))
	}
	return acc
}

// multiExp returns sum s_i * P_i.
func multiExp(g Group, s []Scalar, P []Point) Point {
	acc := g.Identity()
	for i := range s {
		acc = acc.Add(P[i].ScalarMul(s[i]))
	}
	return acc
}

// foldScalars returns cL*vL + cR*vR element-wise.
func foldScalars(vL, vR []Scalar, cL, cR Scalar) []Scalar {
	out := make([]Scalar, len(vL))
	for i := range vL {
		out[i] = vL[i].Mul(cL).Add(vR[i].Mul(cR))
	}
	return out
}

// foldPoints returns cL*PL + cR*PR element-wise.
func foldPoints(PL, PR []Point, cL, cR Scalar) []Point {
	out := make([]Point, len(PL))
	for i := range PL {
		out[i] = PL[i].ScalarMul(cL).Add(PR[i].ScalarMul(cR))
	}
	return out
}

// Commit computes P = sum a_i*G_i + sum b_i*H_i + <a,b>*U.
func Commit(g Group, params *Params, a, b []Scalar) (Point, error) {
	if err := params.validate(); err != nil {
		return nil, err
	}
	n := len(params.G)
	if len(a) != n || len(b) != n {
		return nil, ErrWitnessLength
	}
	acc := g.Identity()
	acc = acc.Add(multiExp(g, a, params.G))
	acc = acc.Add(multiExp(g, b, params.H))
	acc = acc.Add(params.U.ScalarMul(innerScalar(g, a, b)))
	return acc, nil
}

// Prove generates the inner product proof for the witness (a, b) against
// the commitment P = sum a_i*G_i + sum b_i*H_i + <a,b>*U. It returns the
// commitment together with the proof. The proof is deterministic: all
// challenges are derived from the public transcript.
//
// Per round, with (vL, vR) denoting the two halves of a vector:
//
//	L = <aL,GR> + <bR,HL> + <aL,bR>*U
//	R = <aR,GL> + <bL,HR> + <aR,bL>*U
//	a = x*aL + x^-1*aR   b = x^-1*bL + x*bR
//	G = x^-1*GL + x*GR   H = x*HL + x^-1*HR
func Prove(g Group, params *Params, a, b []Scalar) (Point, *Proof, error) {
	if err := params.validate(); err != nil {
		return nil, nil, err
	}
	n := len(params.G)
	if len(a) != n || len(b) != n {
		return nil, nil, ErrWitnessLength
	}
	P, err := Commit(g, params, a, b)
	if err != nil {
		return nil, nil, err
	}
	t := newTranscript(g, params, P)

	aa := append([]Scalar(nil), a...)
	bb := append([]Scalar(nil), b...)
	G := append([]Point(nil), params.G...)
	H := append([]Point(nil), params.H...)

	proof := &Proof{}
	for n > 1 {
		half := n / 2
		aL, aR := aa[:half], aa[half:]
		bL, bR := bb[:half], bb[half:]
		GL, GR := G[:half], G[half:]
		HL, HR := H[:half], H[half:]

		L := g.Identity()
		L = L.Add(multiExp(g, aL, GR))
		L = L.Add(multiExp(g, bR, HL))
		L = L.Add(params.U.ScalarMul(innerScalar(g, aL, bR)))

		R := g.Identity()
		R = R.Add(multiExp(g, aR, GL))
		R = R.Add(multiExp(g, bL, HR))
		R = R.Add(params.U.ScalarMul(innerScalar(g, aR, bL)))

		proof.L = append(proof.L, L)
		proof.R = append(proof.R, R)

		t.absorbPoint(L)
		t.absorbPoint(R)
		x := t.challenge()
		xInv := x.Inv()

		aa = foldScalars(aL, aR, x, xInv)
		bb = foldScalars(bL, bR, xInv, x)
		G = foldPoints(GL, GR, xInv, x)
		H = foldPoints(HL, HR, x, xInv)
		n = half
	}
	proof.A = aa[0]
	proof.B = bb[0]
	return P, proof, nil
}
