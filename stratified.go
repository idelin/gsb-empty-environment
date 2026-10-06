package permtest

import (
	"errors"
	"fmt"
	"math/big"
)

// Cluster is a set of samples that randomization moves between arms as a
// single unit.
type Cluster struct {
	Values []float64
}

// Stratum is one stratum of a stratified cluster-randomized design.
//
// The first Treated clusters are the ones actually treated (the observed
// assignment); the enumeration then covers every legal reassignment that
// keeps exactly Treated treated clusters in the stratum, moving clusters as
// whole units.
type Stratum struct {
	Clusters []Cluster
	Treated  int
	// Weight is the rational weight of this stratum in the statistic.
	// A nil Weight uses (samples in stratum)/(samples overall).
	Weight *big.Rat
}

// StratumEvidence records how many legal assignments a stratum contributes,
// as evidence for the stratified enumeration count.
type StratumEvidence struct {
	Clusters    int
	Treated     int
	Samples     int
	Assignments int64 // C(Clusters, Treated)
	Weight      *big.Rat
}

// StratifiedResult is the outcome of StratifiedCluster.
type StratifiedResult struct {
	P        Fraction          // exact two-sided tail probability
	Total    int64             // number of legal assignments enumerated
	Observed *big.Rat          // observed statistic
	Strata   []StratumEvidence // per-stratum assignment counts
}

// StratifiedCluster runs an exact randomization test for a stratified,
// cluster-randomized design. The statistic is
//
//	T = sum_s w_s * (mean of treated samples in s - mean of control samples in s)
//
// with rational weights w_s. Only legal assignments are enumerated: exactly
// Treated treated clusters per stratum, clusters moved as whole units, and
// duplicate values distinguished by sample position. The tail is
// |T| >= |T_observed|.
func StratifiedCluster(strata []Stratum) (StratifiedResult, error) {
	if len(strata) == 0 {
		return StratifiedResult{}, errors.New("permtest: need at least one stratum")
	}
	type stratumData struct {
		sums    []*big.Rat // per-cluster sample sums
		sizes   []int64    // per-cluster sample counts
		treated int
		weight  *big.Rat
		total   *big.Rat // sum of all samples in the stratum
		n       int64    // number of samples in the stratum
	}
	data := make([]stratumData, len(strata))
	evidence := make([]StratumEvidence, len(strata))
	grandN := int64(0)
	for s, st := range strata {
		if len(st.Clusters) < 2 {
			return StratifiedResult{}, fmt.Errorf("permtest: stratum %d needs at least two clusters", s)
		}
		if st.Treated <= 0 || st.Treated >= len(st.Clusters) {
			return StratifiedResult{}, fmt.Errorf("permtest: stratum %d: Treated must be between 1 and %d", s, len(st.Clusters)-1)
		}
		d := stratumData{treated: st.Treated, total: new(big.Rat)}
		for c, cl := range st.Clusters {
			if len(cl.Values) == 0 {
				return StratifiedResult{}, fmt.Errorf("permtest: stratum %d cluster %d is empty", s, c)
			}
			sum := new(big.Rat)
			for _, v := range cl.Values {
				r, err := rat(v)
				if err != nil {
					return StratifiedResult{}, err
				}
				sum.Add(sum, r)
			}
			d.sums = append(d.sums, sum)
			d.sizes = append(d.sizes, int64(len(cl.Values)))
			d.total.Add(d.total, sum)
			d.n += int64(len(cl.Values))
		}
		grandN += d.n
		data[s] = d
	}
	for s, st := range strata {
		w := st.Weight
		if w == nil {
			w = big.NewRat(data[s].n, grandN)
		}
		data[s].weight = w
		evidence[s] = StratumEvidence{
			Clusters:    len(st.Clusters),
			Treated:     st.Treated,
			Samples:     int(data[s].n),
			Assignments: binomial(len(st.Clusters), st.Treated),
			Weight:      w,
		}
	}

	// contribution returns w_s * (treated mean - control mean) for the
	// stratum when the clusters at positions idx are treated.
	contribution := func(s int, idx []int) *big.Rat {
		d := &data[s]
		ts := new(big.Rat)
		tn := int64(0)
		for _, i := range idx {
			ts.Add(ts, d.sums[i])
			tn += d.sizes[i]
		}
		cs := new(big.Rat).Sub(d.total, ts)
		cn := d.n - tn
		diff := new(big.Rat).Quo(ts, big.NewRat(tn, 1))
		diff.Sub(diff, new(big.Rat).Quo(cs, big.NewRat(cn, 1)))
		return diff.Mul(diff, d.weight)
	}

	// Observed assignment: the first Treated clusters of each stratum.
	obs := new(big.Rat)
	for s := range data {
		idx := make([]int, data[s].treated)
		for i := range idx {
			idx[i] = i
		}
		obs.Add(obs, contribution(s, idx))
	}
	absObs := new(big.Rat).Abs(obs)

	// Enumerate the product of the per-stratum legal assignments, adding
	// each stratum's contribution to acc as we descend.
	var tail, tot int64
	acc := new(big.Rat)
	var rec func(s int)
	rec = func(s int) {
		if s == len(data) {
			if new(big.Rat).Abs(acc).Cmp(absObs) >= 0 {
				tail++
			}
			tot++
			return
		}
		d := &data[s]
		eachCombination(len(d.sums), d.treated, func(idx []int) {
			c := contribution(s, idx)
			acc.Add(acc, c)
			rec(s + 1)
			acc.Sub(acc, c)
		})
	}
	rec(0)

	return StratifiedResult{
		P:        Fraction{Num: tail, Den: tot},
		Total:    tot,
		Observed: obs,
		Strata:   evidence,
	}, nil
}
