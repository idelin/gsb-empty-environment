package replayhandoff

import "sort"

type variable struct {
	node    NodeID
	entry   LogEntry
	allowed []uint64
}

type analysis struct {
	Decision
	variables   []variable
	bound       uint64
	horizonLow  uint64
	horizonHigh uint64
	anchors     []uint64
	conflicts   map[Conflict]struct{}
}

func (a *analysis) addConflict(conflict Conflict) {
	a.conflicts[conflict] = struct{}{}
}

func (a *analysis) hasConflict() bool {
	return len(a.conflicts) > 0
}

func (a *analysis) conflictSlice() []Conflict {
	conflicts := make([]Conflict, 0, len(a.conflicts))
	for conflict := range a.conflicts {
		conflicts = append(conflicts, conflict)
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Code != conflicts[j].Code {
			return conflicts[i].Code < conflicts[j].Code
		}
		if conflicts[i].Node != conflicts[j].Node {
			return conflicts[i].Node < conflicts[j].Node
		}
		if conflicts[i].Low != conflicts[j].Low {
			return conflicts[i].Low < conflicts[j].Low
		}
		return conflicts[i].Full < conflicts[j].Full
	})
	return conflicts
}

func candidateLow(candidate *Frame) uint64 {
	if candidate == nil {
		return 0
	}
	return candidate.LowSequence
}

func saturatingAdd(left, right uint64) uint64 {
	if left > ^uint64(0)-right {
		return ^uint64(0)
	}
	return left + right
}

func saturatingSub(left, right uint64) uint64 {
	if left <= right {
		return 0
	}
	return left - right
}

func matchingValues(low, lowBound, highBound, modulus uint64) []uint64 {
	if highBound < lowBound || low >= modulus {
		return nil
	}

	firstQ := lowBound / modulus
	lastQ := highBound / modulus
	values := make([]uint64, 0, 2)
	for quotient := firstQ; quotient <= lastQ; quotient++ {
		if quotient > firstQ && quotient-firstQ > 2 {
			break
		}
		full := quotient*modulus + low
		if full >= lowBound && full <= highBound {
			values = append(values, full)
		}
	}
	return values
}

func containsUint64(values []uint64, target uint64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func unionAllowed(variables []variable, indices []int) []uint64 {
	values := make([]uint64, 0)
	for _, index := range indices {
		for _, value := range variables[index].allowed {
			if !containsUint64(values, value) {
				values = append(values, value)
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}

func sortedUnique(values []uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
