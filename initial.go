package upgradeplan

// AllMultisetFleets enumerates every unordered multiset of deviceCount states
// from states. Because the publisher cannot distinguish device ordering, these
// multisets are the relevant distinct initial groups.
func AllMultisetFleets(states []DeviceState, deviceCount int) [][]DeviceState {
	if deviceCount == 0 {
		return [][]DeviceState{{}}
	}
	if deviceCount < 0 || len(states) == 0 {
		return nil
	}

	indices := make([]int, deviceCount)
	var out [][]DeviceState
	var walk func(position, start int)
	walk = func(position, start int) {
		if position == deviceCount {
			fleet := make([]DeviceState, deviceCount)
			for i, stateIndex := range indices {
				fleet[i] = states[stateIndex]
			}
			out = append(out, fleet)
			return
		}
		for stateIndex := start; stateIndex < len(states); stateIndex++ {
			indices[position] = stateIndex
			walk(position+1, stateIndex)
		}
	}
	walk(0, 0)
	return out
}

// AllCartesianFleets enumerates all labeled assignments. Reordered assignments
// canonicalize to the same Fleet/Belief keys; this helper is mainly useful in
// exhaustive tests.
func AllCartesianFleets(states []DeviceState, deviceCount int) [][]DeviceState {
	if deviceCount == 0 {
		return [][]DeviceState{{}}
	}
	if deviceCount < 0 || len(states) == 0 {
		return nil
	}
	counts := make([]int, deviceCount)
	for i := range counts {
		counts[i] = len(states)
	}
	assignments := cartesianAssignments(counts)
	out := make([][]DeviceState, 0, len(assignments))
	for _, assignment := range assignments {
		fleet := make([]DeviceState, deviceCount)
		for position, stateIndex := range assignment {
			fleet[position] = states[stateIndex]
		}
		out = append(out, fleet)
	}
	return out
}
