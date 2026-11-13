package netsec

import "fmt"

// DecisionKind identifies the decision being audited.
type DecisionKind uint8

const (
	DecisionEvent DecisionKind = iota + 1
	DecisionAdvance
	DecisionReclaim
)

func (k DecisionKind) String() string {
	switch k {
	case DecisionEvent:
		return "event"
	case DecisionAdvance:
		return "advance"
	case DecisionReclaim:
		return "reclaim"
	default:
		return fmt.Sprintf("decision(%d)", uint8(k))
	}
}

// ProposedStep is one event from a pending implementation together with the
// decisions that implementation reported for it. ClaimedAdvance is meaningful
// only for TryAdvance. ClaimedReclaim lists generations the implementation said
// it reclaimed immediately after the event.
type ProposedStep struct {
	Thread         int
	Event          Event
	ClaimedAdvance *bool
	ClaimedReclaim []ObjectRef
}

// UnsafeDecision is the first point at which a proposed decision exceeds the
// correct model.
type UnsafeDecision struct {
	Step     int
	Thread   int
	Event    Event
	Kind     DecisionKind
	Ref      ObjectRef
	Claimed  bool
	Expected bool
	Reason   string
	Before   Snapshot
}

func (u *UnsafeDecision) String() string {
	if u == nil {
		return "<none>"
	}
	return fmt.Sprintf("step %d (%s): %s", u.Step, u.Kind, u.Reason)
}

// ConservativeMismatch records a decision that was more conservative than the
// correct model. Such a decision is not unsafe.
type ConservativeMismatch struct {
	Step   int
	Kind   DecisionKind
	Detail string
}

// ReviewResult is the outcome of replaying a proposed interleaving.
type ReviewResult struct {
	FirstUnsafe *UnsafeDecision
	Mismatches  []ConservativeMismatch
	Reclaimed   []ObjectRef
	FinalState  *State
}

// Safe reports whether no unsafe decision was found.
func (r *ReviewResult) Safe() bool {
	return r != nil && r.FirstUnsafe == nil
}

// FirstUnsafeDecision is a convenience wrapper that returns only the first
// unsafe decision from a proposed trace. It returns nil when the trace is safe.
func FirstUnsafeDecision(steps []ProposedStep) (*UnsafeDecision, error) {
	result, err := ReviewProposed(steps)
	if err != nil {
		return nil, err
	}
	return result.FirstUnsafe, nil
}

// ReviewProposed replays the steps with automatic reclamation disabled and
// audits each explicit advance/reclaim decision. It stops at the first unsafe
// decision, which is the earliest event that the correct model rejects or the
// earliest claimed advance/reclaim that the model does not permit.
func ReviewProposed(steps []ProposedStep) (*ReviewResult, error) {
	threadCount := 0
	for i, step := range steps {
		if step.Thread < 0 || step.Thread > MaxThreads-1 {
			return nil, fmt.Errorf("step %d: invalid thread %d", i, step.Thread)
		}
		if step.Thread+1 > threadCount {
			threadCount = step.Thread + 1
		}
	}
	opts := DefaultOptions()
	opts.AutoReclaim = false
	return ReviewProposedWithOptions(threadCount, opts, steps)
}

// ReviewProposedWithOptions is ReviewProposed with explicit thread count and
// strictness options. AutoReclaim is forced off so that reclamation decisions
// are audited rather than silently performed.
func ReviewProposedWithOptions(threadCount int, opts Options, steps []ProposedStep) (*ReviewResult, error) {
	if threadCount < 0 || threadCount > MaxThreads {
		return nil, fmt.Errorf("thread count %d outside [0,%d]", threadCount, MaxThreads)
	}
	opts.AutoReclaim = false
	st, err := NewStateWithOptions(threadCount, opts)
	if err != nil {
		return nil, err
	}
	result := &ReviewResult{FinalState: st}

	for i, step := range steps {
		if step.Thread < 0 || step.Thread >= threadCount {
			unsafe := &UnsafeDecision{
				Step:   i,
				Thread: step.Thread,
				Event:  step.Event,
				Kind:   DecisionEvent,
				Reason: fmt.Sprintf("invalid thread %d for %d-thread model", step.Thread, threadCount),
				Before: st.Snapshot(),
			}
			result.FirstUnsafe = unsafe
			result.FinalState = st.Clone()
			return result, nil
		}

		applyThread := ThreadID(step.Thread)
		if step.Event.Kind.IsUpdaterEvent() {
			applyThread = UpdaterThread
		}
		before := st.Snapshot()
		out, applyErr := st.Apply(applyThread, step.Event)
		if applyErr != nil {
			result.FirstUnsafe = &UnsafeDecision{
				Step:   i,
				Thread: step.Thread,
				Event:  step.Event,
				Kind:   DecisionEvent,
				Reason: applyErr.Error(),
				Before: before,
			}
			result.FinalState = st.Clone()
			return result, nil
		}

		if step.Event.Kind == EventTryAdvance && step.ClaimedAdvance != nil {
			switch {
			case *step.ClaimedAdvance && !out.Advanced:
				result.FirstUnsafe = &UnsafeDecision{
					Step:     i,
					Thread:   step.Thread,
					Event:    step.Event,
					Kind:     DecisionAdvance,
					Claimed:  true,
					Expected: false,
					Reason: "claimed epoch advance while an active thread's published epoch " +
						"did not equal the current epoch",
					Before: before,
				}
				result.FinalState = st.Clone()
				return result, nil
			case !*step.ClaimedAdvance && out.Advanced:
				result.Mismatches = append(result.Mismatches, ConservativeMismatch{
					Step:   i,
					Kind:   DecisionAdvance,
					Detail: "advance was permitted but the pending implementation declined it",
				})
			}
		}

		if len(step.ClaimedReclaim) > 0 {
			refs := append([]ObjectRef(nil), step.ClaimedReclaim...)
			sortRefs(refs)
			for _, ref := range refs {
				o, ok := st.Object(ref)
				if !ok {
					result.FirstUnsafe = &UnsafeDecision{
						Step:     i,
						Thread:   step.Thread,
						Event:    step.Event,
						Kind:     DecisionReclaim,
						Ref:      ref,
						Claimed:  true,
						Expected: false,
						Reason:   fmt.Sprintf("reclaimed unknown object %s", ref),
						Before:   st.Snapshot(),
					}
					result.FinalState = st.Clone()
					return result, nil
				}
				if o.Status == ObjectReclaimed {
					result.Mismatches = append(result.Mismatches, ConservativeMismatch{
						Step:   i,
						Kind:   DecisionReclaim,
						Detail: fmt.Sprintf("%s was already reclaimed", ref),
					})
					continue
				}
				if !st.CanReclaim(ref) {
					result.FirstUnsafe = &UnsafeDecision{
						Step:     i,
						Thread:   step.Thread,
						Event:    step.Event,
						Kind:     DecisionReclaim,
						Ref:      ref,
						Claimed:  true,
						Expected: false,
						Reason: fmt.Sprintf(
							"reclaimed %s while status=%s safeAdvances=%d successfulAdvances=%d",
							ref, o.Status, o.SafeAdvances, o.SuccessfulAdvances),
						Before: st.Snapshot(),
					}
					result.FinalState = st.Clone()
					return result, nil
				}
				if err := st.Reclaim(ref); err != nil {
					result.FirstUnsafe = &UnsafeDecision{
						Step:     i,
						Thread:   step.Thread,
						Event:    step.Event,
						Kind:     DecisionReclaim,
						Ref:      ref,
						Claimed:  true,
						Expected: false,
						Reason:   err.Error(),
						Before:   st.Snapshot(),
					}
					result.FinalState = st.Clone()
					return result, nil
				}
				result.Reclaimed = append(result.Reclaimed, ref)
			}
		}
	}
	result.FinalState = st.Clone()
	return result, nil
}
