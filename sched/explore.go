package sched

import "fmt"

// Event is one scheduling step in a trace.
type Event struct {
	Thread int
	Text   string
}

func (e Event) String() string {
	return fmt.Sprintf("T%d: %s", e.Thread, e.Text)
}

type ViolationKind string

const (
	ViolationAssert     ViolationKind = "assertion"
	ViolationDeadlock   ViolationKind = "deadlock"
	ViolationDivergence ViolationKind = "divergence"
)

// Violation describes a failed run. Trace is the shortest sequence of
// scheduling steps (within the explored bound) that leads to it.
type Violation struct {
	Kind    ViolationKind
	Message string
	Trace   []Event
}

type Options struct {
	MaxSteps  int // bound on the number of scheduling steps per trace
	MaxStates int // safety cap on explored states (0 = unlimited)
	// FindAll keeps exploring after the first violation instead of
	// returning immediately; Result.Violation still holds the first
	// (shortest) one found.
	FindAll bool
	// Visit, if set, is called with every distinct state reached.
	Visit func(State)
}

type Result struct {
	Violation *Violation
	Explored  int  // distinct states dequeued and expanded
	Merged    int  // successors dropped because their state was already seen
	Truncated bool // gave up early: step bound or state cap cut exploration off
}

type node struct {
	state  State
	depth  int
	parent int // index into nodes, -1 for the root
	event  Event
}

// Explore enumerates every distinct cooperative schedule of m, breadth
// first, so the first violation found has a shortest trace. Successor
// states are merged by State.Key, which includes each thread's wait
// state; branches that differ only in runnability are therefore kept
// distinct.
func Explore(m *Machine, opts Options) (Result, error) {
	if err := m.Validate(); err != nil {
		return Result{}, err
	}
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 64
	}

	var res Result
	nodes := []node{{state: m.Initial(), depth: 0, parent: -1}}
	seen := map[string]bool{nodes[0].state.Key(): true}
	if opts.Visit != nil {
		opts.Visit(nodes[0].state)
	}

	pathTo := func(idx int) []Event {
		var rev []Event
		for i := idx; i > 0; i = nodes[i].parent {
			rev = append(rev, nodes[i].event)
		}
		trace := make([]Event, 0, len(rev))
		for i := len(rev) - 1; i >= 0; i-- {
			trace = append(trace, rev[i])
		}
		return trace
	}

	for head := 0; head < len(nodes); head++ {
		cur := nodes[head]
		res.Explored++

		var runnable []int
		blocked := false
		for tid := range m.Progs {
			switch {
			case m.Runnable(&cur.state, tid):
				runnable = append(runnable, tid)
			case !m.Done(&cur.state, tid):
				blocked = true
			}
		}

		if len(runnable) == 0 {
			if blocked {
				v := &Violation{
					Kind:    ViolationDeadlock,
					Message: "deadlock: every live thread is blocked in wait",
					Trace:   pathTo(head),
				}
				if !opts.FindAll {
					res.Violation = v
					return res, nil
				}
				if res.Violation == nil {
					res.Violation = v
				}
			}
			continue // all threads finished
		}

		if cur.depth >= opts.MaxSteps {
			res.Truncated = true
			continue
		}

		for _, tid := range runnable {
			next, ev, v := m.Step(cur.state, tid)
			if v != nil {
				v.Trace = append(pathTo(head), ev)
				if !opts.FindAll {
					res.Violation = v
					return res, nil
				}
				if res.Violation == nil {
					res.Violation = v
				}
				continue
			}
			key := next.Key()
			if seen[key] {
				res.Merged++
				continue
			}
			seen[key] = true
			nodes = append(nodes, node{state: next, depth: cur.depth + 1, parent: head, event: ev})
			if opts.Visit != nil {
				opts.Visit(next)
			}
			if opts.MaxStates > 0 && len(nodes) > opts.MaxStates {
				res.Truncated = true
				return res, nil
			}
		}
	}
	return res, nil
}
