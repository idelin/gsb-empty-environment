package desim

import (
	"fmt"
	"strings"
)

// checkpoint binds a history length to a state snapshot. Only the state
// snapshot is needed: the pending future is reconstructed from the
// recorded history tail and the live queue at undo time.
type checkpoint struct {
	historyLen int
	snap       interface{}
}

// Compensation records that an externally confirmed effect could not be
// undone and therefore requires a compensating action.
type Compensation struct {
	Event  *Event
	Reason string
}

// UndoReport describes the result of an Undo: the causal chain of the
// target, which events were undone, which pending successors were
// cancelled, which external effects produced compensation requirements,
// which checkpoint was used, and the replay log.
type UndoReport struct {
	Target           *Event
	CausalChain      []*Event
	Undone           []*Event
	CancelledPending []*Event
	Compensations    []Compensation
	CheckpointLen    int // history length of the checkpoint replay started from
	ReplayLog        []string
}

func (r *UndoReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "undo of #%d %q @ %s\n", r.Target.ID, r.Target.Name, r.Target.Key)
	b.WriteString("causal chain:\n")
	for _, ev := range r.CausalChain {
		fmt.Fprintf(&b, "  #%d %q @ %s [%s]\n", ev.ID, ev.Name, ev.Key, eventStatus(ev))
	}
	fmt.Fprintf(&b, "checkpoint: history length %d\n", r.CheckpointLen)
	b.WriteString("replay log:\n")
	for _, line := range r.ReplayLog {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	return b.String()
}

func eventStatus(ev *Event) string {
	switch {
	case ev.undone:
		return "undone"
	case ev.cancelled:
		return "cancelled"
	case ev.executed && ev.External:
		return "executed,external"
	case ev.executed:
		return "executed"
	default:
		return "pending"
	}
}

// Checkpoint records a snapshot of the user state at the current
// history position. It must be called while the engine is idle (not
// from inside a handler).
func (e *Engine) Checkpoint() error {
	if e.executing || e.replaying {
		return fmt.Errorf("desim: checkpoint from inside a handler not allowed")
	}
	if e.state == nil {
		return fmt.Errorf("desim: checkpoint requires an engine State")
	}
	e.checkpoints = append(e.checkpoints, checkpoint{len(e.history), e.state.Snapshot()})
	return nil
}

// CausalChain returns the target event plus all transitive causal
// successors, parents before children.
func (e *Engine) CausalChain(id EventID) []*Event {
	if _, ok := e.events[id]; !ok {
		return nil
	}
	var out []*Event
	var walk func(id EventID)
	walk = func(id EventID) {
		out = append(out, e.events[id])
		for _, c := range e.children[id] {
			walk(c)
		}
	}
	walk(id)
	return out
}

// FormatCausalChain renders the causal tree of an event with status
// annotations.
func (e *Engine) FormatCausalChain(id EventID) string {
	if _, ok := e.events[id]; !ok {
		return ""
	}
	var b strings.Builder
	var walk func(id EventID, depth int)
	walk = func(id EventID, depth int) {
		ev := e.events[id]
		fmt.Fprintf(&b, "%s#%d %q @ %s [%s]\n",
			strings.Repeat("  ", depth), ev.ID, ev.Name, ev.Key, eventStatus(ev))
		for _, c := range e.children[id] {
			walk(c, depth+1)
		}
	}
	walk(id, 0)
	return b.String()
}

// Undo reverses an executed event and its whole causal successor chain.
//
// Executed non-external members of the chain are undone by restoring
// the nearest checkpoint at or before the earliest affected history
// position and replaying the remaining history without them. Pending
// members of the chain are cancelled. Externally confirmed effects are
// never undone; each produces a Compensation requirement instead.
func (e *Engine) Undo(id EventID) (*UndoReport, error) {
	if e.executing || e.replaying {
		return nil, fmt.Errorf("desim: undo from inside a handler not allowed")
	}
	if e.state == nil {
		return nil, fmt.Errorf("desim: undo requires an engine State")
	}
	target, ok := e.events[id]
	if !ok {
		return nil, fmt.Errorf("desim: undo: unknown event %d", id)
	}
	if !target.executed {
		return nil, fmt.Errorf("desim: undo: event %d (%q) has not executed; use Cancel", id, target.Name)
	}
	if target.undone {
		return nil, fmt.Errorf("desim: undo: event %d (%q) already undone", id, target.Name)
	}

	chain := e.CausalChain(id)
	report := &UndoReport{Target: target, CausalChain: chain}
	undoSet := map[EventID]bool{}
	for _, ev := range chain {
		switch {
		case ev.executed && !ev.undone:
			if ev.External {
				if !ev.compens {
					ev.compens = true
					comp := Compensation{Event: ev, Reason: "externally confirmed effect cannot be undone"}
					e.compensations = append(e.compensations, comp)
					report.Compensations = append(report.Compensations, comp)
					report.ReplayLog = append(report.ReplayLog,
						fmt.Sprintf("COMPENSATE #%d %q @ %s (externally confirmed, effect retained)", ev.ID, ev.Name, ev.Key))
				}
			} else {
				undoSet[ev.ID] = true
				report.Undone = append(report.Undone, ev)
			}
		case !ev.executed && !ev.cancelled:
			if e.removeFromQueue(ev.ID) {
				ev.cancelled = true
				report.CancelledPending = append(report.CancelledPending, ev)
				report.ReplayLog = append(report.ReplayLog,
					fmt.Sprintf("CANCEL-PENDING #%d %q @ %s (causal successor)", ev.ID, ev.Name, ev.Key))
			}
		}
	}

	if len(undoSet) > 0 {
		e.rollbackAndReplay(undoSet, report)
	}
	e.replayLog = append([]string(nil), report.ReplayLog...)
	return report, nil
}

// rollbackAndReplay restores the nearest checkpoint at or before the
// earliest undone event and replays the recorded history tail without
// the undone events. Scheduling is suppressed during replay: the
// recorded history and the live queue already describe the future.
func (e *Engine) rollbackAndReplay(undoSet map[EventID]bool, report *UndoReport) {
	earliest := len(e.history)
	for _, ev := range report.Undone {
		if ev.execIdx < earliest {
			earliest = ev.execIdx
		}
	}

	// Nearest checkpoint at or before the earliest affected position.
	// The genesis checkpoint always qualifies.
	cpIdx := -1
	for i, cp := range e.checkpoints {
		if cp.historyLen <= earliest && (cpIdx == -1 || cp.historyLen > e.checkpoints[cpIdx].historyLen) {
			cpIdx = i
		}
	}
	cp := e.checkpoints[cpIdx]
	report.CheckpointLen = cp.historyLen

	// Checkpoints beyond the rollback point belong to the abandoned
	// future; drop them.
	kept := e.checkpoints[:0]
	for _, c := range e.checkpoints {
		if c.historyLen <= cp.historyLen {
			kept = append(kept, c)
		}
	}
	e.checkpoints = kept

	e.state.Restore(cp.snap)
	tail := e.history[cp.historyLen:]
	newHistory := make([]*Event, 0, len(e.history))
	newHistory = append(newHistory, e.history[:cp.historyLen]...)

	e.replaying = true
	for _, ev := range tail {
		if undoSet[ev.ID] {
			ev.undone = true
			report.ReplayLog = append(report.ReplayLog,
				fmt.Sprintf("SKIP    #%d %q @ %s (undone)", ev.ID, ev.Name, ev.Key))
			continue
		}
		ev.handler(&Context{eng: e, event: ev})
		ev.execIdx = len(newHistory)
		newHistory = append(newHistory, ev)
		report.ReplayLog = append(report.ReplayLog,
			fmt.Sprintf("REPLAY  #%d %q @ %s", ev.ID, ev.Name, ev.Key))
	}
	e.replaying = false
	e.history = newHistory
}

// Resimulate performs a full from-scratch simulation: it restores the
// genesis snapshot, replays the entire current history, and returns the
// resulting state snapshot. The engine's own state is left untouched.
// Comparing the result with the live state cross-checks every
// checkpoint/rollback the engine has performed.
func (e *Engine) Resimulate() (interface{}, error) {
	if e.state == nil {
		return nil, fmt.Errorf("desim: resimulate requires an engine State")
	}
	current := e.state.Snapshot()
	e.state.Restore(e.checkpoints[0].snap)
	e.replaying = true
	for _, ev := range e.history {
		ev.handler(&Context{eng: e, event: ev})
	}
	e.replaying = false
	fresh := e.state.Snapshot()
	e.state.Restore(current)
	return fresh, nil
}
