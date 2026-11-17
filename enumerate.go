package netsec

import (
	"fmt"
	"sort"
)

// DefaultEnumBudget bounds the number of per-channel assignments explored by
// EnumerateOrderings. It keeps the brute-force check suitable for small time
// domains; callers may pass a larger budget explicitly.
const DefaultEnumBudget = 200_000

// Assignment maps "channel/event-id" to the tick at which the event occurs in
// one feasible realization of the whole system.
type Assignment map[string]int64

// EnumOutcome collects what brute-force enumeration observed for one finding.
type EnumOutcome struct {
	UsageID      string
	RevocationID string
	// Before/After/Straddle count assignments in which the usage is
	// respectively earlier than, later than, or exactly on the revocation tick.
	Before   int
	After    int
	Straddle int
	// Exhaustive is false when the budget was exhausted before every
	// assignment had been explored.
	Exhaustive bool
	// Infeasible is true when at least one participating channel admits no
	// assignment at all, so no realization can be enumerated.
	Infeasible bool
}

// Verdict reduces the observed assignments to the same vocabulary used by the
// analytical classifier.
func (o EnumOutcome) Verdict() Verdict {
	switch {
	case o.Before > 0 && o.After == 0 && o.Straddle == 0:
		return VerdictBefore
	case o.After > 0 && o.Before == 0 && o.Straddle == 0:
		return VerdictAfter
	case o.Before == 0 && o.After == 0 && o.Straddle == 0:
		return VerdictUnknown
	default:
		return VerdictStraddle
	}
}

// Mismatch records a disagreement between the analytical classifier and the
// enumeration oracle for one finding.
type Mismatch struct {
	CredID          string
	UsageID         string
	RevocationID    string
	Analytical      Verdict
	Enumerated      Verdict
	EnumeratedProof EnumOutcome
	Reason          string
}

// CrossCheckReport pairs the oracle outcomes with any disagreements found.
type CrossCheckReport struct {
	Outcomes   []EnumOutcome
	Mismatches []Mismatch
	// TruncatedChannels lists channels whose enumeration hit the budget.
	TruncatedChannels []string
}

func eventKey(channel, id string) string { return channel + "/" + id }

// enumerateChannel returns every strictly increasing tick assignment for one
// channel: sorted events e1..en receive ticks t1 < t2 < ... < tn inside their
// *narrowed* intervals. Submission sequences are a strict total order, so two
// events on the same channel can never share an occurrence tick. The boolean
// is false when the budget stopped enumeration early.
func enumerateChannel(events []NarrowedEvent, budget int) ([]eventAssignment, bool) {
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Event.Sequence < events[j].Event.Sequence
	})

	current := []prefix{{ticks: nil, last: 0}}
	for idx, ne := range events {
		var next []prefix
		for _, p := range current {
			lo := ne.NarrowLo
			if idx > 0 {
				lo = p.last + 1
				if ne.NarrowLo > lo {
					lo = ne.NarrowLo
				}
			}
			for tick := lo; tick <= ne.NarrowHi; tick++ {
				grown := prefix{ticks: make([]int64, len(p.ticks)+1), last: tick}
				copy(grown.ticks, p.ticks)
				grown.ticks[len(p.ticks)] = tick
				next = append(next, grown)
				if len(next) >= budget {
					return toAssignments(events, next), false
				}
			}
		}
		current = next
		if len(current) == 0 {
			break
		}
	}
	return toAssignments(events, current), true
}

func toAssignments(events []NarrowedEvent, prefixes []prefix) []eventAssignment {
	result := make([]eventAssignment, 0, len(prefixes))
	for _, p := range prefixes {
		ea := make(eventAssignment, len(events))
		for idx, ne := range events {
			ea[eventKey(ne.Event.Channel, ne.Event.ID)] = p.ticks[idx]
		}
		result = append(result, ea)
	}
	return result
}

// eventAssignment is one channel-local realization.
type eventAssignment map[string]int64

type prefix struct {
	ticks []int64
	last  int64
}

// EnumerateOrderings enumerates feasible realizations over the narrowed
// intervals of every channel and tallies the usage-vs-revocation ordering for
// each finding in report. A non-positive budget selects DefaultEnumBudget.
//
// Channels are independent, so a realization is the union of one feasible
// channel assignment each. The tally stops once budget total realizations have
// been examined; outcomes still carry Exhaustive=false in that case.
func EnumerateOrderings(report Report, budget int) CrossCheckReport {
	if budget <= 0 {
		budget = DefaultEnumBudget
	}

	channelEvents := make(map[string][]NarrowedEvent)
	for _, ne := range report.Events {
		channelEvents[ne.Event.Channel] = append(channelEvents[ne.Event.Channel], ne)
	}

	channelSets := make(map[string][]eventAssignment)
	var truncated []string
	for channel, events := range channelEvents {
		set, exhaustive := enumerateChannel(events, budget+1)
		if !exhaustive {
			truncated = append(truncated, channel)
		}
		channelSets[channel] = set
	}
	sort.Strings(truncated)

	outcomes := make(map[string]*EnumOutcome)
	for _, finding := range report.Findings {
		outcomes[finding.Usage.Event.ID+"\x00"+finding.Revocation.Event.ID] = &EnumOutcome{
			UsageID:      finding.Usage.Event.ID,
			RevocationID: finding.Revocation.Event.ID,
			Exhaustive:   true,
			Infeasible:   !finding.Usage.Feasible || !finding.Revocation.Feasible,
		}
	}

	// Walk the cartesian product of channel assignments lazily so that the
	// total number of examined realizations respects the budget.
	channelNames := make([]string, 0, len(channelSets))
	for name := range channelSets {
		channelNames = append(channelNames, name)
	}
	sort.Strings(channelNames)

	examined := 0
	exhaustive := true

	var walk func(depth int, partial Assignment)
	walk = func(depth int, partial Assignment) {
		if examined >= budget {
			exhaustive = false
			return
		}
		if depth == len(channelNames) {
			tally(report, partial, outcomes)
			examined++
			return
		}
		name := channelNames[depth]
		for _, local := range channelSets[name] {
			next := make(Assignment, len(partial)+len(local))
			for k, v := range partial {
				next[k] = v
			}
			for k, v := range local {
				next[k] = v
			}
			walk(depth+1, next)
			if examined >= budget {
				exhaustive = false
				return
			}
		}
	}
	if len(channelNames) == 0 {
		tally(report, Assignment{}, outcomes)
		examined = 1
	} else {
		walk(0, Assignment{})
	}

	result := CrossCheckReport{TruncatedChannels: truncated}
	keys := make([]string, 0, len(outcomes))
	for key := range outcomes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		outcome := outcomes[key]
		outcome.Exhaustive = exhaustive && len(truncated) == 0
		result.Outcomes = append(result.Outcomes, *outcome)
	}

	for _, finding := range report.Findings {
		key := finding.Usage.Event.ID + "\x00" + finding.Revocation.Event.ID
		outcome := outcomes[key]
		if outcome.Infeasible {
			continue
		}
		enumVerdict := outcome.Verdict()
		if enumVerdict != finding.Verdict {
			result.Mismatches = append(result.Mismatches, Mismatch{
				CredID:          finding.CredID,
				UsageID:         finding.Usage.Event.ID,
				RevocationID:    finding.Revocation.Event.ID,
				Analytical:      finding.Verdict,
				Enumerated:      enumVerdict,
				EnumeratedProof: *outcome,
				Reason: fmt.Sprintf("analytical classifier says %s, enumeration over %d realizations says %s",
					finding.Verdict, examined, enumVerdict),
			})
		}
	}
	sort.SliceStable(result.Mismatches, func(i, j int) bool {
		if result.Mismatches[i].CredID != result.Mismatches[j].CredID {
			return result.Mismatches[i].CredID < result.Mismatches[j].CredID
		}
		if result.Mismatches[i].UsageID != result.Mismatches[j].UsageID {
			return result.Mismatches[i].UsageID < result.Mismatches[j].UsageID
		}
		return result.Mismatches[i].RevocationID < result.Mismatches[j].RevocationID
	})

	return result
}

func tally(report Report, assignment Assignment, outcomes map[string]*EnumOutcome) {
	for _, finding := range report.Findings {
		usageTick := assignment[eventKey(finding.Usage.Event.Channel, finding.Usage.Event.ID)]
		revocationTick := assignment[eventKey(finding.Revocation.Event.Channel, finding.Revocation.Event.ID)]
		outcome := outcomes[finding.Usage.Event.ID+"\x00"+finding.Revocation.Event.ID]
		switch {
		case usageTick < revocationTick:
			outcome.Before++
		case usageTick > revocationTick:
			outcome.After++
		default:
			outcome.Straddle++
		}
	}
}

// CrossCheck runs the analytical correlation and then verifies every finding
// against brute-force enumeration over the given per-realization budget.
func (c *Correlator) CrossCheck(budget int) (Report, CrossCheckReport) {
	report := c.Correlate()
	return report, EnumerateOrderings(report, budget)
}
