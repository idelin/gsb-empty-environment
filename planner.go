package logtrunc

import "sort"

// BlockerKind distinguishes consumer-lag blocks from snapshot pins.
type BlockerKind int

const (
	// BlockerConsumer means a consumer's confirmed continuous prefix
	// does not yet cover the whole segment.
	BlockerConsumer BlockerKind = iota
	// BlockerSnapshot means a live snapshot pins part of the segment.
	BlockerSnapshot
)

// Blocker is one piece of evidence that a segment must be retained.
type Blocker struct {
	Kind BlockerKind

	// Consumer blockers.
	Consumer string
	Horizon  uint64 // consumer's confirmed continuous prefix
	FirstGap uint64 // smallest unconfirmed sequence number inside the segment

	// Snapshot blockers.
	Snapshot string
	PinFrom  uint64
	PinTo    uint64
}

// RetainedSegment is a truncation candidate that must be kept, together
// with its minimal blocking evidence: the exact set of entities whose
// state must change before the segment becomes deletable. Every blocker
// in the set independently prevents deletion, so all of them must clear.
type RetainedSegment struct {
	Segment  Segment
	Blockers []Blocker
}

// Plan is the result of evaluating truncation up to a target sequence
// number. Only segments with End <= Target are candidates; higher
// segments are left untouched and are not reported.
type Plan struct {
	Target uint64
	// Deletable segments may be removed immediately.
	Deletable []Segment
	// Retained candidates are blocked; each carries its evidence.
	Retained []RetainedSegment
}

// Planner computes truncation plans from the segment store, the consumer
// registry, and the live snapshots.
type Planner struct {
	Store SegmentStore
	Reg   *Registry
	Snaps *Snapshots
}

// Plan evaluates which segments with End <= target can be deleted.
//
// A segment is deletable iff every active consumer has confirmed a
// continuous prefix covering the whole segment AND no live snapshot
// overlaps it. Because horizons are continuous prefixes, a confirmation
// gap below a consumer's maximum confirmed sequence number still blocks
// the segment containing the gap.
func (p *Planner) Plan(target uint64) Plan {
	horizons := p.Reg.Horizons()
	consumerIDs := make([]string, 0, len(horizons))
	for id := range horizons {
		consumerIDs = append(consumerIDs, id)
	}
	sort.Strings(consumerIDs)
	snaps := p.Snaps.List()

	plan := Plan{Target: target}
	for _, seg := range p.Store.Segments() {
		if seg.End > target {
			continue
		}
		var blockers []Blocker
		for _, id := range consumerIDs {
			horizon := horizons[id]
			if horizon >= seg.End {
				continue
			}
			gap := seg.Start
			if horizon+1 > gap {
				gap = horizon + 1
			}
			blockers = append(blockers, Blocker{
				Kind:     BlockerConsumer,
				Consumer: id,
				Horizon:  horizon,
				FirstGap: gap,
			})
		}
		for _, snap := range snaps {
			if seg.Overlaps(snap.From, snap.To) {
				blockers = append(blockers, Blocker{
					Kind:     BlockerSnapshot,
					Snapshot: snap.ID,
					PinFrom:  snap.From,
					PinTo:    snap.To,
				})
			}
		}
		if len(blockers) == 0 {
			plan.Deletable = append(plan.Deletable, seg)
		} else {
			plan.Retained = append(plan.Retained, RetainedSegment{Segment: seg, Blockers: blockers})
		}
	}
	return plan
}
