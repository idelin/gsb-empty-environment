package netsec

// Verify checks the inner product proof for commitment P against the
// host-fixed parameters. It fully reconstructs every round challenge from
// the transcript (domain tag, host context, n, all generators, P, and the
// ordered L/R encodings) and folds the generators and the commitment:
//
//	G = x^-1*GL + x*GR   H = x*HL + x^-1*HR
//	P = P + x^2*L + x^-2*R
//
// before checking the final single-element commitment equation
// P == a*G + b*H + a*b*U. Verification never skips challenge
// reconstruction.
func Verify(g Group, params *Params, P Point, proof *Proof) error {
	if err := params.validate(); err != nil {
		return err
	}
	if P == nil || proof == nil || proof.A == nil || proof.B == nil {
		return ErrParams
	}
	n := len(params.G)
	rounds := roundsFor(n)
	if len(proof.L) != rounds || len(proof.R) != rounds {
		return ErrRoundCount
	}
	for i := 0; i < rounds; i++ {
		if proof.L[i] == nil || proof.R[i] == nil {
			return ErrEncoding
		}
	}

	t := newTranscript(g, params, P)
	G := append([]Point(nil), params.G...)
	H := append([]Point(nil), params.H...)
	acc := P

	for i := 0; i < rounds; i++ {
		L, R := proof.L[i], proof.R[i]
		t.absorbPoint(L)
		t.absorbPoint(R)
		x := t.challenge()
		xInv := x.Inv()
		x2 := x.Mul(x)
		xInv2 := xInv.Mul(xInv)

		acc = acc.Add(L.ScalarMul(x2)).Add(R.ScalarMul(xInv2))

		half := n / 2
		G = foldPoints(G[:half], G[half:], xInv, x)
		H = foldPoints(H[:half], H[half:], x, xInv)
		n = half
	}

	rhs := G[0].ScalarMul(proof.A)
	rhs = rhs.Add(H[0].ScalarMul(proof.B))
	rhs = rhs.Add(params.U.ScalarMul(proof.A.Mul(proof.B)))
	if !acc.Equal(rhs) {
		return ErrVerification
	}
	return nil
}

// VerifyBytes decodes an encoded proof (rejecting truncated, over-long,
// or non-canonical encodings) and then runs full verification.
func VerifyBytes(g Group, params *Params, P Point, data []byte) error {
	proof, err := UnmarshalProof(g, data)
	if err != nil {
		return err
	}
	return Verify(g, params, P, proof)
}
