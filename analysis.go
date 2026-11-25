package replayhandoff

import (
	"errors"
	"fmt"
)

// SegmentReport contains both the current decision and the decision after the
// globally selected supplementary pages are fetched.
type SegmentReport struct {
	Index             int
	Segment           QuerySegment
	Decision          Decision
	Parts             []PartDecision
	ProjectedDecision Decision
	ProjectedParts    []PartDecision
}

// BatchReport is the result of one offline batch analysis.
type BatchReport struct {
	Segments        []SegmentReport
	Supplement      []PageSelection
	SupplementCost  int64
	Feasible        bool
	FullyDetermined bool
	LeaseBlocked    bool
	Conflicts       []RootConflict
	Normalization   NormalizationReport
	Certificate     *CoverageCertificate
}

// AnalyzeBatch decides arbitrary half-open query segments, finds the globally
// minimum-cost supplementary page set, and emits a coverage certificate.
func (h *Handoff) AnalyzeBatch(queries []QuerySegment) (*BatchReport, error) {
	return h.analyzeBatch(queries, true)
}

// AnalyzeIntervals is the single-partition convenience form of AnalyzeBatch.
func (h *Handoff) AnalyzeIntervals(partition Partition, ranges []Interval) (*BatchReport, error) {
	queries := make([]QuerySegment, 0, len(ranges))
	for _, bounds := range ranges {
		queries = append(queries, QuerySegment{Partition: partition, Range: bounds})
	}
	return h.AnalyzeBatch(queries)
}

func (h *Handoff) analyzeBatch(queries []QuerySegment, emitCertificate bool) (*BatchReport, error) {
	for i, query := range queries {
		if err := query.Partition.Validate(); err != nil {
			return nil, fmt.Errorf("replayhandoff: query %d: %w", i, err)
		}
		if err := query.Range.Validate(); err != nil {
			return nil, fmt.Errorf("replayhandoff: query %d: %w", i, err)
		}
	}
	report := &BatchReport{
		Conflicts:     cloneNormalization(h.report).Conflicts,
		Normalization: cloneNormalization(h.report),
		Feasible:      true,
	}

	knownProofsByPartition := make(map[Partition][]RangeProof)
	partialReports := make([]SegmentReport, len(queries))
	reqs := make([]requirement, 0)
	leaseBlocked := false
	for i, query := range queries {
		proofs := h.proofsFor(query.Partition, h.known)
		knownProofsByPartition[query.Partition] = proofs
		currentParts := h.analyzeParts(query.Partition, query.Range, proofs, false)
		candidateParts := h.analyzeParts(query.Partition, query.Range, proofs, true)
		segment := SegmentReport{
			Index:    i,
			Segment:  query,
			Decision: AggregateParts(currentParts),
			Parts:    currentParts,
		}
		partialReports[i] = segment
		for _, part := range candidateParts {
			if len(part.OpenLeases) > 0 {
				leaseBlocked = true
			}
			if part.Decision == DecisionWait && len(part.MissingNodes) > 0 {
				reqs = append(reqs, h.requirementFor(query.Partition, part.Interval, part.MissingNodes))
			}
		}
	}

	selected, cost, feasible := h.minimumPages(reqs)
	report.Supplement = selected
	report.SupplementCost = cost
	report.Feasible = feasible
	report.LeaseBlocked = leaseBlocked

	selectedProofs := h.selectedProofs(selected)
	finalProofsByPartition := make(map[Partition][]RangeProof)
	for _, query := range queries {
		if _, exists := finalProofsByPartition[query.Partition]; exists {
			continue
		}
		combined := append([]RangeProof(nil), knownProofsByPartition[query.Partition]...)
		combined = append(combined, selectedProofs[query.Partition]...)
		finalProofsByPartition[query.Partition] = h.proofsFor(query.Partition, combined)
	}

	finalSegments := make([]SegmentReport, 0, len(queries))
	fullyDetermined := true
	for i, query := range queries {
		parts := h.analyzeParts(query.Partition, query.Range, finalProofsByPartition[query.Partition], true)
		projected := AggregateParts(parts)
		for _, part := range parts {
			if part.Decision == DecisionWait {
				fullyDetermined = false
			}
			if len(part.OpenLeases) > 0 {
				leaseBlocked = true
			}
		}
		current := partialReports[i]
		current.ProjectedDecision = projected
		current.ProjectedParts = parts
		finalSegments = append(finalSegments, current)
	}
	report.Segments = finalSegments
	report.LeaseBlocked = leaseBlocked
	report.FullyDetermined = fullyDetermined && feasible

	if emitCertificate {
		cert := h.newCoverageCertificate(queries, selected, finalSegments, report)
		report.Certificate = &cert
	}
	return report, nil
}

func (h *Handoff) selectedProofs(selected []PageSelection) map[Partition][]RangeProof {
	out := make(map[Partition][]RangeProof)
	selectedIDs := make(map[string]struct{}, len(selected))
	for _, page := range selected {
		selectedIDs[page.ID] = struct{}{}
	}
	for _, page := range h.pages {
		if _, ok := selectedIDs[page.ID]; !ok {
			continue
		}
		for _, proof := range page.Proofs {
			out[proof.Partition] = append(out[proof.Partition], proof)
		}
	}
	for partition := range out {
		sortProofs(out[partition])
	}
	return out
}

// DecisionAt returns the projected per-sequence decision from a batch report.
// It is O(number of atomic parts), never O(2^40).
func (r *BatchReport) DecisionAt(index int, seq Seq) (PartDecision, error) {
	if index < 0 || index >= len(r.Segments) {
		return PartDecision{}, errors.New("replayhandoff: query index out of range")
	}
	segment := r.Segments[index]
	for _, part := range segment.ProjectedParts {
		if part.Interval.Contains(seq) {
			return part, nil
		}
	}
	return PartDecision{}, errors.New("replayhandoff: sequence is outside the query segment")
}
