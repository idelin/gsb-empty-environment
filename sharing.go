package netsec

import (
	"fmt"
	"io"
)

// AdditiveShares is a three-party additive sharing of a field element:
// Sh0 + Sh1 + Sh2 = secret (mod p).
type AdditiveShares [3]uint32

// SplitSecret creates a fresh three-party additive sharing of x. The first two
// shares are uniform; the third is determined so that the sum equals x.
func (f Field) SplitSecret(x uint32, rng io.Reader) (AdditiveShares, error) {
	if !f.Valid(x) {
		return AdditiveShares{}, fmt.Errorf("%w: x=%d", ErrInvalidShare, x)
	}
	s0, err := f.Random(rng)
	if err != nil {
		return AdditiveShares{}, err
	}
	s1, err := f.Random(rng)
	if err != nil {
		return AdditiveShares{}, err
	}
	s2 := f.Sub(x, f.Add(s0, s1))
	shares := AdditiveShares{s0, s1, s2}
	if err := f.ValidateShares(shares); err != nil {
		return AdditiveShares{}, err
	}
	return shares, nil
}

// SplitValue derives a fresh additive sharing of a triple component value.
// It is the same operation as SplitSecret and exists to make fixture code clear.
func (f Field) SplitValue(x uint32, rng io.Reader) (AdditiveShares, error) {
	return f.SplitSecret(x, rng)
}

// Recombine verifies and sums an additive sharing.
func (f Field) Recombine(shares AdditiveShares) (uint32, error) {
	if err := f.ValidateShares(shares); err != nil {
		return 0, err
	}
	return f.Sum(shares[0], shares[1], shares[2]), nil
}

// ValidateShares checks that all three shares are canonical field elements.
func (f Field) ValidateShares(shares AdditiveShares) error {
	for i, s := range shares {
		if !f.Valid(s) {
			return fmt.Errorf("%w: share[%d]=%d", ErrInvalidShare, i, s)
		}
	}
	return nil
}
