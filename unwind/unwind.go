package unwind

import (
	"context"
	"errors"
)

// ErrNoProgress reports that unwinding reached a register state already
// seen before: either a frame that maps to itself or a longer cycle. In
// both cases no further frames can be confirmed.
var ErrNoProgress = errors.New("unwind: no-progress cycle (register state repeats)")

// Result is the outcome of an unwind attempt.
type Result struct {
	// Frames holds every confirmed register state, starting with the
	// initial (innermost) one. It is never empty when Err is nil or not:
	// confirmed frames are preserved even when unwinding stops early.
	Frames []Regs
	// Gap is the exact missing memory range when Err is a read failure.
	Gap *Gap
	// Err is nil on complete success. It is ErrNoProgress when a cycle
	// stopped the walk, a *GapError when memory was missing, or the
	// context error when the caller cancelled.
	Err error
}

// Unwinder walks a stack using caller-supplied frame rules over a Memory.
type Unwinder struct {
	mem *Memory
}

// NewUnwinder returns an Unwinder reading stack memory from mem.
func NewUnwinder(mem *Memory) *Unwinder { return &Unwinder{mem: mem} }

// Unwind applies steps in order, starting from start. Each step's rules are
// evaluated simultaneously against the previous confirmed register set, so
// rules within one frame never observe each other's results.
//
// The walk stops when steps are exhausted, when a register state repeats
// (ErrNoProgress), when a rule needs memory that is unavailable (the
// confirmed frames and the exact Gap are kept), or when ctx is cancelled.
func (u *Unwinder) Unwind(ctx context.Context, start Regs, steps []Frame) Result {
	res := Result{Frames: []Regs{start.Clone()}}
	seen := map[string]struct{}{start.key(): {}}
	cur := start.Clone()

	for _, f := range steps {
		if err := ctx.Err(); err != nil {
			res.Err = err
			return res
		}
		next := cur.Clone()
		for slot := 0; slot < len(f) && slot < len(cur); slot++ {
			rule := f[slot]
			if rule == nil {
				continue
			}
			v, err := rule.Eval(slot, cur, ctx, u.mem)
			if err != nil {
				var ge *GapError
				if errors.As(err, &ge) {
					g := ge.Gap
					res.Gap = &g
				}
				res.Err = err
				return res
			}
			next[slot] = v
		}
		if _, ok := seen[next.key()]; ok {
			res.Err = ErrNoProgress
			return res
		}
		seen[next.key()] = struct{}{}
		res.Frames = append(res.Frames, next)
		cur = next
	}
	return res
}
