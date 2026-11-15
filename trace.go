package netsec

import "fmt"

// EventKind identifies the only two observable events emitted by the VM: a
// read from an addressed byte and the outcome of a value-dependent branch.
type EventKind uint8

const (
	EventRead EventKind = iota + 1
	EventBranch
)

func (k EventKind) String() string {
	switch k {
	case EventRead:
		return "read"
	case EventBranch:
		return "branch"
	default:
		return "unknown"
	}
}

// Event is one observation in an execution trace. PC is diagnostic metadata;
// Address and Taken are the security-relevant observations. For EventRead,
// Address is the absolute byte address that was read. For EventBranch, Taken
// is the branch result.
type Event struct {
	Kind    EventKind
	PC      uint32
	Address uint32
	Taken   bool
}

func (e Event) String() string {
	if e.Kind == EventRead {
		return fmt.Sprintf("read(pc=%d,addr=0x%08x)", e.PC, e.Address)
	}
	return fmt.Sprintf("branch(pc=%d,taken=%t)", e.PC, e.Taken)
}

// Trace is the sequence of addressed reads and branch outcomes produced while
// executing a program. It deliberately contains no wall-clock measurements.
type Trace []Event

// Clone returns an independent copy of t.
func (t Trace) Clone() Trace {
	if t == nil {
		return nil
	}
	out := make(Trace, len(t))
	copy(out, t)
	return out
}

// Equal reports whether two traces contain exactly the same observations.
func (t Trace) Equal(other Trace) bool {
	if len(t) != len(other) {
		return false
	}
	for i := range t {
		if t[i] != other[i] {
			return false
		}
	}
	return true
}

// Difference describes the first position at which two traces differ.
type Difference struct {
	Step             int
	Left             Event
	Right            Event
	LeftEnded        bool
	RightEnded       bool
	LeftTraceLength  int
	RightTraceLength int
}

// DiffTraces returns the first difference between left and right. The second
// return value is false when the traces are equal. A trace that has ended
// without a counterpart event is represented as a zero-valued Event with the
// corresponding *Ended flag set.
func DiffTraces(left, right Trace) (Difference, bool) {
	n := len(left)
	if len(right) < n {
		n = len(right)
	}
	for i := 0; i < n; i++ {
		if left[i] != right[i] {
			return Difference{
				Step:             i,
				Left:             left[i],
				Right:            right[i],
				LeftTraceLength:  len(left),
				RightTraceLength: len(right),
			}, true
		}
	}
	if len(left) == len(right) {
		return Difference{}, false
	}

	d := Difference{
		Step:             n,
		LeftTraceLength:  len(left),
		RightTraceLength: len(right),
	}
	if n == len(left) {
		d.LeftEnded = true
	} else {
		d.Left = left[n]
	}
	if n == len(right) {
		d.RightEnded = true
	} else {
		d.Right = right[n]
	}
	return d, true
}

// FirstDifference is a convenience wrapper around DiffTraces.
func (t Trace) FirstDifference(other Trace) (int, bool) {
	d, ok := DiffTraces(t, other)
	if !ok {
		return 0, false
	}
	return d.Step, true
}
