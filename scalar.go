package ipp

import (
	"fmt"
	"math/big"
)

func scalarByteLen(q *big.Int) int {
	return (q.BitLen() + 7) / 8
}

func checkOrder(q *big.Int) error {
	if q == nil || q.Sign() <= 0 {
		return fmt.Errorf("%w: missing or non-positive order", ErrAdapter)
	}
	if !q.ProbablyPrime(32) {
		return fmt.Errorf("%w: order is not a probable prime", ErrAdapter)
	}
	return nil
}

func checkScalar(s, q *big.Int) error {
	if s == nil {
		return fmt.Errorf("%w: nil", ErrInvalidScalar)
	}
	if s.Sign() < 0 || s.Cmp(q) >= 0 {
		return fmt.Errorf("%w: %s is outside [0,q)", ErrInvalidScalar, s.String())
	}
	return nil
}

func checkNonZeroScalar(s, q *big.Int) error {
	if err := checkScalar(s, q); err != nil {
		return err
	}
	if s.Sign() == 0 {
		return fmt.Errorf("%w: scalar must be non-zero", ErrZeroScalar)
	}
	return nil
}

func parseScalar(raw []byte, q *big.Int, nonzero bool) (*big.Int, error) {
	want := scalarByteLen(q)
	if len(raw) != want {
		return nil, fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidScalar, want, len(raw))
	}
	s := new(big.Int).SetBytes(raw)
	if nonzero {
		if err := checkNonZeroScalar(s, q); err != nil {
			return nil, err
		}
	} else if err := checkScalar(s, q); err != nil {
		return nil, err
	}
	return s, nil
}

func ParseScalar(raw []byte, q *big.Int) (*big.Int, error) {
	return parseScalar(raw, q, false)
}

func ParseNonZeroScalar(raw []byte, q *big.Int) (*big.Int, error) {
	return parseScalar(raw, q, true)
}

func encodeScalar(s, q *big.Int) ([]byte, error) {
	if err := checkScalar(s, q); err != nil {
		return nil, err
	}
	out := make([]byte, scalarByteLen(q))
	s.FillBytes(out)
	return out, nil
}

func addMod(a, b, q *big.Int) *big.Int {
	return new(big.Int).Mod(new(big.Int).Add(a, b), q)
}

func mulMod(a, b, q *big.Int) *big.Int {
	return new(big.Int).Mod(new(big.Int).Mul(a, b), q)
}

func invMod(s, q *big.Int) (*big.Int, error) {
	if err := checkNonZeroScalar(s, q); err != nil {
		return nil, err
	}
	inv := new(big.Int).ModInverse(new(big.Int).Set(s), q)
	if inv == nil {
		return nil, fmt.Errorf("%w: no inverse", ErrZeroScalar)
	}
	return inv, nil
}
