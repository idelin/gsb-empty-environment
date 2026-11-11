package netsec

import (
	"fmt"
	"math/big"
	"slices"
)

// verifyEnumeration cross-checks the decision diagram against exhaustive
// enumeration of the whole Cartesian product. It is only invoked for
// small domains and treats any divergence as an internal error, since the
// two computation paths are independent.
func verifyEnumeration(spec Spec, pol Policy, forbidden []Pattern, eng *mdd, leak int, res *Result) error {
	domIndex, err := validateSpec(spec)
	if err != nil {
		return err
	}
	allowSets := make([][]map[int]bool, len(pol.Allow))
	for i, p := range pol.Allow {
		if allowSets[i], err = resolvePattern(spec, domIndex, p, "allow"); err != nil {
			return err
		}
	}
	denySets := make([][]map[int]bool, len(pol.Deny))
	for i, p := range pol.Deny {
		if denySets[i], err = resolvePattern(spec, domIndex, p, "deny"); err != nil {
			return err
		}
	}
	forbSets := make([][]map[int]bool, len(forbidden))
	for i, p := range forbidden {
		if forbSets[i], err = resolvePattern(spec, domIndex, p, "forbidden"); err != nil {
			return err
		}
	}

	k := len(spec.Segments)
	idx := make([]int, k)
	count := new(big.Int)
	var firstWitness []int
	for {
		visible := matchesAny(allowSets, idx) && !matchesAny(denySets, idx)
		want := visible && matchesAny(forbSets, idx)
		if got := eng.contains(leak, idx); got != want {
			return fmt.Errorf("netsec: internal error: diagram and enumeration disagree at tuple %v", tupleStrings(spec, idx))
		}
		if want {
			count.Add(count, big.NewInt(1))
			if firstWitness == nil {
				firstWitness = slices.Clone(idx)
			}
		}
		if !nextTuple(spec, idx) {
			break
		}
	}
	if count.Cmp(res.Count) != 0 {
		return fmt.Errorf("netsec: internal error: diagram count %s, enumeration count %s", res.Count, count)
	}
	if firstWitness == nil {
		if res.Witness != nil {
			return fmt.Errorf("netsec: internal error: diagram witness %v but enumeration found none", res.Witness)
		}
		return nil
	}
	if res.Witness == nil || !slices.Equal(tupleStrings(spec, firstWitness), res.Witness) {
		return fmt.Errorf("netsec: internal error: diagram witness %v, enumeration witness %v",
			res.Witness, tupleStrings(spec, firstWitness))
	}
	return nil
}

// matchesAny reports whether any compiled pattern accepts the tuple.
func matchesAny(patterns [][]map[int]bool, idx []int) bool {
	for _, sets := range patterns {
		ok := true
		for lvl, set := range sets {
			if !set[idx[lvl]] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// nextTuple advances idx like an odometer in lexicographic domain order
// and reports whether it wrapped around.
func nextTuple(spec Spec, idx []int) bool {
	for lvl := len(idx) - 1; lvl >= 0; lvl-- {
		idx[lvl]++
		if idx[lvl] < len(spec.Segments[lvl].Domain) {
			return true
		}
		idx[lvl] = 0
	}
	return false
}

func tupleStrings(spec Spec, idx []int) []string {
	out := make([]string, len(idx))
	for i, v := range idx {
		out[i] = spec.Segments[i].Domain[v]
	}
	return out
}
