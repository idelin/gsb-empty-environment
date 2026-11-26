package sdf

import (
	"sort"
)

// inflight is one running firing: a descriptor is reserved at start and the
// produced tokens plus output slots are released only at end.
type inflight struct {
	actor    int
	end      int64
	launched int64
	prod     []edgeTok // internal productions
	outProd  []edgeTok // external output productions
}

type edgeTok struct {
	edge int
	n    int
}

type timedState struct {
	now       int64
	occ       []int // backlog per edge
	resv      []int // reserved output slots per internal edge
	flights   []inflight
	busy      bool
	busyUntil int64
	cursors   []arrivalCursor
	inputEids []int
	totalIn   int64
	totalOut  int64
	fireCount []int64
}

type timedOutcome struct {
	status   Status
	reason   string
	sched    *Schedule
	windowOK bool
	badAt    int64 // input ordinal (1-based) of the first violated window
}

const eventCap = 200_000

// simulate runs the deterministic self-timed policy and proves eventual
// periodicity by detecting an identical normalized state snapshot at two run
// boundaries. It never asserts stability from a fixed number of rounds.
func simulate(p *Problem, idx *index, caps []int, q []int64) timedOutcome {
	E := len(p.Edges)
	st := timedState{
		occ:       make([]int, E),
		resv:      make([]int, E),
		fireCount: make([]int64, p.Actors),
	}
	for eid, e := range p.Edges {
		st.occ[eid] = e.Delay
	}
	st.inputEids = append(st.inputEids, idx.inputEdgeIDs()...)
	sort.Ints(st.inputEids)
	for _, eid := range st.inputEids {
		st.cursors = append(st.cursors, newCursor(idx.inputPat[eid]))
	}

	var events []Event
	record := func(ev Event) {
		ev.InInputs = st.totalIn
		ev.OutOutputs = st.totalOut
		if len(events) < eventCap {
			events = append(events, ev)
		}
	}

	snapAt := map[string]int{}
	var cycleFrom int
	var cycleKey string
	cycleFound := false

	for {
		if len(events) >= eventCap && !cycleFound {
			return timedOutcome{status: StatusInconclusive, reason: "event envelope exhausted before state repeat"}
		}
		// No external input streams: drive the ring from quiescent states.
		if len(st.cursors) == 0 && len(st.flights) == 0 {
			if key, ready := quiescentSnapshot(p, idx, &st, caps); ready {
				if !cycleFound {
					if first, ok := snapAt[key]; ok {
						cycleFound = true
						cycleFrom = first
						cycleKey = key
					} else {
						snapAt[key] = len(events)
					}
				} else if key == cycleKey && len(events) > cycleFrom {
					return finalizeTimed(p, idx, caps, q, events, cycleFrom, st)
				}
				if !launchAll(p, idx, &st, caps, record) {
					return timedOutcome{status: StatusInconclusive, reason: "firing end time overflowed int64"}
				}
				continue
			}
			_, se, fe := firstBlocked(p, idx, &st, caps)
			reason := "deadlock: no firing can proceed"
			if se >= 0 {
				reason = describeStarve(p, se)
			} else if fe >= 0 {
				reason = describeFull(p, fe)
			}
			return timedOutcome{status: StatusDeadlock, reason: reason}
		}
		// Advance time to the next completion or external arrival, then make
		// every input due at that time available. This never walks idle ticks
		// one by one: long idle gaps are jumped arithmetically.
		next, ok := nextEventTime(&st)
		if !ok {
			if len(st.flights) == 0 {
				_, se, fe := firstBlocked(p, idx, &st, caps)
				reason := "deadlock: no firing can proceed"
				if se >= 0 {
					reason = describeStarve(p, se)
				} else if fe >= 0 {
					reason = describeFull(p, fe)
				}
				return timedOutcome{status: StatusDeadlock, reason: reason}
			}
			return timedOutcome{status: StatusInconclusive, reason: "no schedulable event"}
		}
		st.now = next
		completeAt(p, idx, &st, record)
		// Quiescent period boundary: every input cursor is at a fresh period
		// and no firing is in flight. Snapshotting here (before injecting the
		// new period's tokens) yields a clean steady state whose repeat span is
		// an integer multiple of the input period.
		if atPeriodBoundary(&st) && len(st.flights) == 0 {
			key := snapshotKey(p, &st)
			if !cycleFound {
				if first, ok := snapAt[key]; ok {
					cycleFound = true
					cycleFrom = first
					cycleKey = key
				} else {
					snapAt[key] = len(events)
				}
			} else if key == cycleKey && len(events) > cycleFrom {
				return finalizeTimed(p, idx, caps, q, events, cycleFrom, st)
			}
		}
		if injectOverrun(p, idx, &st, caps, record) {
			return timedOutcome{status: StatusOverrun, reason: describeFull(p, overrunEdgeAt(&st, st.now))}
		}
		if !launchAll(p, idx, &st, caps, record) {
			return timedOutcome{status: StatusInconclusive, reason: "firing end time overflowed int64"}
		}
	}
}

func (idx *index) inputEdgeIDs() []int {
	out := make([]int, 0, len(idx.inputPat))
	for eid := range idx.inputPat {
		out = append(out, eid)
	}
	return out
}

func nextEventTime(st *timedState) (int64, bool) {
	best := int64(1<<63 - 1)
	found := false
	for ci := range st.cursors {
		t := st.cursors[ci].nextTime()
		if t < best {
			best, found = t, true
		}
	}
	for _, f := range st.flights {
		if f.end < best {
			best, found = f.end, true
		}
	}
	return best, found
}

// atPeriodBoundary reports whether every input cursor is exactly at the first
// token of a fresh input period. Snapshots taken only at these instants carry
// an unambiguous phase, so a repeated key is a genuine periodic state.
func atPeriodBoundary(st *timedState) bool {
	if len(st.cursors) == 0 {
		// No external inputs: every steady instant is equivalent.
		return len(st.flights) == 0
	}
	for ci := range st.cursors {
		if st.cursors[ci].next != 0 {
			return false
		}
	}
	return true
}

func overrunEdgeAt(st *timedState, now int64) int {
	for ci, eid := range st.inputEids {
		if st.cursors[ci].nextTime() <= now {
			return eid
		}
	}
	return -1
}
