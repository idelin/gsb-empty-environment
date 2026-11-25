package replayhandoff

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
)

// ExhaustiveReport records a small-domain independent check.
type ExhaustiveReport struct {
	SequencesChecked int
	PageSubsets      int
	MinimumCost      int64
	MinimumPages     []string
}

// VerifyCertificateExhaustive is intended for small domains and test/audit
// use. It verifies the certificate normally, then:
//   - enumerates every sequence in [0, domainHi) covered by a query;
//   - compares each sequence with a fresh final-evidence decision;
//   - enumerates every candidate-page subset and recomputes the minimum
//     supplement cost.
//
// It refuses domains/runs that would be too large. Production decisions use
// interval algebra and never enumerate the 40-bit space.
func VerifyCertificateExhaustive(cert *CoverageCertificate, keys KeyRing, domainHi Seq) (ExhaustiveReport, error) {
	var report ExhaustiveReport
	if cert == nil {
		return report, errors.New("replayhandoff: nil certificate")
	}
	if domainHi == 0 || domainHi > SequenceLimit {
		return report, errors.New("replayhandoff: invalid exhaustive domain")
	}
	pageCount := len(cert.Input.CandidatePages)
	if pageCount > MaxCandidatePages {
		return report, errors.New("replayhandoff: too many candidate pages")
	}
	if domainHi > 1<<20 || pageCount >= 63 ||
		(uint64(1)<<uint(pageCount))*uint64(domainHi) > 20_000_000 {
		return report, errors.New("replayhandoff: exhaustive run is too large")
	}
	for i, query := range cert.Input.Queries {
		if query.Range.Hi > domainHi {
			return report, fmt.Errorf("replayhandoff: query %d exceeds exhaustive domain", i)
		}
	}
	if err := VerifyCertificate(cert, keys); err != nil {
		return report, err
	}

	selectedProofs, err := selectedPageProofs(cert)
	if err != nil {
		return report, err
	}
	allProofs := append([]RangeProof(nil), cert.Input.KnownProofs...)
	for _, page := range cert.Input.CandidatePages {
		allProofs = append(allProofs, page.Proofs...)
	}
	oracle, err := NewHandoff(HandoffConfig{
		Nodes:            cert.Input.Nodes,
		NodesByPartition: clonePartitionNodes(cert.Input.NodesByPartition),
		NewNode:          cert.Input.NewNode,
		Leases:           append([]Lease(nil), cert.Input.Leases...),
		LocalSeen:        cloneLocalSeen(cert.Input.LocalSeen),
		KnownProofs:      allProofs,
		Keys:             keys,
	})
	if err != nil {
		return report, err
	}
	finalProofs := append([]RangeProof(nil), cert.Input.KnownProofs...)
	finalProofs = append(finalProofs, selectedProofs...)
	baseProofs := cert.Input.KnownProofs

	for seq := Seq(0); seq < domainHi; seq++ {
		for queryIndex, query := range cert.Input.Queries {
			if !query.Range.Contains(seq) {
				continue
			}
			part, err := certificatePartAt(cert, queryIndex, seq)
			if err != nil {
				return report, err
			}
			final := evaluateWithProofs(oracle, query.Partition, seq, finalProofs)
			if final.Decision != part.Decision {
				return report, fmt.Errorf("replayhandoff: certificate mismatch at seq %d: certificate=%s final=%s", seq, part.Decision, final.Decision)
			}
			report.SequencesChecked++
		}
	}
	if cert.FullyDetermined {
		for seq := Seq(0); seq < domainHi; seq++ {
			for _, query := range cert.Input.Queries {
				if !query.Range.Contains(seq) {
					continue
				}
				final := evaluateWithProofs(oracle, query.Partition, seq, finalProofs)
				if final.Decision == DecisionWait {
					return report, fmt.Errorf("replayhandoff: certificate is not fully determined at seq %d", seq)
				}
			}
		}
	}

	// Every page subset is checked by direct sequence decisions. A subset is
	// feasible if every sequence that was initially waiting is no longer
	// waiting. Already accepted/rejected sequences need no extra page.
	bestCost, bestCount := int64(0), 0
	var bestIDs []string
	bestSet := false
	for mask := 0; mask < (1 << pageCount); mask++ {
		report.PageSubsets++
		cost, ids := subsetPages(cert.Input.CandidatePages, mask)
		valid := true
		subset := subsetProofs(cert.Input.CandidatePages, mask)
		subsetEvidence := append([]RangeProof(nil), baseProofs...)
		subsetEvidence = append(subsetEvidence, subset...)
		for seq := Seq(0); seq < domainHi && valid; seq++ {
			for _, query := range cert.Input.Queries {
				if !query.Range.Contains(seq) {
					continue
				}
				initial := evaluateWithProofs(oracle, query.Partition, seq, baseProofs)
				if initial.Decision != DecisionWait {
					continue
				}
				final := evaluateWithProofs(oracle, query.Partition, seq, subsetEvidence)
				if final.Decision == DecisionWait {
					valid = false
					break
				}
			}
		}
		if !valid {
			continue
		}
		if !bestSet || cost < bestCost || (cost == bestCost && len(ids) < bestCount) ||
			(cost == bestCost && len(ids) == bestCount && lexLess(ids, bestIDs)) {
			bestSet = true
			bestCost = cost
			bestCount = len(ids)
			bestIDs = append([]string(nil), ids...)
		}
	}
	if !bestSet {
		return report, errors.New("replayhandoff: no page subset can determine the query domain")
	}
	if bestCost != cert.SupplementCost || !reflect.DeepEqual(bestIDs, cert.SelectedPages) {
		return report, fmt.Errorf("replayhandoff: minimum page subset mismatch: exhaustive=%v/%d certificate=%v/%d", bestIDs, bestCost, cert.SelectedPages, cert.SupplementCost)
	}
	report.MinimumCost = bestCost
	report.MinimumPages = append([]string(nil), bestIDs...)
	return report, nil
}

func evaluateWithProofs(h *Handoff, partition Partition, seq Seq, proofs []RangeProof) PartDecision {
	atom := Interval{Lo: seq, Hi: seq + 1}
	return h.evaluateAtom(partition, atom, h.proofsFor(partition, proofs))
}

func certificatePartAt(cert *CoverageCertificate, queryIndex int, seq Seq) (*PartDecision, error) {
	if queryIndex < 0 || queryIndex >= len(cert.Segments) {
		return nil, errors.New("replayhandoff: certificate query index out of range")
	}
	for i := range cert.Segments[queryIndex].Parts {
		if cert.Segments[queryIndex].Parts[i].Interval.Contains(seq) {
			return &cert.Segments[queryIndex].Parts[i], nil
		}
	}
	return nil, fmt.Errorf("replayhandoff: certificate has a gap at seq %d", seq)
}

func selectedPageProofs(cert *CoverageCertificate) ([]RangeProof, error) {
	selected := make(map[string]struct{}, len(cert.SelectedPages))
	for _, id := range cert.SelectedPages {
		selected[id] = struct{}{}
	}
	out := make([]RangeProof, 0)
	for _, page := range cert.Input.CandidatePages {
		if _, ok := selected[page.ID]; !ok {
			continue
		}
		delete(selected, page.ID)
		out = append(out, page.Proofs...)
	}
	if len(selected) > 0 {
		return nil, errors.New("replayhandoff: certificate names a missing selected page")
	}
	return out, nil
}

func subsetPages(pages []ProofPage, mask int) (int64, []string) {
	var cost int64
	ids := make([]string, 0)
	for i, page := range pages {
		if mask&(1<<i) == 0 {
			continue
		}
		if page.Cost > 0 {
			cost += page.Cost
		}
		ids = append(ids, page.ID)
	}
	sort.Strings(ids)
	return cost, ids
}

func subsetProofs(pages []ProofPage, mask int) []RangeProof {
	out := make([]RangeProof, 0)
	for i, page := range pages {
		if mask&(1<<i) != 0 {
			out = append(out, page.Proofs...)
		}
	}
	return out
}

func lexLess(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return true
		}
		if a[i] > b[i] {
			return false
		}
	}
	return len(a) < len(b)
}
