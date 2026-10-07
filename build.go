package perflib

import "fmt"

// DiagKind classifies malformed-event diagnostics.
type DiagKind int

const (
	// DiagEndWithoutStart: an End event arrived with an empty thread stack.
	DiagEndWithoutStart DiagKind = iota
	// DiagNameMismatch: an End event names no currently open frame.
	DiagNameMismatch
	// DiagUnclosedFrame: a frame was never closed by a matching End;
	// it is force-closed at the thread's last observed timestamp.
	DiagUnclosedFrame
	// DiagNegativeDuration: an End predates its frame's Start;
	// the frame is clamped to zero duration.
	DiagNegativeDuration
	// DiagOutOfOrder: an event's timestamp is earlier than the
	// previous event on the same thread.
	DiagOutOfOrder
)

// Diagnostic describes one malformed event stream condition.
type Diagnostic struct {
	Kind   DiagKind
	Thread uint64
	Time   int64
	Name   string
	Detail string
}

func (d Diagnostic) String() string {
	var kind string
	switch d.Kind {
	case DiagEndWithoutStart:
		kind = "end-without-start"
	case DiagNameMismatch:
		kind = "name-mismatch"
	case DiagUnclosedFrame:
		kind = "unclosed-frame"
	case DiagNegativeDuration:
		kind = "negative-duration"
	case DiagOutOfOrder:
		kind = "out-of-order"
	}
	return fmt.Sprintf("thread=%d t=%d %s: %s (%s)", d.Thread, d.Time, kind, d.Name, d.Detail)
}

// Trace is the result of building an event stream: a forest of call
// trees (roots are top-level calls, ordered by start time) plus any
// diagnostics produced while pairing the stack.
type Trace struct {
	Roots       []*CallNode
	Diagnostics []Diagnostic
}

// Build pairs start/end events into call trees. Events on the same
// thread must nest as a strict stack; events on different threads are
// independent (async children live on their own threads) and may be
// interleaved arbitrarily in the stream. Events are processed in input
// order; per-thread timestamps must be non-decreasing (same-timestamp
// events are fine) and regressions are reported as DiagOutOfOrder.
func Build(events []Event) *Trace {
	tr := &Trace{}

	type threadState struct {
		stack    []*CallNode
		lastTime int64
	}
	states := map[uint64]*threadState{}
	var order []uint64
	stateFor := func(th uint64) *threadState {
		st := states[th]
		if st == nil {
			st = &threadState{}
			states[th] = st
			order = append(order, th)
		}
		return st
	}

	closeFrame := func(n *CallNode, t int64) {
		n.End = t
		if n.End < n.Start {
			tr.Diagnostics = append(tr.Diagnostics, Diagnostic{
				Kind:   DiagNegativeDuration,
				Thread: n.Thread,
				Time:   t,
				Name:   n.Name,
				Detail: fmt.Sprintf("end %d before start %d, clamped", t, n.Start),
			})
			n.End = n.Start
		}
	}

	for _, ev := range events {
		st := stateFor(ev.Thread)
		if ev.Time < st.lastTime {
			tr.Diagnostics = append(tr.Diagnostics, Diagnostic{
				Kind:   DiagOutOfOrder,
				Thread: ev.Thread,
				Time:   ev.Time,
				Name:   ev.Name,
				Detail: fmt.Sprintf("clock regressed from %d", st.lastTime),
			})
		} else {
			st.lastTime = ev.Time
		}

		if ev.Kind == EventStart {
			n := &CallNode{Name: ev.Name, Thread: ev.Thread, Start: ev.Time}
			if len(st.stack) > 0 {
				parent := st.stack[len(st.stack)-1]
				n.Parent = parent
				parent.Children = append(parent.Children, n)
			} else {
				tr.Roots = append(tr.Roots, n)
			}
			st.stack = append(st.stack, n)
			continue
		}

		// End event: must match the innermost open frame.
		switch idx := findFrame(st.stack, ev.Name); {
		case len(st.stack) == 0:
			tr.Diagnostics = append(tr.Diagnostics, Diagnostic{
				Kind:   DiagEndWithoutStart,
				Thread: ev.Thread,
				Time:   ev.Time,
				Name:   ev.Name,
				Detail: "end event with empty stack",
			})
		case idx < 0:
			tr.Diagnostics = append(tr.Diagnostics, Diagnostic{
				Kind:   DiagNameMismatch,
				Thread: ev.Thread,
				Time:   ev.Time,
				Name:   ev.Name,
				Detail: fmt.Sprintf("no open frame named %q (top is %q)", ev.Name, st.stack[len(st.stack)-1].Name),
			})
		default:
			// Force-close any frames left open above the match.
			for len(st.stack)-1 > idx {
				top := st.stack[len(st.stack)-1]
				tr.Diagnostics = append(tr.Diagnostics, Diagnostic{
					Kind:   DiagUnclosedFrame,
					Thread: ev.Thread,
					Time:   ev.Time,
					Name:   top.Name,
					Detail: fmt.Sprintf("skipped by end of %q", ev.Name),
				})
				closeFrame(top, ev.Time)
				st.stack = st.stack[:len(st.stack)-1]
			}
			closeFrame(st.stack[len(st.stack)-1], ev.Time)
			st.stack = st.stack[:len(st.stack)-1]
		}
	}

	// Force-close frames still open when the stream ends, in
	// first-seen thread order for deterministic diagnostics.
	for _, th := range order {
		st := states[th]
		for len(st.stack) > 0 {
			top := st.stack[len(st.stack)-1]
			tr.Diagnostics = append(tr.Diagnostics, Diagnostic{
				Kind:   DiagUnclosedFrame,
				Thread: th,
				Time:   st.lastTime,
				Name:   top.Name,
				Detail: "stream ended with frame still open",
			})
			closeFrame(top, st.lastTime)
			st.stack = st.stack[:len(st.stack)-1]
		}
	}
	return tr
}

// findFrame returns the deepest stack index whose frame has the given
// name, or -1. Recursion (same name at several depths) resolves to the
// innermost frame, preserving strict LIFO pairing.
func findFrame(stack []*CallNode, name string) int {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].Name == name {
			return i
		}
	}
	return -1
}
