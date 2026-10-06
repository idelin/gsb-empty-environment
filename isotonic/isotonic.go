// Package isotonic computes the non-decreasing least-squares fit
// (isotonic regression) of ordered rational observations with
// positive integer weights, using exact rational arithmetic.
//
// The fit is computed with the pool-adjacent-violators algorithm.
// The result is reported as maximal blocks of consecutive
// observations that share one fitted value; adjacent blocks always
// have strictly increasing values, so equal-valued runs are merged.
package isotonic

import (
	"fmt"
	"math/big"
)

// Observation is a single ordered observation.
type Observation struct {
	Value  *big.Rat // observed value
	Weight *big.Int // positive integer weight
}

// Block is a maximal run of consecutive observations sharing one
// fitted value.
type Block struct {
	Start int      // index of the first observation in the block (inclusive)
	End   int      // index of the last observation in the block (inclusive)
	Value *big.Rat // fitted value, in lowest terms
}

// Fit returns the blocks of the non-decreasing weighted least-squares
// fit of obs. Observations must be given in their fit order. Every
// observation needs a non-nil Value and a positive non-nil Weight.
func Fit(obs []Observation) ([]Block, error) {
	for i, o := range obs {
		if o.Value == nil {
			return nil, fmt.Errorf("isotonic: observation %d has nil Value", i)
		}
		if o.Weight == nil || o.Weight.Sign() <= 0 {
			return nil, fmt.Errorf("isotonic: observation %d has non-positive weight", i)
		}
	}

	type pool struct {
		start, end int
		weight     *big.Int
		sum        *big.Rat // sum of Weight*Value over the pool
	}
	value := func(p pool) *big.Rat {
		return new(big.Rat).Quo(p.sum, new(big.Rat).SetInt(p.weight))
	}

	pools := make([]pool, 0, len(obs))
	for i, o := range obs {
		sum := new(big.Rat).Mul(o.Value, new(big.Rat).SetInt(o.Weight))
		pools = append(pools, pool{i, i, new(big.Int).Set(o.Weight), sum})
		// Merge while the previous pool's mean is not strictly
		// smaller than the new last pool's mean. Merging on
		// equality guarantees that adjacent output blocks never
		// share a value.
		for len(pools) >= 2 {
			curr := pools[len(pools)-1]
			prev := pools[len(pools)-2]
			if value(prev).Cmp(value(curr)) < 0 {
				break
			}
			pools = pools[:len(pools)-2]
			pools = append(pools, pool{
				start:  prev.start,
				end:    curr.end,
				weight: new(big.Int).Add(prev.weight, curr.weight),
				sum:    new(big.Rat).Add(prev.sum, curr.sum),
			})
		}
	}

	blocks := make([]Block, len(pools))
	for i, p := range pools {
		blocks[i] = Block{Start: p.start, End: p.end, Value: value(p)}
	}
	return blocks, nil
}

// Values expands blocks into one fitted value per observation.
// The length of the result is one past the last covered index.
func Values(blocks []Block) []*big.Rat {
	n := 0
	for _, b := range blocks {
		if b.End+1 > n {
			n = b.End + 1
		}
	}
	values := make([]*big.Rat, n)
	for _, b := range blocks {
		for i := b.Start; i <= b.End; i++ {
			values[i] = new(big.Rat).Set(b.Value)
		}
	}
	return values
}

// Loss returns the weighted squared error of a fit, exactly:
// sum over observations of Weight*(Value - fitted)^2.
func Loss(obs []Observation, fitted []*big.Rat) (*big.Rat, error) {
	if len(fitted) != len(obs) {
		return nil, fmt.Errorf("isotonic: got %d fitted values for %d observations", len(fitted), len(obs))
	}
	total := new(big.Rat)
	for i, o := range obs {
		if o.Value == nil || o.Weight == nil || o.Weight.Sign() <= 0 {
			return nil, fmt.Errorf("isotonic: invalid observation %d", i)
		}
		if fitted[i] == nil {
			return nil, fmt.Errorf("isotonic: nil fitted value %d", i)
		}
		d := new(big.Rat).Sub(o.Value, fitted[i])
		d.Mul(d, d)
		d.Mul(d, new(big.Rat).SetInt(o.Weight))
		total.Add(total, d)
	}
	return total, nil
}
