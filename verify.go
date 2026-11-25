package ipp

import (
	"bytes"
	"fmt"
	"math/big"
)

func VerifyProof(ad Adapter, context []byte, gens Generators, commitment Point, proof *Proof) error {
	return Verify(ad, context, gens, commitment, proof)
}

func Verify(ad Adapter, context []byte, gens Generators, commitment Point, proof *Proof) error {
	return verify(ad, nil, context, gens, commitment, proof)
}

func VerifyWithDomain(ad Adapter, domainLabel, context []byte, gens Generators, commitment Point, proof *Proof) error {
	return verify(ad, domainLabel, context, gens, commitment, proof)
}

func VerifyWithConfig(ad Adapter, cfg Config, gens Generators, commitment Point, proof *Proof) error {
	return verify(ad, cfg.DomainLabel, cfg.Context, gens, commitment, proof)
}

func VerifyStatement(ad Adapter, statement Statement, proof *Proof) error {
	return verify(ad, statement.DomainLabel, statement.Context, statement.Generators, statement.Commitment, proof)
}

func Challenges(ad Adapter, context []byte, gens Generators, commitment Point, proof *Proof) ([]*big.Int, error) {
	rec, err := reconstruct(ad, nil, context, gens, commitment, proof)
	if err != nil {
		return nil, err
	}
	return rec.challenges, nil
}

type reconstruction struct {
	pp         *preparedParams
	l          []Point
	r          []Point
	challenges []*big.Int
}

func reconstruct(ad Adapter, domainLabel, context []byte, gens Generators, commitment Point, proof *Proof) (*reconstruction, error) {
	if proof == nil {
		return nil, fmt.Errorf("%w: nil proof", ErrProofFormat)
	}
	pp, err := prepareParams(ad, context, gens, commitment, true)
	if err != nil {
		return nil, err
	}
	if proof.N != pp.n {
		return nil, fmt.Errorf("%w: statement n=%d proof n=%d", ErrN, pp.n, proof.N)
	}
	if len(proof.L) != pp.rounds || len(proof.R) != pp.rounds {
		return nil, fmt.Errorf("%w: n=%d wants %d, got L=%d R=%d",
			ErrWrongRoundCount, pp.n, pp.rounds, len(proof.L), len(proof.R))
	}
	if err := checkScalar(proof.A, pp.q); err != nil {
		return nil, fmt.Errorf("final a: %w", err)
	}
	if err := checkScalar(proof.B, pp.q); err != nil {
		return nil, fmt.Errorf("final b: %w", err)
	}

	l := make([]Point, pp.rounds)
	r := make([]Point, pp.rounds)
	for i := 0; i < pp.rounds; i++ {
		l[i], err = canonicalPoint(ad, proof.L[i])
		if err != nil {
			return nil, fmt.Errorf("L[%d]: %w", i, err)
		}
		r[i], err = canonicalPoint(ad, proof.R[i])
		if err != nil {
			return nil, fmt.Errorf("R[%d]: %w", i, err)
		}
	}

	transcript, err := newTranscript(domainLabel, context, pp)
	if err != nil {
		return nil, err
	}
	if len(proof.contextTag) > 0 && !bytes.Equal(proof.contextTag, transcript.initialTag()) {
		return nil, fmt.Errorf("%w: domain/context/n/generators/P transcript prefix", ErrContextMismatch)
	}
	rec := &reconstruction{pp: pp, l: l, r: r, challenges: make([]*big.Int, pp.rounds)}
	for i := 0; i < pp.rounds; i++ {
		x, err := transcript.Challenge(l[i], r[i])
		if err != nil {
			return nil, fmt.Errorf("round %d: %w", i, err)
		}
		if err := checkNonZeroScalar(x, pp.q); err != nil {
			return nil, fmt.Errorf("round %d transcript challenge: %w", i, err)
		}
		rec.challenges[i] = x
	}
	return rec, nil
}

func verify(ad Adapter, domainLabel, context []byte, gens Generators, commitment Point, proof *Proof) error {
	errorPoint, err := verificationErrorPoint(ad, domainLabel, context, gens, commitment, proof)
	if err != nil {
		return err
	}
	id := ad.Identity()
	canonicalID, err := canonicalPoint(ad, id)
	if err != nil {
		return err
	}
	if !bytes.Equal(errorPoint, canonicalID) {
		if proof.A.Sign() == 0 || proof.B.Sign() == 0 {
			return fmt.Errorf("%w: injected zero opening does not satisfy commitment: %w", ErrZeroScalar, ErrVerificationFailed)
		}
		return fmt.Errorf("%w: folded commitment does not match final single-element opening", ErrVerificationFailed)
	}
	return nil
}

func verificationErrorPoint(ad Adapter, domainLabel, context []byte, gens Generators, commitment Point, proof *Proof) (Point, error) {
	rec, err := reconstruct(ad, domainLabel, context, gens, commitment, proof)
	if err != nil {
		return nil, err
	}
	pp := rec.pp
	q := pp.q

	curP := clonePoint(pp.p)
	curG := pp.g
	curH := pp.h
	for round, x := range rec.challenges {
		xInv, err := invMod(x, q)
		if err != nil {
			return nil, fmt.Errorf("round %d challenge inverse: %w", round, err)
		}
		x2 := mulMod(x, x, q)
		xInv2 := mulMod(xInv, xInv, q)

		lTerm, err := ad.MultiplyPoint(x2, rec.l[round])
		if err != nil {
			return nil, err
		}
		curP, err = ad.AddPoints(curP, lTerm)
		if err != nil {
			return nil, err
		}
		rTerm, err := ad.MultiplyPoint(xInv2, rec.r[round])
		if err != nil {
			return nil, err
		}
		curP, err = ad.AddPoints(curP, rTerm)
		if err != nil {
			return nil, err
		}

		half := len(curG) / 2
		newG := make([]Point, half)
		newH := make([]Point, half)
		for i := 0; i < half; i++ {
			leftG, err := ad.MultiplyPoint(xInv, curG[i])
			if err != nil {
				return nil, err
			}
			rightG, err := ad.MultiplyPoint(x, curG[half+i])
			if err != nil {
				return nil, err
			}
			newG[i], err = ad.AddPoints(leftG, rightG)
			if err != nil {
				return nil, err
			}

			leftH, err := ad.MultiplyPoint(x, curH[i])
			if err != nil {
				return nil, err
			}
			rightH, err := ad.MultiplyPoint(xInv, curH[half+i])
			if err != nil {
				return nil, err
			}
			newH[i], err = ad.AddPoints(leftH, rightH)
			if err != nil {
				return nil, err
			}
		}
		curG = newG
		curH = newH
	}

	if len(curG) != 1 || len(curH) != 1 {
		return nil, fmt.Errorf("%w: final vector length %d", ErrWrongRoundCount, len(curG))
	}

	aTerm, err := ad.MultiplyPoint(proof.A, curG[0])
	if err != nil {
		return nil, err
	}
	bTerm, err := ad.MultiplyPoint(proof.B, curH[0])
	if err != nil {
		return nil, err
	}
	rhs, err := ad.AddPoints(aTerm, bTerm)
	if err != nil {
		return nil, err
	}
	ab := mulMod(proof.A, proof.B, q)
	uTerm, err := ad.MultiplyPoint(ab, pp.u)
	if err != nil {
		return nil, err
	}
	rhs, err = ad.AddPoints(rhs, uTerm)
	if err != nil {
		return nil, err
	}
	negativeOne := new(big.Int).Sub(q, big.NewInt(1))
	negativeRHS, err := ad.MultiplyPoint(negativeOne, rhs)
	if err != nil {
		return nil, err
	}
	errorPoint, err := ad.AddPoints(curP, negativeRHS)
	if err != nil {
		return nil, err
	}
	return errorPoint, nil
}
