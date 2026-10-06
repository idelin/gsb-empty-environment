package permtest

import (
	"errors"
	"fmt"
	"math/big"
)

// Block is the randomization unit of a stratified design: all samples in a
// block switch group together. Treated records the observed assignment.
type Block struct {
	Values  []float64
	Treated bool
}

// Stratum is one randomization layer. Its blocks are permuted only among
// themselves, keeping the observed number of treated blocks fixed, and the
// stratum's mean difference contributes Weight to the overall statistic.
type Stratum struct {
	Blocks []Block
	Weight *big.Rat
}

// StratumEvidence documents how many legal assignments one stratum
// contributes to the enumeration.
type StratumEvidence struct {
	Blocks        int
	TreatedBlocks int
	ControlBlocks int
	Samples       int
	Assignments   *big.Int // C(Blocks, TreatedBlocks)
}

// StratifiedResult reports the exact two-sided p-value of the weighted sum
// of per-stratum mean differences, plus the enumeration evidence.
type StratifiedResult struct {
	// ObservedStat is sum_s Weight_s * (treatedMean_s - controlMean_s).
	ObservedStat *big.Rat
	// Tail is the number of legal assignments with |stat| >= |ObservedStat|.
	Tail *big.Int
	// Total is the number of legal assignments, the product of the
	// per-stratum binomial counts.
	Total *big.Int
	// PValue is Tail/Total, already in lowest terms.
	PValue *big.Rat
	// Evidence holds one entry per stratum, in input order.
	Evidence []StratumEvidence
}

// NumDen returns the reduced numerator and denominator of the p-value.
func (r *StratifiedResult) NumDen() (num, den *big.Int) {
	return r.PValue.Num(), r.PValue.Denom()
}

// StratifiedExact enumerates every legal randomization of a stratified
// blocked design and returns the exact two-sided p-value. Only assignments
// that keep each stratum's treated-block count at its observed level are
// enumerated; duplicate values are kept distinct by position.
func StratifiedExact(strata []Stratum) (*StratifiedResult, error) {
	if len(strata) == 0 {
		return nil, errors.New("permtest: at least one stratum is required")
	}
	// Per stratum: exact rational values, treated count, and diff as a
	// function of which blocks are treated.
	type stratumData struct {
		weight    *big.Rat
		blockSums []*big.Rat // sum of values per block
		blockLens []int64
		treated   int
		nT        int64 // treated samples under the observed assignment
		nC        int64
	}
	ds := make([]stratumData, len(strata))
	evidence := make([]StratumEvidence, len(strata))
	for s, st := range strata {
		if st.Weight == nil || st.Weight.Sign() == 0 {
			return nil, fmt.Errorf("permtest: stratum %d needs a non-zero rational weight", s)
		}
		if len(st.Blocks) < 2 {
			return nil, fmt.Errorf("permtest: stratum %d needs at least two blocks", s)
		}
		d := stratumData{weight: new(big.Rat).Set(st.Weight)}
		samples := 0
		for b, bl := range st.Blocks {
			if len(bl.Values) == 0 {
				return nil, fmt.Errorf("permtest: stratum %d block %d is empty", s, b)
			}
			sum := new(big.Rat)
			for _, v := range bl.Values {
				sum.Add(sum, new(big.Rat).SetFloat64(v))
			}
			d.blockSums = append(d.blockSums, sum)
			d.blockLens = append(d.blockLens, int64(len(bl.Values)))
			samples += len(bl.Values)
			if bl.Treated {
				d.treated++
				d.nT += int64(len(bl.Values))
			} else {
				d.nC += int64(len(bl.Values))
			}
		}
		if d.treated == 0 || d.treated == len(st.Blocks) {
			return nil, fmt.Errorf("permtest: stratum %d must have both treated and control blocks", s)
		}
		ds[s] = d
		assign := new(big.Int).Binomial(int64(len(st.Blocks)), int64(d.treated))
		evidence[s] = StratumEvidence{
			Blocks:        len(st.Blocks),
			TreatedBlocks: d.treated,
			ControlBlocks: len(st.Blocks) - d.treated,
			Samples:       samples,
			Assignments:   assign,
		}
	}

	// stat(choice) = sum_s w_s * (tSum_s/nT_s - (blockTot_s-tSum_s)/nC_s)
	// where tSum_s is the sum of values of the chosen treated blocks.
	stat := func(choices [][]int) *big.Rat {
		acc := new(big.Rat)
		for s, idx := range choices {
			d := &ds[s]
			tSum := new(big.Rat)
			for _, b := range idx {
				tSum.Add(tSum, d.blockSums[b])
			}
			tot := new(big.Rat)
			for _, bs := range d.blockSums {
				tot.Add(tot, bs)
			}
			cSum := new(big.Rat).Sub(tot, tSum)
			diff := new(big.Rat).Quo(tSum, new(big.Rat).SetInt64(d.nT))
			diff.Sub(diff, new(big.Rat).Quo(cSum, new(big.Rat).SetInt64(d.nC)))
			acc.Add(acc, new(big.Rat).Mul(d.weight, diff))
		}
		return acc
	}

	// Observed assignment: the blocks flagged Treated, in order.
	observedChoice := make([][]int, len(strata))
	for s, st := range strata {
		for b, bl := range st.Blocks {
			if bl.Treated {
				observedChoice[s] = append(observedChoice[s], b)
			}
		}
	}
	observed := stat(observedChoice)
	absObserved := new(big.Rat).Abs(observed)

	tail := big.NewInt(0)
	count := big.NewInt(0)
	one := big.NewInt(1)
	choices := make([][]int, len(strata))
	var walk func(s int)
	walk = func(s int) {
		if s == len(strata) {
			st := stat(choices)
			if new(big.Rat).Abs(st).Cmp(absObserved) >= 0 {
				tail.Add(tail, one)
			}
			count.Add(count, one)
			return
		}
		forEachCombination(len(strata[s].Blocks), ds[s].treated, func(idx []int) {
			cp := make([]int, len(idx))
			copy(cp, idx)
			choices[s] = cp
			walk(s + 1)
		})
	}
	walk(0)

	return &StratifiedResult{
		ObservedStat: observed,
		Tail:         tail,
		Total:        count,
		PValue:       new(big.Rat).SetFrac(tail, count),
		Evidence:     evidence,
	}, nil
}
