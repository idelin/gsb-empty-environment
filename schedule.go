package changeaccept

import (
	"fmt"
	"math/bits"
)

type maskVerdict uint8

const (
	maskUnknown maskVerdict = iota
	maskValid
	maskInvalid
	maskUndetermined
)

// FindMinimalOrder searches all dependency-respecting orders for one that is
// compliant at every prefix. Among those it returns the minimum number of
// adjacent swaps from the input's candidate order, breaking ties by the
// lexicographically smallest group-ID sequence.
func (in Input) FindMinimalOrder() (ScheduleReport, error) {
	compiled, err := in.compile()
	if err != nil {
		return ScheduleReport{}, err
	}
	if err := checkOrderSet(compiled.order, compiled.groups); err != nil {
		return ScheduleReport{}, err
	}
	n := len(compiled.order)
	if n > MaxSwitchGroups {
		return ScheduleReport{}, fmt.Errorf("too many groups: %d", n)
	}
	if n == 0 {
		snapshot, err := ApplyGroups(in.Topology, in.BaseGeneration, compiled.groups, nil)
		if err != nil {
			return ScheduleReport{}, err
		}
		snapshot.Auth = in.Auth
		snapshot.Labels = append([]Label(nil), in.Labels...)
		prefix := in.verifySnapshot(snapshot, compiled)
		return ScheduleReport{
			Order:    []GroupID{},
			Swaps:    0,
			Prefixes: []PrefixReport{prefix},
			Outcome:  prefix.Outcome,
		}, nil
	}

	bitByID := make(map[GroupID]int, n)
	for index, id := range compiled.order {
		bitByID[id] = index
	}
	predecessors := make([]int, n)
	for index, id := range compiled.order {
		for _, dependency := range compiled.groups[id].DependsOn {
			dependencyBit, ok := bitByID[dependency]
			if !ok {
				return ScheduleReport{}, fmt.Errorf("group %d depends on %d outside the candidate order", id, dependency)
			}
			predecessors[index] |= 1 << dependencyBit
		}
	}

	m := newMeter(in.Budget)
	verdicts := make([]maskVerdict, 1<<n)
	reports := make([]PrefixReport, 1<<n)
	verdicts[0] = maskUnknown
	sawUndetermined := false

	maskOutcome := func(mask int) (maskVerdict, PrefixReport) {
		if verdicts[mask] != maskUnknown {
			return verdicts[mask], reports[mask]
		}
		if err := m.addSubset(); err != nil {
			verdicts[mask] = maskUndetermined
			reports[mask] = PrefixReport{Outcome: OutcomeUndetermined, BudgetExhausted: true}
			sawUndetermined = true
			return verdicts[mask], reports[mask]
		}
		order := make([]GroupID, 0, bits.OnesCount(uint(mask)))
		for bit, id := range compiled.order {
			if mask&(1<<bit) != 0 {
				order = append(order, id)
			}
		}
		snapshot, err := ApplyGroups(in.Topology, in.BaseGeneration, compiled.groups, order)
		if err != nil {
			verdicts[mask] = maskInvalid
			reports[mask] = PrefixReport{Outcome: OutcomeNonCompliant}
			return verdicts[mask], reports[mask]
		}
		snapshot.Auth = in.Auth
		snapshot.Labels = append([]Label(nil), in.Labels...)
		report := in.verifySnapshot(snapshot, compiled)
		report.Applied = order
		switch report.Outcome {
		case OutcomeCompliant:
			verdicts[mask] = maskValid
		case OutcomeNonCompliant:
			verdicts[mask] = maskInvalid
		default:
			verdicts[mask] = maskUndetermined
			sawUndetermined = true
		}
		reports[mask] = report
		return verdicts[mask], report
	}

	baseVerdict, baseReport := maskOutcome(0)
	switch baseVerdict {
	case maskInvalid:
		return ScheduleReport{
			Order:    nil,
			Swaps:    0,
			Prefixes: []PrefixReport{baseReport},
			Outcome:  OutcomeNonCompliant,
		}, nil
	case maskUndetermined:
		return ScheduleReport{
			Order:    nil,
			Swaps:    0,
			Prefixes: []PrefixReport{baseReport},
			Outcome:  OutcomeUndetermined,
		}, nil
	}

	fullMask := (1 << n) - 1
	const unreachable = int(^uint(0) >> 1)
	cost := make([]int, 1<<n)
	for index := range cost {
		cost[index] = unreachable
	}
	cost[0] = 0
	sequences := make([][]GroupID, 1<<n)
	sequences[0] = []GroupID{}

	laterMask := make([]int, n)
	for bit := 0; bit < n; bit++ {
		for next := bit + 1; next < n; next++ {
			laterMask[bit] |= 1 << next
		}
	}

	for mask := 0; mask <= fullMask; mask++ {
		if cost[mask] == unreachable {
			continue
		}
		for bit := 0; bit < n; bit++ {
			flag := 1 << bit
			if mask&flag != 0 || predecessors[bit]&^mask != 0 {
				continue
			}
			nextMask := mask | flag
			verdict, _ := maskOutcome(nextMask)
			if verdict != maskValid {
				continue
			}
			addedCost := bits.OnesCount(uint(mask & laterMask[bit]))
			candidateCost := cost[mask] + addedCost
			candidateSequence := append(append([]GroupID(nil), sequences[mask]...), compiled.order[bit])
			if candidateCost < cost[nextMask] {
				cost[nextMask] = candidateCost
				sequences[nextMask] = candidateSequence
				continue
			}
			if candidateCost == cost[nextMask] && lexLess(candidateSequence, sequences[nextMask]) {
				sequences[nextMask] = candidateSequence
			}
		}
	}

	if cost[fullMask] == unreachable {
		outcome := OutcomeNonCompliant
		if sawUndetermined {
			outcome = OutcomeUndetermined
		}
		return ScheduleReport{Outcome: outcome}, nil
	}

	order := sequences[fullMask]
	prefixReports := make([]PrefixReport, 0, n+1)
	mask := 0
	prefixReports = append(prefixReports, reports[0])
	for index, id := range order {
		mask |= 1 << bitByID[id]
		report := reports[mask]
		prefixReport := PrefixReport{
			Applied:         append([]GroupID(nil), order[:index+1]...),
			Generation:      report.Generation,
			Outcome:         report.Outcome,
			Violations:      append([]Violation(nil), report.Violations...),
			Probes:          append([]ProbeResult(nil), report.Probes...),
			Active:          append([]ActiveResult(nil), report.Active...),
			States:          report.States,
			Transitions:     report.Transitions,
			BudgetExhausted: report.BudgetExhausted,
		}
		prefixReports = append(prefixReports, prefixReport)
	}
	return ScheduleReport{
		Order:    append([]GroupID(nil), order...),
		Swaps:    cost[fullMask],
		Prefixes: prefixReports,
		Outcome:  OutcomeCompliant,
	}, nil
}

func lexLess(left, right []GroupID) bool {
	for index := 0; index < len(left) && index < len(right); index++ {
		if left[index] != right[index] {
			return left[index] < right[index]
		}
	}
	return len(left) < len(right)
}

// AdjacentSwapDistance returns the exact minimum number of adjacent swaps
// needed to turn original into reordered.
func AdjacentSwapDistance(original, reordered []GroupID) (int, error) {
	if len(original) != len(reordered) {
		return 0, fmt.Errorf("orders have different lengths")
	}
	position := make(map[GroupID]int, len(original))
	for index, id := range original {
		if _, exists := position[id]; exists {
			return 0, fmt.Errorf("original repeats group %d", id)
		}
		position[id] = index
	}
	ranks := make([]int, len(reordered))
	seen := make(map[GroupID]struct{}, len(reordered))
	for index, id := range reordered {
		rank, ok := position[id]
		if !ok {
			return 0, fmt.Errorf("reordered contains unknown group %d", id)
		}
		if _, exists := seen[id]; exists {
			return 0, fmt.Errorf("reordered repeats group %d", id)
		}
		seen[id] = struct{}{}
		ranks[index] = rank
	}
	swaps := 0
	for i := 0; i < len(ranks); i++ {
		for j := i + 1; j < len(ranks); j++ {
			if ranks[i] > ranks[j] {
				swaps++
			}
		}
	}
	return swaps, nil
}
