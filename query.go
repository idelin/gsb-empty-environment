package netsec

import "sort"

// Resolve selects the innermost protected entry that contains pc and whose
// action chain names typeID. If none matches, the only legal result is unwind.
func (t *Table) Resolve(pc uint64, typeID TypeID) Resolution {
	unwind := Resolution{
		Unwind:        true,
		ThrowingPC:    pc,
		TypeID:        typeID,
		EntryIndex:    -1,
		ActionStart:   NoAction,
		MatchedAction: -1,
	}
	if t == nil {
		return unwind
	}

	fnIdx := sort.Search(len(t.functions), func(i int) bool {
		return t.functions[i].fn.Start > pc
	}) - 1
	if fnIdx < 0 {
		return unwind
	}
	fn := &t.functions[fnIdx]
	if pc < fn.fn.Start || pc >= fn.fn.End {
		return unwind
	}

	best := -1
	var bestSpan uint64
	for _, ei := range fn.entryIdx {
		e := &t.entries[ei].entry
		if pc < e.Start || pc >= e.End {
			continue
		}
		if matchAction(t.actions, t.entries[ei].chain, typeID) < 0 {
			continue
		}
		span := e.End - e.Start
		if best < 0 || span < bestSpan {
			best = ei
			bestSpan = span
		}
	}
	if best < 0 {
		return unwind
	}

	e := &t.entries[best]
	trail := make([]ActionEvidence, len(e.chain))
	for i, ai := range e.chain {
		node := t.actions[ai]
		trail[i] = ActionEvidence{
			Index:   ai,
			TypeID:  node.TypeID,
			Cleanup: node.Cleanup,
		}
	}
	return Resolution{
		Unwind:         false,
		ThrowingPC:     pc,
		TypeID:         typeID,
		LandingPad:     e.entry.LandingPad,
		FunctionID:     e.entry.FunctionID,
		EntryIndex:     best,
		ProtectedStart: e.entry.Start,
		ProtectedEnd:   e.entry.End,
		Personality:    e.personality,
		ActionStart:    e.entry.ActionStart,
		Actions:        trail,
		MatchedAction:  matchAction(t.actions, e.chain, typeID),
	}
}

// Query is an alias for Resolve.
func (t *Table) Query(pc uint64, typeID TypeID) Resolution {
	return t.Resolve(pc, typeID)
}

// FindHandler returns the selected landing pad and whether a handler matched.
func (t *Table) FindHandler(pc uint64, typeID TypeID) (landingPad uint64, ok bool) {
	r := t.Resolve(pc, typeID)
	if r.Unwind {
		return 0, false
	}
	return r.LandingPad, true
}

func matchAction(actions []ActionNode, chain []int, typeID TypeID) int {
	for i, ai := range chain {
		if actions[ai].TypeID == typeID {
			return i
		}
	}
	return -1
}
