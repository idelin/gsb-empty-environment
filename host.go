package netsec

import (
	"errors"
	"fmt"
)

// Host is the verification-host interface invoked by OpHost instructions.
// Call receives the host identifier selected by the instruction and the
// argument read from a variable, and returns a bounded result plus a fault
// flag. A fault makes the machine raise ExcHost and stop; every call,
// including faulting ones, is recorded in the trace's host-call sequence.
type Host interface {
	Call(hostID uint8, arg uint64) (result uint64, fault bool)
}

// HostFunc adapts a plain function to the Host interface.
type HostFunc func(hostID uint8, arg uint64) (uint64, bool)

// Call implements Host.
func (f HostFunc) Call(hostID uint8, arg uint64) (uint64, bool) {
	return f(hostID, arg)
}

// ErrReplayMismatch is returned by Replay when re-execution does not reproduce
// the recorded trace exactly.
var ErrReplayMismatch = errors.New("replay did not reproduce the recorded trace")

// replayHost answers host calls from a pre-recorded sequence, in order, and
// verifies that the replayed program asks for the same calls again.
type replayHost struct {
	calls []Call
	next  int
}

func (h *replayHost) Call(hostID uint8, arg uint64) (uint64, bool) {
	if h.next >= len(h.calls) {
		return 0, true
	}
	recorded := h.calls[h.next]
	h.next++
	if recorded.HostID != hostID || recorded.Arg != arg {
		return 0, true
	}
	return recorded.Result, recorded.Fault
}

// Replay deterministically re-executes a program using the host calls captured
// in trace, without contacting the real verification host. The freshly
// produced trace must be identical to the recorded one; otherwise
// ErrReplayMismatch is returned. This makes a counterexample's two state
// sequences independently replayable.
func Replay(p *Program, trace *Trace) (*Trace, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	if trace == nil || len(trace.Initial) != len(p.Vars) {
		return nil, fmt.Errorf("replay: trace does not match program")
	}
	rh := &replayHost{calls: recordedCalls(trace)}
	got := execute(p, append([]uint64(nil), trace.Initial...), rh)
	if rh.next != len(rh.calls) || !tracesEqual(got, trace) {
		return got, ErrReplayMismatch
	}
	return got, nil
}

func recordedCalls(t *Trace) []Call {
	out := make([]Call, 0)
	for _, step := range t.Steps {
		if step.Call != nil {
			out = append(out, *step.Call)
		}
	}
	return out
}

func tracesEqual(a, b *Trace) bool {
	if a.Return != b.Return || a.Exc != b.Exc || a.ExcPC != b.ExcPC {
		return false
	}
	if len(a.Initial) != len(b.Initial) || len(a.Steps) != len(b.Steps) {
		return false
	}
	for i := range a.Initial {
		if a.Initial[i] != b.Initial[i] {
			return false
		}
	}
	for i := range a.Steps {
		sa, sb := a.Steps[i], b.Steps[i]
		if sa.Instr != sb.Instr || sa.State.PC != sb.State.PC {
			return false
		}
		if len(sa.State.Vars) != len(sb.State.Vars) {
			return false
		}
		for j := range sa.State.Vars {
			if sa.State.Vars[j] != sb.State.Vars[j] {
				return false
			}
		}
		if (sa.Call == nil) != (sb.Call == nil) {
			return false
		}
		if sa.Call != nil && *sa.Call != *sb.Call {
			return false
		}
	}
	return true
}
