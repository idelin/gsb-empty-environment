package netsec

import "math/bits"

// SubsetResult records the identification rates of one plugin combination.
type SubsetResult struct {
	Mask          uint32
	PluginCount   int
	BayesRate     float64
	SingletonRate float64
}

// Exhaustive enumerates all 2^n plugin combinations (n <= MaxPlugins) and
// reports both identification rates for each. Results are ordered by mask so
// the empty combination comes first.
func (a *Analysis) Exhaustive() []SubsetResult {
	results := make([]SubsetResult, 0, 1<<uint(len(a.plugins)))
	a.exhaustiveWalk(0, 0, &results)
	return results
}

// exhaustiveWalk visits every subset once in increasing mask order; each
// subset rebuilds its composite label keys in O(m) where m is the domain
// size, so the full table costs O(2^n * m).
func (a *Analysis) exhaustiveWalk(mask uint32, next int, results *[]SubsetResult) {
	bayes, singleton := a.rates(mask)
	*results = append(*results, SubsetResult{
		Mask:          mask,
		PluginCount:   bits.OnesCount32(mask),
		BayesRate:     bayes,
		SingletonRate: singleton,
	})
	for plugin := next; plugin < len(a.plugins); plugin++ {
		a.exhaustiveWalk(mask|1<<uint(plugin), plugin+1, results)
	}
}

// CheckMonotonic enumerates every subset and verifies that adding a plugin can
// never decrease the Bayes identification rate: refinement only splits
// classes, and the optimal one-shot guess of a refinement dominates that of
// the coarser partition. The singleton rate is deliberately excluded because
// splitting a class can turn a unique best guess into a non-singleton cell.
// An empty return value means the property holds everywhere.
func (a *Analysis) CheckMonotonic() []string {
	results := a.Exhaustive()
	rateByMask := make(map[uint32]float64, len(results))
	for _, result := range results {
		rateByMask[result.Mask] = result.BayesRate
	}

	var violations []string
	for _, result := range results {
		mask := result.Mask
		for plugin := 0; plugin < len(a.plugins); plugin++ {
			bit := uint32(1) << uint(plugin)
			if mask&bit != 0 {
				continue
			}
			extended := mask | bit
			if rateByMask[extended]+epsilon < result.BayesRate {
				violations = append(violations,
					"rate decreased when adding plugin "+a.plugins[plugin].Name+
						" to mask "+maskName(a, mask))
			}
		}
	}
	return violations
}

// VerifyReport summarizes the independent cross-checks between the map-based
// refinement, the per-secret pairwise enumeration and the full subset table.
type VerifyReport struct {
	// Subsets is the number of plugin combinations that were compared.
	Subsets int
	// Secrets is the domain size used in the pairwise enumeration.
	Secrets int
	// Mismatches lists every discrepancy; an empty slice means agreement.
	Mismatches []string
}

// OK reports whether every cross-check agreed.
func (r VerifyReport) OK() bool { return len(r.Mismatches) == 0 }

// Verify cross-checks the analysis three independent ways:
//
//  1. full subset enumeration: every mask's key-grouping partition is rebuilt
//     with the per-secret pairwise union-find enumeration and classes, masses
//     and both rates must match exactly;
//  2. per-secret enumeration: each secret's equivalence class is derived by
//     scanning all other secrets and comparing label vectors directly;
//  3. monotonicity: every superset Bayes rate must dominate every subset
//     Bayes rate.
func (a *Analysis) Verify() VerifyReport {
	report := VerifyReport{Secrets: len(a.secrets)}
	results := a.Exhaustive()
	report.Subsets = len(results)

	for _, result := range results {
		mask := result.Mask
		direct := a.Refine(mask)
		pairwise := a.pairwiseRefine(mask)

		if !partitionsEqual(direct, pairwise) {
			report.Mismatches = append(report.Mismatches,
				"refinement mismatch at "+maskName(a, mask))
		}
		if !floatsEqual(direct.BayesRate, result.BayesRate) ||
			!floatsEqual(direct.SingletonRate, result.SingletonRate) {
			report.Mismatches = append(report.Mismatches,
				"exhaustive rate mismatch at "+maskName(a, mask))
		}

		for _, class := range direct.Classes {
			for _, secret := range class.Members {
				expected := a.enumerateClass(mask, secret)
				if !intSetsEqual(expected, class.Members) {
					report.Mismatches = append(report.Mismatches,
						"per-secret class mismatch at "+maskName(a, mask)+
							" for secret "+a.secrets[secret])
				}
			}
		}
	}

	report.Mismatches = append(report.Mismatches, a.CheckMonotonic()...)
	return report
}

// enumerateClass independently determines the cell of one secret by scanning
// every other secret and comparing their label vectors plugin by plugin.
func (a *Analysis) enumerateClass(mask uint32, secret int) []int {
	members := []int{}
	for other := 0; other < len(a.secrets); other++ {
		same := true
		for remainder := mask; remainder != 0; remainder &= remainder - 1 {
			plugin := bits.TrailingZeros32(remainder)
			if a.ids[plugin][secret] != a.ids[plugin][other] {
				same = false
				break
			}
		}
		if same {
			members = append(members, other)
		}
	}
	return members
}

func partitionsEqual(left, right *Partition) bool {
	if left.Mask != right.Mask || len(left.Classes) != len(right.Classes) {
		return false
	}
	for idx := range left.Classes {
		l, r := left.Classes[idx], right.Classes[idx]
		if !intSetsEqual(l.Members, r.Members) ||
			!floatsEqual(l.Mass, r.Mass) ||
			!floatsEqual(l.GuessMass, r.GuessMass) ||
			l.Guess != r.Guess {
			return false
		}
	}
	if !floatsEqual(left.BayesRate, right.BayesRate) ||
		!floatsEqual(left.SingletonRate, right.SingletonRate) {
		return false
	}
	return true
}

func intSetsEqual(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for idx := range left {
		if left[idx] != right[idx] {
			return false
		}
	}
	return true
}

func floatsEqual(left, right float64) bool {
	delta := left - right
	if delta < 0 {
		delta = -delta
	}
	return delta <= epsilon
}

func maskName(a *Analysis, mask uint32) string {
	names := a.PluginNames(mask)
	if len(names) == 0 {
		return "empty combination"
	}
	name := "{"
	for idx, plugin := range names {
		if idx > 0 {
			name += ","
		}
		name += plugin
	}
	return name + "}"
}
