package unwind

import "context"

// Regs is a snapshot of the machine registers for one frame. Slots are
// architecture-specific indices (e.g. PC, SP, FP) chosen by the caller.
type Regs []uint64

// Clone returns an independent copy of r.
func (r Regs) Clone() Regs {
	out := make(Regs, len(r))
	copy(out, r)
	return out
}

// Equal reports whether r and o hold identical values in every slot.
func (r Regs) Equal(o Regs) bool {
	if len(r) != len(o) {
		return false
	}
	for i := range r {
		if r[i] != o[i] {
			return false
		}
	}
	return true
}

// key returns a canonical comparable form of r for cycle detection.
func (r Regs) key() string {
	b := make([]byte, 8*len(r))
	for i, v := range r {
		putle64(b[i*8:], v)
	}
	return string(b)
}

// Rule computes the value of one register slot in the caller frame.
//
// Every rule of a frame is evaluated against the same old register set:
// no rule may observe the result of another rule of the same step. This
// simultaneous-evaluation semantics mirrors call-frame-information rules
// and keeps the unwinding order-independent.
type Rule interface {
	// Eval computes the new value for register slot, given the complete
	// old register set. It may read memory through m.
	Eval(slot int, old Regs, ctx context.Context, m *Memory) (uint64, error)
}

// Same keeps the register's current value: new[i] = old[i].
type Same struct{}

// Eval implements Rule.
func (Same) Eval(slot int, old Regs, _ context.Context, _ *Memory) (uint64, error) {
	return old[slot], nil
}

// Const sets the register to a constant: new[i] = c.
type Const uint64

// Eval implements Rule.
func (c Const) Eval(_ int, _ Regs, _ context.Context, _ *Memory) (uint64, error) {
	return uint64(c), nil
}

// Reg copies another register from the old set: new[i] = old[j].
type Reg int

// Eval implements Rule.
func (j Reg) Eval(_ int, old Regs, _ context.Context, _ *Memory) (uint64, error) {
	return old[int(j)], nil
}

// AddrOf computes an address from an old register: new[i] = old[j] + off.
type AddrOf struct {
	Reg int
	Off int64
}

// Eval implements Rule.
func (a AddrOf) Eval(_ int, old Regs, _ context.Context, _ *Memory) (uint64, error) {
	return old[a.Reg] + uint64(a.Off), nil
}

// Deref loads a word through an old register: new[i] = mem[old[j] + off].
// A missing page surfaces as a *GapError.
type Deref struct {
	Reg int
	Off int64
}

// Eval implements Rule.
func (d Deref) Eval(_ int, old Regs, ctx context.Context, m *Memory) (uint64, error) {
	return m.Uint64(ctx, old[d.Reg]+uint64(d.Off))
}

// Frame is the set of rules producing the caller's register set from the
// current one. A nil rule is treated as Same. Frames with fewer rules than
// registers leave the trailing registers unchanged.
type Frame []Rule
