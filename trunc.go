// Package logtrunc plans safe truncation of a segmented log.
//
// A segment becomes deletable only when two conditions hold:
//
//  1. Every registered consumer has confirmed a *contiguous* prefix of
//     records that covers the whole segment. A gap in a consumer's
//     confirmations is never masked by a larger confirmed sequence
//     number: only the gap-free prefix (the watermark) counts.
//     Consumers that exit are unregistered and stop constraining
//     truncation; with no registered consumers there is no consumer
//     dependency at all.
//  2. No live snapshot pins the segment. Old snapshots may depend on
//     historical segments and block their deletion until removed.
//
// For every retained segment the planner computes a minimal blocking
// evidence: a single, deterministically chosen reason (one snapshot or
// one consumer) that suffices to explain why the segment must be kept.
package logtrunc

import (
	"fmt"
	"sort"
)

// Segment is a half-open range [Base, End) of record offsets.
type Segment struct {
	ID   string
	Base uint64
	End  uint64 // exclusive
}

// RollbackError is returned when a consumer tries to move its confirmed
// watermark backwards. Ack rollbacks are always rejected.
type RollbackError struct {
	Consumer  string
	Current   uint64
	Requested uint64
}

func (e *RollbackError) Error() string {
	return fmt.Sprintf("logtrunc: ack rollback rejected for consumer %q: watermark %d -> %d",
		e.Consumer, e.Current, e.Requested)
}

type consumer struct {
	// confirmed holds individually acked offsets at or above the
	// watermark. Offsets below the watermark are pruned.
	confirmed map[uint64]struct{}
	// watermark is the contiguous confirmed prefix: every offset in
	// [0, watermark) is confirmed, and confirmed[watermark] is empty.
	watermark uint64
}

// Snapshot pins the historical segments it still depends on.
type Snapshot struct {
	ID       string
	Segments map[string]struct{}
}

// Planner tracks segments, consumer confirmations and snapshots, and
// computes which segments may be truncated.
type Planner struct {
	segments  map[string]Segment
	consumers map[string]*consumer
	snapshots map[string]Snapshot
}

func NewPlanner() *Planner {
	return &Planner{
		segments:  make(map[string]Segment),
		consumers: make(map[string]*consumer),
		snapshots: make(map[string]Snapshot),
	}
}

// AddSegment registers a segment. Re-adding an existing ID replaces it.
func (p *Planner) AddSegment(s Segment) error {
	if s.End <= s.Base {
		return fmt.Errorf("logtrunc: invalid segment %q: empty range [%d, %d)", s.ID, s.Base, s.End)
	}
	p.segments[s.ID] = s
	return nil
}

// RemoveSegment forgets a segment, e.g. after it has been deleted.
func (p *Planner) RemoveSegment(id string) {
	delete(p.segments, id)
}

// Segments returns all known segments ordered by (Base, ID).
func (p *Planner) Segments() []Segment {
	out := make([]Segment, 0, len(p.segments))
	for _, s := range p.segments {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Base != out[j].Base {
			return out[i].Base < out[j].Base
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// AddConsumer registers a consumer whose confirmed prefix starts at
// the given watermark.
func (p *Planner) AddConsumer(name string, watermark uint64) {
	p.consumers[name] = &consumer{confirmed: make(map[uint64]struct{}), watermark: watermark}
}

// RemoveConsumer unregisters a consumer that has exited. Its
// confirmations stop constraining truncation immediately.
func (p *Planner) RemoveConsumer(name string) {
	delete(p.consumers, name)
}

// Consumers returns the names of registered consumers, sorted.
func (p *Planner) Consumers() []string {
	out := make([]string, 0, len(p.consumers))
	for name := range p.consumers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Ack records confirmation of a single record offset. The consumer's
// watermark advances only across a gap-free prefix: acking offset 5
// while 3 is missing leaves the watermark at 3. Acking an offset below
// the watermark is a harmless duplicate and never moves it backwards.
func (p *Planner) Ack(name string, offset uint64) error {
	c, ok := p.consumers[name]
	if !ok {
		return fmt.Errorf("logtrunc: unknown consumer %q", name)
	}
	if offset < c.watermark {
		return nil
	}
	c.confirmed[offset] = struct{}{}
	c.advance()
	return nil
}

// SetWatermark bulk-confirms every offset below w. Moving the watermark
// backwards is an ack rollback and is rejected with *RollbackError.
func (p *Planner) SetWatermark(name string, w uint64) error {
	c, ok := p.consumers[name]
	if !ok {
		return fmt.Errorf("logtrunc: unknown consumer %q", name)
	}
	if w < c.watermark {
		return &RollbackError{Consumer: name, Current: c.watermark, Requested: w}
	}
	for off := range c.confirmed {
		if off < w {
			delete(c.confirmed, off)
		}
	}
	c.watermark = w
	c.advance()
	return nil
}

func (c *consumer) advance() {
	for {
		if _, ok := c.confirmed[c.watermark]; !ok {
			return
		}
		delete(c.confirmed, c.watermark)
		c.watermark++
	}
}

// Watermark returns the consumer's contiguous confirmed prefix end.
func (p *Planner) Watermark(name string) (uint64, bool) {
	c, ok := p.consumers[name]
	if !ok {
		return 0, false
	}
	return c.watermark, true
}

// AddSnapshot registers a snapshot pinning its dependent segments.
func (p *Planner) AddSnapshot(s Snapshot) {
	p.snapshots[s.ID] = s
}

// RemoveSnapshot forgets a snapshot, releasing its pinned segments.
func (p *Planner) RemoveSnapshot(id string) {
	delete(p.snapshots, id)
}

// Horizon returns the truncation horizon: the minimum contiguous
// confirmed watermark across all consumers. The second result is false
// when no consumer is registered, in which case no record has a
// consumer dependency and the horizon does not constrain truncation.
func (p *Planner) Horizon() (uint64, bool) {
	if len(p.consumers) == 0 {
		return 0, false
	}
	first := true
	var min uint64
	for _, c := range p.consumers {
		if first || c.watermark < min {
			min = c.watermark
			first = false
		}
	}
	return min, true
}

// Evidence is a single minimal reason a segment must be retained.
// Exactly one field is set.
type Evidence struct {
	Consumer string // this consumer's contiguous watermark does not cover the segment
	Snapshot string // this snapshot still pins the segment
}

// SegmentPlan pairs a retained segment with its minimal blocking
// evidence.
type SegmentPlan struct {
	Segment  Segment
	Evidence []Evidence
}

// Plan is the result of one truncation planning pass.
type Plan struct {
	// Horizon is the minimum contiguous consumer watermark.
	Horizon uint64
	// HasConsumers is false when no consumer is registered; the
	// horizon then imposes no constraint.
	HasConsumers bool
	// Deletable lists segments safe to delete, ordered by (Base, ID).
	Deletable []Segment
	// Retained lists the other segments with minimal blocking evidence.
	Retained []SegmentPlan
}

// Plan computes deletable segments and, for each retained segment, a
// minimal blocking evidence. The evidence holds exactly one blocker:
// a pinning snapshot if any exists (smallest ID), otherwise the
// consumer with the lowest watermark (smallest name on ties).
func (p *Planner) Plan() Plan {
	horizon, hasConsumers := p.Horizon()
	plan := Plan{Horizon: horizon, HasConsumers: hasConsumers}
	for _, s := range p.Segments() {
		snapBlockers := p.snapshotBlockers(s.ID)
		consumerBlocked := hasConsumers && s.End > horizon
		if !consumerBlocked && len(snapBlockers) == 0 {
			plan.Deletable = append(plan.Deletable, s)
			continue
		}
		sp := SegmentPlan{Segment: s}
		switch {
		case len(snapBlockers) > 0:
			sp.Evidence = []Evidence{{Snapshot: snapBlockers[0]}}
		case hasConsumers:
			sp.Evidence = []Evidence{{Consumer: p.slowestConsumer()}}
		}
		plan.Retained = append(plan.Retained, sp)
	}
	return plan
}

// AllBlockers returns every reason a segment is retained: all consumers
// whose contiguous watermark does not cover the segment, and all
// snapshots pinning it. Useful for diagnostics; Plan returns only the
// minimal evidence.
func (p *Planner) AllBlockers(segID string) (consumers []string, snapshots []string) {
	s, ok := p.segments[segID]
	if !ok {
		return nil, nil
	}
	for name, c := range p.consumers {
		if c.watermark < s.End {
			consumers = append(consumers, name)
		}
	}
	sort.Strings(consumers)
	return consumers, p.snapshotBlockers(segID)
}

func (p *Planner) snapshotBlockers(segID string) []string {
	var out []string
	for id, snap := range p.snapshots {
		if _, ok := snap.Segments[segID]; ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// slowestConsumer returns the consumer with the lowest watermark,
// breaking ties by name. Call only when at least one consumer exists.
func (p *Planner) slowestConsumer() string {
	best := ""
	first := true
	var bestWM uint64
	for name, c := range p.consumers {
		if first || c.watermark < bestWM || (c.watermark == bestWM && name < best) {
			best, bestWM, first = name, c.watermark, false
		}
	}
	return best
}
