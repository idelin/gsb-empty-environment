package ipp

import (
	"bytes"
	"fmt"
	"math/big"
)

type Config struct {
	Context     []byte
	DomainLabel []byte
}

func checkWitness(q *big.Int, a, b []*big.Int, n int) error {
	if len(a) != n || len(b) != n {
		return fmt.Errorf("%w: n=%d, a=%d, b=%d", ErrWitnessLength, n, len(a), len(b))
	}
	for i := range a {
		if err := checkScalar(a[i], q); err != nil {
			return fmt.Errorf("a[%d]: %w", i, err)
		}
	}
	for i := range b {
		if err := checkScalar(b[i], q); err != nil {
			return fmt.Errorf("b[%d]: %w", i, err)
		}
	}
	return nil
}

func innerProduct(a, b []*big.Int, q *big.Int) *big.Int {
	sum := big.NewInt(0)
	for i := range a {
		sum = addMod(sum, mulMod(a[i], b[i], q), q)
	}
	return sum
}

func commitPrepared(ad Adapter, pp *preparedParams, a, b []*big.Int) (Point, error) {
	p := clonePoint(pp.id)
	for i := 0; i < pp.n; i++ {
		term, err := ad.MultiplyPoint(a[i], pp.g[i])
		if err != nil {
			return nil, fmt.Errorf("a[%d]G[%d]: %w", i, i, err)
		}
		p, err = ad.AddPoints(p, term)
		if err != nil {
			return nil, err
		}
		term, err = ad.MultiplyPoint(b[i], pp.h[i])
		if err != nil {
			return nil, fmt.Errorf("b[%d]H[%d]: %w", i, i, err)
		}
		p, err = ad.AddPoints(p, term)
		if err != nil {
			return nil, err
		}
	}
	cross := innerProduct(a, b, pp.q)
	uTerm, err := ad.MultiplyPoint(cross, pp.u)
	if err != nil {
		return nil, fmt.Errorf("<a,b>U: %w", err)
	}
	p, err = ad.AddPoints(p, uTerm)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func Commit(ad Adapter, gens Generators, a, b []*big.Int) (Point, error) {
	n := len(a)
	if !ValidN(n) || len(b) != n || len(gens.G) != n || len(gens.H) != n || len(gens.U) == 0 {
		if !ValidN(n) {
			return nil, fmt.Errorf("%w: %d", ErrN, n)
		}
		return nil, fmt.Errorf("%w: n=%d", ErrWitnessLength, n)
	}
	id, err := identityPoint(ad)
	if err != nil {
		return nil, err
	}
	pp, err := prepareParams(ad, nil, gens, id, true)
	if err != nil {
		return nil, err
	}
	if err := checkWitness(pp.q, a, b, n); err != nil {
		return nil, err
	}
	return commitPrepared(ad, pp, a, b)
}

func Prove(ad Adapter, context []byte, gens Generators, a, b []*big.Int) (Point, *Proof, error) {
	return generate(ad, nil, context, gens, nil, a, b)
}

func ProveWithConfig(ad Adapter, cfg Config, gens Generators, a, b []*big.Int) (Point, *Proof, error) {
	return generate(ad, cfg.DomainLabel, cfg.Context, gens, nil, a, b)
}

func Generate(ad Adapter, cfg Config, gens Generators, witness Witness) (Point, *Proof, error) {
	return generate(ad, cfg.DomainLabel, cfg.Context, gens, nil, witness.A, witness.B)
}

func GenerateProof(ad Adapter, context []byte, gens Generators, commitment Point, a, b []*big.Int) (*Proof, error) {
	_, proof, err := generate(ad, nil, context, gens, commitment, a, b)
	return proof, err
}

func GenerateProofWithDomain(ad Adapter, domainLabel, context []byte, gens Generators, commitment Point, a, b []*big.Int) (*Proof, error) {
	_, proof, err := generate(ad, domainLabel, context, gens, commitment, a, b)
	return proof, err
}

func generate(ad Adapter, domainLabel, context []byte, gens Generators, suppliedP Point, a, b []*big.Int) (Point, *Proof, error) {
	n := len(a)
	if !ValidN(n) {
		return nil, nil, fmt.Errorf("%w: %d", ErrN, n)
	}
	if len(b) != n {
		return nil, nil, fmt.Errorf("%w: a=%d b=%d", ErrWitnessLength, n, len(b))
	}
	if len(gens.G) != n || len(gens.H) != n || len(gens.U) == 0 {
		return nil, nil, fmt.Errorf("%w: witness n=%d G=%d H=%d",
			ErrGeneratorLength, n, len(gens.G), len(gens.H))
	}

	placeholderP := suppliedP
	if placeholderP == nil {
		id, err := identityPoint(ad)
		if err != nil {
			return nil, nil, err
		}
		placeholderP = id
	}
	pp, err := prepareParams(ad, context, gens, placeholderP, true)
	if err != nil {
		return nil, nil, err
	}
	if err := checkWitness(pp.q, a, b, n); err != nil {
		return nil, nil, err
	}

	computedP, err := commitPrepared(ad, pp, a, b)
	if err != nil {
		return nil, nil, err
	}
	if suppliedP != nil {
		if !bytes.Equal(pp.p, computedP) {
			return nil, nil, fmt.Errorf("%w: supplied P does not equal witness commitment", ErrWitnessCommitment)
		}
	}
	pp.p = computedP

	transcript, err := newTranscript(domainLabel, context, pp)
	if err != nil {
		return nil, nil, err
	}

	curA := make([]*big.Int, n)
	curB := make([]*big.Int, n)
	for i := range a {
		curA[i] = new(big.Int).Set(a[i])
		curB[i] = new(big.Int).Set(b[i])
	}
	curG := pp.g
	curH := pp.h
	curP := pp.p
	proof := &Proof{N: n, L: make([]Point, 0, pp.rounds), R: make([]Point, 0, pp.rounds)}
	proof.contextTag = transcript.initialTag()

	for round := 0; round < pp.rounds; round++ {
		half := len(curA) / 2
		lPoint := clonePoint(pp.id)
		rPoint := clonePoint(pp.id)

		for i := 0; i < half; i++ {
			term, err := ad.MultiplyPoint(curA[i], curG[half+i])
			if err != nil {
				return nil, nil, fmt.Errorf("round %d L a: %w", round, err)
			}
			lPoint, err = ad.AddPoints(lPoint, term)
			if err != nil {
				return nil, nil, err
			}
			term, err = ad.MultiplyPoint(curB[half+i], curH[i])
			if err != nil {
				return nil, nil, fmt.Errorf("round %d L b: %w", round, err)
			}
			lPoint, err = ad.AddPoints(lPoint, term)
			if err != nil {
				return nil, nil, err
			}

			term, err = ad.MultiplyPoint(curA[half+i], curG[i])
			if err != nil {
				return nil, nil, fmt.Errorf("round %d R a: %w", round, err)
			}
			rPoint, err = ad.AddPoints(rPoint, term)
			if err != nil {
				return nil, nil, err
			}
			term, err = ad.MultiplyPoint(curB[i], curH[half+i])
			if err != nil {
				return nil, nil, fmt.Errorf("round %d R b: %w", round, err)
			}
			rPoint, err = ad.AddPoints(rPoint, term)
			if err != nil {
				return nil, nil, err
			}
		}

		crossLR := innerProduct(curA[:half], curB[half:], pp.q)
		uTerm, err := ad.MultiplyPoint(crossLR, pp.u)
		if err != nil {
			return nil, nil, err
		}
		lPoint, err = ad.AddPoints(lPoint, uTerm)
		if err != nil {
			return nil, nil, err
		}
		crossRL := innerProduct(curA[half:], curB[:half], pp.q)
		uTerm, err = ad.MultiplyPoint(crossRL, pp.u)
		if err != nil {
			return nil, nil, err
		}
		rPoint, err = ad.AddPoints(rPoint, uTerm)
		if err != nil {
			return nil, nil, err
		}

		x, err := transcript.Challenge(lPoint, rPoint)
		if err != nil {
			return nil, nil, fmt.Errorf("round %d: %w", round, err)
		}
		if err := checkNonZeroScalar(x, pp.q); err != nil {
			return nil, nil, fmt.Errorf("round %d challenge: %w", round, err)
		}
		xInv, err := invMod(x, pp.q)
		if err != nil {
			return nil, nil, fmt.Errorf("round %d challenge: %w", round, err)
		}

		x2 := mulMod(x, x, pp.q)
		xInv2 := mulMod(xInv, xInv, pp.q)
		lTerm, err := ad.MultiplyPoint(x2, lPoint)
		if err != nil {
			return nil, nil, err
		}
		curP, err = ad.AddPoints(curP, lTerm)
		if err != nil {
			return nil, nil, err
		}
		rTerm, err := ad.MultiplyPoint(xInv2, rPoint)
		if err != nil {
			return nil, nil, err
		}
		curP, err = ad.AddPoints(curP, rTerm)
		if err != nil {
			return nil, nil, err
		}

		newA := make([]*big.Int, half)
		newB := make([]*big.Int, half)
		newG := make([]Point, half)
		newH := make([]Point, half)
		for i := 0; i < half; i++ {
			newA[i] = addMod(
				mulMod(x, curA[i], pp.q),
				mulMod(xInv, curA[half+i], pp.q),
				pp.q,
			)
			newB[i] = addMod(
				mulMod(xInv, curB[i], pp.q),
				mulMod(x, curB[half+i], pp.q),
				pp.q,
			)

			leftG, err := ad.MultiplyPoint(xInv, curG[i])
			if err != nil {
				return nil, nil, err
			}
			rightG, err := ad.MultiplyPoint(x, curG[half+i])
			if err != nil {
				return nil, nil, err
			}
			newG[i], err = ad.AddPoints(leftG, rightG)
			if err != nil {
				return nil, nil, err
			}

			leftH, err := ad.MultiplyPoint(x, curH[i])
			if err != nil {
				return nil, nil, err
			}
			rightH, err := ad.MultiplyPoint(xInv, curH[half+i])
			if err != nil {
				return nil, nil, err
			}
			newH[i], err = ad.AddPoints(leftH, rightH)
			if err != nil {
				return nil, nil, err
			}
		}

		proof.L = append(proof.L, clonePoint(lPoint))
		proof.R = append(proof.R, clonePoint(rPoint))
		curA = newA
		curB = newB
		curG = newG
		curH = newH
	}

	proof.A = new(big.Int).Set(curA[0])
	proof.B = new(big.Int).Set(curB[0])
	return clonePoint(pp.p), proof, nil
}
