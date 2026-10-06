// Package isotonic computes the non-decreasing least-squares fit
// (isotonic regression) of ordered rational observations with
// positive integer weights, using exact rational arithmetic.
package isotonic

import (
	"errors"
	"math/big"
)

// Block is one maximal constant segment of the fitted values.
// Value is always in lowest terms (big.Rat is normalized).
// Start is inclusive, End is exclusive; both index the original
// observation slice.
type Block struct {
	Value *big.Rat
	Start int
	End   int
}

// Fit returns the non-decreasing weighted least-squares fit of y
// with weights w, as a sequence of blocks with strictly increasing
// values (adjacent equal values are merged). It uses the pool
// adjacent violators algorithm.
func Fit(y []*big.Rat, w []*big.Int) ([]Block, error) {
	if len(y) == 0 {
		return nil, errors.New("isotonic: no observations")
	}
	if len(y) != len(w) {
		return nil, errors.New("isotonic: len(y) != len(w)")
	}
	for i := range y {
		if y[i] == nil {
			return nil, errors.New("isotonic: nil observation")
		}
		if w[i] == nil || w[i].Sign() <= 0 {
			return nil, errors.New("isotonic: weights must be positive integers")
		}
	}

	type acc struct {
		sumW  *big.Rat // sum of weights
		sumWY *big.Rat // sum of weight*observation
		start int
		end   int
	}

	stack := make([]acc, 0, len(y))
	push := func(start, end int) {
		sumW := new(big.Rat)
		sumWY := new(big.Rat)
		tmp := new(big.Rat)
		for i := start; i < end; i++ {
			wi := new(big.Rat).SetInt(w[i])
			sumW.Add(sumW, wi)
			sumWY.Add(sumWY, tmp.Mul(wi, y[i]))
		}
		stack = append(stack, acc{sumW, sumWY, start, end})
	}
	value := func(a acc) *big.Rat {
		return new(big.Rat).Quo(a.sumWY, a.sumW)
	}

	for i := 0; i < len(y); i++ {
		push(i, i+1)
		// Pool adjacent violators: merge while the previous block's
		// mean exceeds the new block's mean.
		for len(stack) >= 2 {
			prev := stack[len(stack)-2]
			curr := stack[len(stack)-1]
			if value(prev).Cmp(value(curr)) <= 0 {
				break
			}
			merged := acc{
				sumW:  new(big.Rat).Add(prev.sumW, curr.sumW),
				sumWY: new(big.Rat).Add(prev.sumWY, curr.sumWY),
				start: prev.start,
				end:   curr.end,
			}
			stack = stack[:len(stack)-2]
			stack = append(stack, merged)
		}
	}

	// Merge adjacent blocks with equal values (can arise when a
	// pooled mean lands exactly on a neighbor's value).
	blocks := make([]Block, 0, len(stack))
	for _, a := range stack {
		v := value(a)
		if n := len(blocks); n > 0 && blocks[n-1].End == a.start && blocks[n-1].Value.Cmp(v) == 0 {
			blocks[n-1].End = a.end
			continue
		}
		blocks = append(blocks, Block{Value: v, Start: a.start, End: a.end})
	}
	return blocks, nil
}

// Loss returns the weighted squared error sum_i w_i (y_i - v_i)^2
// of the fit described by blocks, as an exact rational.
func Loss(y []*big.Rat, w []*big.Int, blocks []Block) *big.Rat {
	total := new(big.Rat)
	d := new(big.Rat)
	tmp := new(big.Rat)
	for _, b := range blocks {
		for i := b.Start; i < b.End; i++ {
			d.Sub(y[i], b.Value)
			tmp.Mul(d, d)
			tmp.Mul(tmp, new(big.Rat).SetInt(w[i]))
			total.Add(total, tmp)
		}
	}
	return total
}

// Values expands blocks into one fitted value per observation.
func Values(n int, blocks []Block) []*big.Rat {
	out := make([]*big.Rat, n)
	for _, b := range blocks {
		for i := b.Start; i < b.End; i++ {
			out[i] = b.Value
		}
	}
	return out
}
