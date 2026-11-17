package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// Kind distinguishes the two audit events that bracket a credential's
// revocation boundary.
type Kind int

const (
	// KindUnknown is returned for event kinds the correlator does not model.
	KindUnknown Kind = iota
	// KindUsage is a credential-use event.
	KindUsage
	// KindRevocation is a credential-deletion/credential-revocation event.
	KindRevocation
)

func (k Kind) String() string {
	switch k {
	case KindUsage:
		return "usage"
	case KindRevocation:
		return "revocation"
	default:
		return "unknown"
	}
}

// Verdict is the ordered relation of a usage event relative to a revocation
// boundary, after sequence constraints have been applied.
type Verdict int

const (
	// VerdictUnknown is reserved for unclassified pairs.
	VerdictUnknown Verdict = iota
	// VerdictBefore means the usage is necessarily earlier than the revocation
	// in every feasible assignment: the credential was still valid.
	VerdictBefore
	// VerdictAfter means the usage is necessarily later than the revocation in
	// every feasible assignment: the credential was already revoked.
	VerdictAfter
	// VerdictStraddle means both orderings are feasible, or one of the events
	// participates in an infeasible (self-contradictory) sequence. The conflict
	// is retained instead of being silently resolved.
	VerdictStraddle
)

func (v Verdict) String() string {
	switch v {
	case VerdictBefore:
		return "before"
	case VerdictAfter:
		return "after"
	case VerdictStraddle:
		return "straddle"
	default:
		return "unknown"
	}
}

// Event is a cloud-audit event whose arrival order cannot be trusted.
//
// Channel identifies an ordered submission stream whose Sequence values are
// trustworthy: for two events on the same channel, the event with the smaller
// sequence is guaranteed to have happened no later than the other. Different
// channels (for example independent cloud services) have no ordering relation;
// only their time intervals constrain them.
//
// Lo/Hi are the inclusive, discrete time bounds inside which the event may have
// occurred, expressed in caller-defined ticks.
type Event struct {
	ID       string
	Kind     Kind
	CredID   string
	Channel  string
	Sequence int64
	Lo       int64
	Hi       int64
}

// NarrowedEvent is an Event together with the interval that survives the
// sequence-constraint narrowing.
type NarrowedEvent struct {
	Event    Event
	NarrowLo int64
	NarrowHi int64
	// Feasible is false when no time assignment satisfies the channel's
	// sequence ordering together with the reported intervals.
	Feasible bool
	// MinTick/MaxTick are the smallest/largest tick this specific event can
	// take in a feasible strict-order assignment of its whole channel. They
	// can lie inside [NarrowLo, NarrowHi] because neighbor events consume
	// ticks of their own.
	MinTick int64
	MaxTick int64
}

// Finding pairs one usage event with one revocation event for the same
// credential and records their necessarily/possibly ordering.
type Finding struct {
	CredID     string
	Usage      NarrowedEvent
	Revocation NarrowedEvent
	Verdict    Verdict
	// Conflict describes why the pair cannot be decided, empty otherwise.
	Conflict string
}

// Report is the full correlation result. Conflicting and undecidable material
// is always retained; callers decide how to escalate it.
type Report struct {
	Events   []NarrowedEvent
	Findings []Finding
	// ChannelFeasible records, per channel, whether its sequence constraints
	// admit at least one assignment.
	ChannelFeasible map[string]bool
}

// ErrInvalidEvent is wrapped and returned for malformed input events.
var ErrInvalidEvent = errors.New("netsec: invalid event")

// Correlator holds a set of possibly out-of-order audit events and narrows
// their time intervals with the trusted submission sequences.
type Correlator struct {
	events []Event
}

// NewCorrelator validates and stores the input events. Input order is
// irrelevant: events are re-ordered internally by (channel, sequence).
func NewCorrelator(events []Event) (*Correlator, error) {
	seen := make(map[[2]string]bool, len(events))
	sequences := make(map[string]map[int64]bool)
	for idx, ev := range events {
		if ev.ID == "" {
			return nil, fmt.Errorf("%w: event %d: empty ID", ErrInvalidEvent, idx)
		}
		if ev.Channel == "" {
			return nil, fmt.Errorf("%w: event %q: empty channel", ErrInvalidEvent, ev.ID)
		}
		if ev.Kind != KindUsage && ev.Kind != KindRevocation {
			return nil, fmt.Errorf("%w: event %q: unsupported kind %v", ErrInvalidEvent, ev.ID, ev.Kind)
		}
		if ev.Lo > ev.Hi {
			return nil, fmt.Errorf("%w: event %q: empty interval [%d,%d]", ErrInvalidEvent, ev.ID, ev.Lo, ev.Hi)
		}
		key := [2]string{ev.Channel, ev.ID}
		if seen[key] {
			return nil, fmt.Errorf("%w: event %q: duplicate ID within channel %q", ErrInvalidEvent, ev.ID, ev.Channel)
		}
		seen[key] = true
		if sequences[ev.Channel] == nil {
			sequences[ev.Channel] = make(map[int64]bool)
		}
		if sequences[ev.Channel][ev.Sequence] {
			return nil, fmt.Errorf("%w: event %q: duplicate sequence %d within channel %q",
				ErrInvalidEvent, ev.ID, ev.Sequence, ev.Channel)
		}
		sequences[ev.Channel][ev.Sequence] = true
	}

	cp := make([]Event, len(events))
	copy(cp, events)
	return &Correlator{events: cp}, nil
}

// narrowChannel applies the strict sequence constraint inside one channel.
//
// Submission sequences are a trusted total order: for events e1..en sorted by
// sequence, feasibility requires an assignment t1 < t2 < ... < tn with
// ti in [Lo_i, Hi_i]. A forward pass propagates the strict lower bound
// (Lo_i >= Lo_{i-1}+1) and a backward pass propagates the strict upper bound
// (Hi_i <= Hi_{i+1}-1). Both resulting bounds are individually attainable, so
// an empty result means the whole channel is self-contradictory.
func narrowChannel(events []Event) []NarrowedEvent {
	sorted := make([]Event, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Sequence < sorted[j].Sequence
	})

	out := make([]NarrowedEvent, len(sorted))
	var floor int64
	for i := range sorted {
		if i == 0 {
			floor = sorted[i].Lo
		} else if candidate := floor + 1; sorted[i].Lo > candidate {
			floor = sorted[i].Lo
		} else {
			floor = candidate
		}
		out[i] = NarrowedEvent{Event: sorted[i], NarrowLo: floor}
	}

	feasible := true
	var ceil int64
	for i := len(sorted) - 1; i >= 0; i-- {
		if i == len(sorted)-1 {
			ceil = sorted[i].Hi
		} else if candidate := ceil - 1; sorted[i].Hi < candidate {
			ceil = sorted[i].Hi
		} else {
			ceil = candidate
		}
		out[i].NarrowHi = ceil
		if out[i].NarrowLo > out[i].NarrowHi {
			feasible = false
		}
	}
	for i := range out {
		out[i].Feasible = feasible
	}
	if !feasible {
		return out
	}

	// Earliest attainable tick per event: place every predecessor as early as
	// possible while respecting strict order.
	var earliest int64
	for i := range out {
		if i == 0 {
			earliest = out[i].NarrowLo
		} else if candidate := earliest + 1; out[i].NarrowLo > candidate {
			earliest = out[i].NarrowLo
		} else {
			earliest = candidate
		}
		out[i].MinTick = earliest
	}

	// Latest attainable tick per event: place every successor as late as
	// possible.
	var latest int64
	for i := len(out) - 1; i >= 0; i-- {
		if i == len(out)-1 {
			latest = out[i].NarrowHi
		} else if candidate := latest - 1; out[i].NarrowHi < candidate {
			latest = out[i].NarrowHi
		} else {
			latest = candidate
		}
		out[i].MaxTick = latest
	}
	return out
}

// Narrow groups events by channel and narrows every interval. The returned
// slice is sorted by (channel, sequence); channel feasibility is reported
// separately so infeasible events are retained rather than dropped.
func (c *Correlator) Narrow() ([]NarrowedEvent, map[string]bool) {
	channels := make(map[string][]Event)
	order := make([]string, 0)
	for _, ev := range c.events {
		if _, ok := channels[ev.Channel]; !ok {
			order = append(order, ev.Channel)
		}
		channels[ev.Channel] = append(channels[ev.Channel], ev)
	}
	sort.Strings(order)

	feasible := make(map[string]bool, len(order))
	var all []NarrowedEvent
	for _, ch := range order {
		narrowed := narrowChannel(channels[ch])
		feasible[ch] = len(narrowed) > 0 && narrowed[0].Feasible
		all = append(all, narrowed...)
	}
	return all, feasible
}

// classify compares one usage against one revocation. Same-channel pairs rely
// on the trusted strict sequence order. Cross-channel pairs use the exact
// attainable tick ranges (MinTick..MaxTick): only when the usage's latest
// attainable tick is strictly below the revocation's earliest, or vice versa,
// is the ordering necessary; overlapping attainable ranges mean both orders
// are realizable and the pair straddles the revocation boundary.
func classify(usage, revocation NarrowedEvent, sameChannel bool) Verdict {
	if !usage.Feasible || !revocation.Feasible {
		return VerdictStraddle
	}
	if sameChannel {
		switch {
		case usage.Event.Sequence < revocation.Event.Sequence:
			return VerdictBefore
		case usage.Event.Sequence > revocation.Event.Sequence:
			return VerdictAfter
		default:
			return VerdictStraddle
		}
	}
	switch {
	case usage.MaxTick < revocation.MinTick:
		return VerdictBefore
	case usage.MinTick > revocation.MaxTick:
		return VerdictAfter
	default:
		return VerdictStraddle
	}
}

// Correlate narrows every event and emits one Finding per (usage, revocation)
// pair sharing a credential. No event or pair is discarded: infeasible
// sequences and straddling pairs carry an explicit Conflict note.
func (c *Correlator) Correlate() Report {
	narrowed, feasible := c.Narrow()

	var usages, revocations []NarrowedEvent
	for _, ne := range narrowed {
		switch ne.Event.Kind {
		case KindUsage:
			usages = append(usages, ne)
		case KindRevocation:
			revocations = append(revocations, ne)
		}
	}

	sort.SliceStable(usages, func(i, j int) bool {
		return usages[i].Event.ID < usages[j].Event.ID
	})
	sort.SliceStable(revocations, func(i, j int) bool {
		return revocations[i].Event.ID < revocations[j].Event.ID
	})

	var findings []Finding
	for _, usage := range usages {
		for _, revocation := range revocations {
			if usage.Event.CredID != revocation.Event.CredID || usage.Event.CredID == "" {
				continue
			}
			sameChannel := usage.Event.Channel == revocation.Event.Channel
			finding := Finding{
				CredID:     usage.Event.CredID,
				Usage:      usage,
				Revocation: revocation,
				Verdict:    classify(usage, revocation, sameChannel),
			}
			switch {
			case !usage.Feasible:
				finding.Conflict = fmt.Sprintf("usage channel %q has no assignment satisfying its sequence constraints", usage.Event.Channel)
			case !revocation.Feasible:
				finding.Conflict = fmt.Sprintf("revocation channel %q has no assignment satisfying its sequence constraints", revocation.Event.Channel)
			case finding.Verdict == VerdictStraddle:
				if sameChannel {
					finding.Conflict = "usage and revocation share a sequence position"
				} else {
					finding.Conflict = fmt.Sprintf("attainable ranges overlap: usage ticks [%d,%d] vs revocation ticks [%d,%d]",
						usage.MinTick, usage.MaxTick, revocation.MinTick, revocation.MaxTick)
				}
			}
			findings = append(findings, finding)
		}
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].CredID != findings[j].CredID {
			return findings[i].CredID < findings[j].CredID
		}
		if findings[i].Usage.Event.ID != findings[j].Usage.Event.ID {
			return findings[i].Usage.Event.ID < findings[j].Usage.Event.ID
		}
		return findings[i].Revocation.Event.ID < findings[j].Revocation.Event.ID
	})

	return Report{Events: narrowed, Findings: findings, ChannelFeasible: feasible}
}
