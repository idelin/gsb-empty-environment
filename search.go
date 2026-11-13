package netsec

import (
	"fmt"
	"time"
)

const MaxSearchCandidates = 20

// Divergence is a minimal approval set on which the correct policy refuses to
// complete but the pending policy completes the recovery.
type Divergence struct {
	Indices    []int
	Approvals  []Approval
	ContactIDs []ContactID
	Correct    Evaluation
	Pending    Evaluation
}

// FindMinimalDivergence searches approval subsets by increasing cardinality and
// returns the first set where CorrectPolicy cannot complete but pendingPolicy
// can. Within the same cardinality, lower candidate indices win, so the result
// is deterministic.
func FindMinimalDivergence(cfg Config, rec Recovery, candidates []Approval, now time.Time, pendingPolicy EvaluationPolicy) (*Divergence, error) {
	if len(candidates) > MaxSearchCandidates {
		return nil, fmt.Errorf("%w: %d candidates exceeds limit %d", ErrSearchLimit, len(candidates), MaxSearchCandidates)
	}
	normalized, err := normalizeConfig(cfg, cfg.AccountID)
	if err != nil {
		return nil, err
	}
	if rec.AccountID != "" && rec.AccountID != normalized.AccountID {
		return nil, fmt.Errorf("%w: recovery account %q does not match config account %q", ErrInvalidRecoveryID, rec.AccountID, normalized.AccountID)
	}
	if now.IsZero() {
		now = rec.CreatedAt
	}

	for size := 1; size <= len(candidates); size++ {
		indices := make([]int, size)
		var search func(start, depth int) (*Divergence, error)
		search = func(start, depth int) (*Divergence, error) {
			if depth == size {
				subset := make([]Approval, size)
				for i, index := range indices {
					subset[i] = candidates[index].clone()
				}
				correct := EvaluateApprovals(normalized, rec, subset, now, correctPolicy)
				pending := EvaluateApprovals(normalized, rec, subset, now, pendingPolicy)
				if !correct.CanComplete && pending.CanComplete {
					contactIDs := append([]ContactID(nil), pending.AcceptedContacts...)
					return &Divergence{
						Indices:    append([]int(nil), indices...),
						Approvals:  subset,
						ContactIDs: contactIDs,
						Correct:    correct,
						Pending:    pending,
					}, nil
				}
				return nil, nil
			}
			remaining := len(candidates) - (size - depth)
			for index := start; index <= remaining; index++ {
				indices[depth] = index
				found, err := search(index+1, depth+1)
				if err != nil {
					return nil, err
				}
				if found != nil {
					return found, nil
				}
			}
			return nil, nil
		}
		found, err := search(0, 0)
		if err != nil {
			return nil, err
		}
		if found != nil {
			return found, nil
		}
	}
	return nil, ErrNoDivergence
}

// FindMinimalPendingDedupEpochDivergence is the common case: search for a
// minimal set that exposes a pending implementation's missing dedup and epoch
// callbacks.
func FindMinimalPendingDedupEpochDivergence(cfg Config, rec Recovery, candidates []Approval, now time.Time) (*Divergence, error) {
	return FindMinimalDivergence(cfg, rec, candidates, now, PendingDedupEpochPolicy)
}
