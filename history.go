package main

import "fmt"

// OpKind is the kind of a register operation.
type OpKind int

const (
	OpRead OpKind = iota
	OpWrite
)

func (k OpKind) String() string {
	if k == OpRead {
		return "read"
	}
	return "write"
}

// Op is one operation in a register history, with invocation/response
// intervals on a (virtual) clock. A pending op has been invoked but has
// no response yet (Pending=true, Respond unused).
//
// For a completed read, Val is the value the read returned.
// For a write, Val is the value written.
// A pending read is treated as a wildcard: its return value is unknown,
// so any register value satisfies it.
type Op struct {
	ID      int
	Kind    OpKind
	Val     int
	Invoke  int64
	Respond int64
	Pending bool
}

func (op Op) String() string {
	ival := fmt.Sprintf("[%d,%d]", op.Invoke, op.Respond)
	if op.Pending {
		ival = fmt.Sprintf("[%d,_]", op.Invoke)
	}
	if op.Kind == OpRead {
		if op.Pending {
			return fmt.Sprintf("#%d read()->?   %s", op.ID, ival)
		}
		return fmt.Sprintf("#%d read()->%d   %s", op.ID, op.Val, ival)
	}
	return fmt.Sprintf("#%d write(%d)  %s", op.ID, op.Val, ival)
}

// precedes reports the real-time order: a finishes before b starts.
// Pending ops have no response, so nothing is ordered after them.
func precedes(a, b Op) bool {
	return !a.Pending && a.Respond < b.Invoke
}

// Validate checks basic well-formedness of a history.
func Validate(ops []Op) error {
	seen := map[int]bool{}
	for _, op := range ops {
		if seen[op.ID] {
			return fmt.Errorf("duplicate op id %d", op.ID)
		}
		seen[op.ID] = true
		if op.Invoke < 0 {
			return fmt.Errorf("op %d: negative invoke time", op.ID)
		}
		if !op.Pending && op.Respond < op.Invoke {
			return fmt.Errorf("op %d: response %d before invoke %d", op.ID, op.Respond, op.Invoke)
		}
	}
	return nil
}
