package prof

import (
	"fmt"
	"sort"
)

// DiagKind classifies stack-pairing problems found in the raw event stream.
type DiagKind int

const (
	// DiagOrphanEnd is an End event with no open call on its thread.
	DiagOrphanEnd DiagKind = iota
	// DiagMismatchEnd is an End event whose function does not match the
	// innermost open call and matches no enclosing open call either.
	DiagMismatchEnd
	// DiagUnclosedStart is a Start event that never saw its End; it is
	// force-closed (at a mismatched End, or at the thread's last timestamp).
	DiagUnclosedStart
)

// Diagnostic describes one recovered inconsistency in the event stream.
type Diagnostic struct {
	Kind   DiagKind
	Thread string
	Func   string
	Time   int64
	Detail string
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("thread %q t=%d func %q: %s", d.Thread, d.Time, d.Func, d.Detail)
}

// Call is one paired function activation.
type Call struct {
	Func     string
	Thread   string
	Start    int64
	End      int64
	Self     int64 // exclusive time: duration minus same-thread children
	Children []*Call
}

// Inclusive returns the wall duration of this activation.
func (c *Call) Inclusive() int64 { return c.End - c.Start }

// FuncStats aggregates all activations of a function, across threads.
//
// Inclusive and Exclusive are plain sums over activations: for recursive or
// concurrently-overlapping functions they exceed real elapsed time.
// WallUnion is computed with the time-slice occupancy model and is the union
// of all intervals in which the function is active on any thread, so
// WallUnion <= Inclusive always holds.
type FuncStats struct {
	Func      string
	Calls     int
	Inclusive int64
	Exclusive int64
	WallUnion int64
}

// Report is the result of analyzing an event stream.
type Report struct {
	Roots       []*Call
	Funcs       map[string]*FuncStats
	FuncOrder   []string // sorted function names, for deterministic output
	WallBusy    int64    // union of busy intervals across ALL threads
	ThreadBusy  map[string]int64
	Diagnostics []Diagnostic
}

// TotalInclusive sums inclusive durations of every root call. For async
// workloads this double-counts overlapping intervals; use WallBusy for the
// real elapsed busy time.
func (r *Report) TotalInclusive() int64 {
	var total int64
	for _, c := range r.Roots {
		total += c.Inclusive()
	}
	return total
}

// TotalExclusive sums exclusive time over every call. It equals the sum of
// per-thread busy times (each busy thread has exactly one innermost call
// accruing exclusive time at any instant), so it is >= WallBusy; the
// difference is exactly the cross-thread overlap.
func (r *Report) TotalExclusive() int64 {
	var total int64
	for _, s := range r.Funcs {
		total += s.Exclusive
	}
	return total
}

// Overlap returns how much of TotalExclusive is lost to cross-thread
// overlap when collapsed onto a single wall-clock axis.
func (r *Report) Overlap() int64 { return r.TotalExclusive() - r.WallBusy }

// Analyze pairs events into a call forest and computes inclusive/exclusive
// statistics plus a time-slice occupancy cross-check.
func Analyze(events []Event) *Report {
	byThread := make(map[string][]Event)
	threadOrder := []string{}
	for _, e := range events {
		if _, ok := byThread[e.Thread]; !ok {
			threadOrder = append(threadOrder, e.Thread)
		}
		byThread[e.Thread] = append(byThread[e.Thread], e)
	}
	// Stable sort keeps input order for same-timestamp events, which makes
	// zero-duration calls pair deterministically.
	for _, evs := range byThread {
		sort.SliceStable(evs, func(i, j int) bool { return evs[i].Time < evs[j].Time })
	}
	sort.Strings(threadOrder)

	rep := &Report{
		Funcs:      make(map[string]*FuncStats),
		ThreadBusy: make(map[string]int64),
	}
	var allCalls []*Call
	for _, thread := range threadOrder {
		m := &machine{thread: thread}
		m.run(byThread[thread])
		rep.Roots = append(rep.Roots, m.roots...)
		rep.Diagnostics = append(rep.Diagnostics, m.diags...)
		allCalls = append(allCalls, m.closed...)
	}

	for _, c := range allCalls {
		s := rep.Funcs[c.Func]
		if s == nil {
			s = &FuncStats{Func: c.Func}
			rep.Funcs[c.Func] = s
		}
		s.Calls++
		s.Inclusive += c.Inclusive()
		s.Exclusive += c.Self
	}
	for name := range rep.Funcs {
		rep.FuncOrder = append(rep.FuncOrder, name)
	}
	sort.Strings(rep.FuncOrder)

	// Time-slice occupancy model: independent of the tree math above.
	occ := buildOccupancy(allCalls)
	rep.WallBusy = occ.wallBusy
	rep.ThreadBusy = occ.threadBusy
	for name, u := range occ.funcUnion {
		rep.Funcs[name].WallUnion = u
	}
	return rep
}

// frame is an open (not yet closed) call on a thread's stack.
type frame struct {
	call     *Call
	childSum int64 // inclusive time of direct children closed so far
}

// machine is the per-thread strict-stack pairing state machine.
type machine struct {
	thread string
	stack  []frame
	roots  []*Call
	closed []*Call // every closed call, flat
	diags  []Diagnostic
}

func (m *machine) run(events []Event) {
	var lastTime int64
	for i, e := range events {
		if i == 0 || e.Time > lastTime {
			lastTime = e.Time
		}
		switch e.Type {
		case EventStart:
			m.stack = append(m.stack, frame{call: &Call{
				Func:   e.Func,
				Thread: m.thread,
				Start:  e.Time,
			}})
		case EventEnd:
			m.end(e)
		}
	}
	// Force-close anything still open at the thread's last timestamp.
	for len(m.stack) > 0 {
		top := m.stack[len(m.stack)-1]
		m.diags = append(m.diags, Diagnostic{
			Kind:   DiagUnclosedStart,
			Thread: m.thread,
			Func:   top.call.Func,
			Time:   lastTime,
			Detail: "start event never matched; closed at thread end",
		})
		m.closeTop(lastTime)
	}
}

func (m *machine) end(e Event) {
	if len(m.stack) == 0 {
		m.diags = append(m.diags, Diagnostic{
			Kind:   DiagOrphanEnd,
			Thread: m.thread,
			Func:   e.Func,
			Time:   e.Time,
			Detail: "end event with empty stack; ignored",
		})
		return
	}
	// Find the innermost open frame matching this End.
	match := -1
	for i := len(m.stack) - 1; i >= 0; i-- {
		if m.stack[i].call.Func == e.Func {
			match = i
			break
		}
	}
	if match < 0 {
		m.diags = append(m.diags, Diagnostic{
			Kind:   DiagMismatchEnd,
			Thread: m.thread,
			Func:   e.Func,
			Time:   e.Time,
			Detail: "end event matches no open call; ignored",
		})
		return
	}
	// Any frames opened after the match are unclosed starts: force-close
	// them at this timestamp so the strict-stack invariant is restored.
	for len(m.stack)-1 > match {
		top := m.stack[len(m.stack)-1]
		m.diags = append(m.diags, Diagnostic{
			Kind:   DiagUnclosedStart,
			Thread: m.thread,
			Func:   top.call.Func,
			Time:   e.Time,
			Detail: fmt.Sprintf("unclosed start interrupted by end of %q; force-closed", e.Func),
		})
		m.closeTop(e.Time)
	}
	m.closeTop(e.Time)
}

// closeTop pops the innermost frame, finalizes its exclusive time and
// attaches it to its parent (or the root list).
func (m *machine) closeTop(t int64) {
	top := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	c := top.call
	c.End = t
	c.Self = c.Inclusive() - top.childSum
	if c.Self < 0 { // defensive: strict nesting makes this impossible
		c.Self = 0
	}
	if n := len(m.stack); n > 0 {
		parent := &m.stack[n-1]
		parent.childSum += c.Inclusive()
		parent.call.Children = append(parent.call.Children, c)
	} else {
		m.roots = append(m.roots, c)
	}
	m.closed = append(m.closed, c)
}
