package logtmpl

import (
	"sort"
)

// Recluster is the independent offline reference implementation, intended
// for small streams. It canonicalizes the input (sort + dedupe with
// counts), then runs deterministic agglomerative clustering: repeatedly
// merge the closest compatible pair until no pair is within threshold.
// It shares only the token/distance/merge-policy primitives with the
// online Clusterer, not its control flow, so it can cross-check results.
func Recluster(lines []string, cfg Config) []*Template {
	cfg = cfg.withDefaults()
	counts := map[string]int{}
	for _, l := range lines {
		counts[l]++
	}
	uniq := make([]string, 0, len(counts))
	for l := range counts {
		uniq = append(uniq, l)
	}
	sort.Strings(uniq) // canonical order: input order cannot leak in

	templates := make([]*Template, 0, len(uniq))
	for i, l := range uniq {
		t := templateFromTokens(Tokenize(l), i)
		t.Count = counts[l]
		t.Examples = []string{l}
		templates = append(templates, t)
	}

	for {
		bestD := cfg.MergeThreshold + 1
		bi, bj := -1, -1
		var bestM *Template
		for x := 0; x < len(templates); x++ {
			for y := x + 1; y < len(templates); y++ {
				a, b := templates[x], templates[y]
				d, ok := Distance(a, b)
				if !ok || d > cfg.MergeThreshold {
					continue
				}
				m, _, _, ok := mergeTemplates(a, b)
				if !ok || m.WildRatio() > cfg.MaxWildcardRatio {
					continue
				}
				// Deterministic tie-break: (distance, sigA, sigB).
				if d > bestD {
					continue
				}
				if d == bestD && bi >= 0 {
					curA, curB := templates[bi].Signature(), templates[bj].Signature()
					sigA, sigB := a.Signature(), b.Signature()
					if sigA > curA || sigA == curA && sigB >= curB {
						continue
					}
				}
				bestD, bi, bj, bestM = d, x, y, m
			}
		}
		if bi < 0 {
			break
		}
		bestM.Examples = mergeExamples(templates[bi].Examples, templates[bj].Examples, cfg.MaxExamples)
		bestM.ID = templates[bi].ID
		templates[bi] = bestM
		templates = append(templates[:bj], templates[bj+1:]...)
	}
	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Signature() < templates[j].Signature()
	})
	return templates
}

// Signatures returns the sorted signatures of a template set, the canonical
// form used for determinism comparisons.
func Signatures(ts []*Template) []string {
	sigs := make([]string, 0, len(ts))
	for _, t := range ts {
		sigs = append(sigs, t.Signature())
	}
	sort.Strings(sigs)
	return sigs
}

// PartitionOf maps each line to the signature of its best-matching
// template. ok is false if any line matches no template or matches two
// templates ambiguously.
func PartitionOf(ts []*Template, lines []string) (map[string]string, bool) {
	out := map[string]string{}
	for _, line := range lines {
		cand := templateFromTokens(Tokenize(line), -1)
		bestD, bestSig, ambiguous := -1, "", false
		for _, t := range ts {
			d, ok := Distance(t, cand)
			if !ok {
				continue
			}
			if bestD < 0 || d < bestD {
				bestD, bestSig, ambiguous = d, t.Signature(), false
			} else if d == bestD && t.Signature() != bestSig {
				ambiguous = true
			}
		}
		if bestD < 0 || ambiguous {
			return nil, false
		}
		out[line] = bestSig
	}
	return out, true
}
