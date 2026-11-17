package netsec

import (
	"errors"
	"fmt"
	"math/bits"
)

// MaxStateBits is the largest register width supported by this package.
// A wider register cannot be explored exhaustively in bounded memory.
const MaxStateBits = 24

// Common validation errors. Errors returned by this package wrap one of
// these values, so callers can use errors.Is.
var (
	ErrTooManyBits    = errors.New("netsec: register width exceeds 24 bits")
	ErrOutOfRange     = errors.New("netsec: value has bits outside the register")
	ErrInvalidProblem = errors.New("netsec: invalid problem")
	ErrTooLarge       = errors.New("netsec: state space exceeds exhaustive analysis limit")
	ErrNotApplicable  = errors.New("netsec: operation is not applicable")
	ErrUnsafePlan     = errors.New("netsec: plan enters a forbidden state")
	ErrGoalNotReached = errors.New("netsec: plan does not satisfy the goal")
	ErrIncomplete     = errors.New("netsec: planning result is incomplete")
)

// Condition matches a state when every bit selected by Mask has the value
// selected by Value. Bits outside Mask are ignored.
//
// A condition is useful both as a forbidden-state predicate and as a partial
// goal. For example, {Mask: 0b1010, Value: 0b1000} matches states whose bit 3
// is set and bit 1 is clear.
type Condition struct {
	Mask  uint32
	Value uint32
}

// Match reports whether state satisfies c.
func (c Condition) Match(state uint32) bool {
	return state&c.Mask == c.Value&c.Mask
}

// Operation is one atomic register write.
//
// The write first checks the precondition:
//
//	state & RequireMask == RequireValue & RequireMask
//
// If it holds, the bits in Mask are replaced by the corresponding bits of
// Value. Bits outside Mask are left unchanged. Value bits outside Mask are
// ignored. The write is rejected if it would clear a lock bit or change a bit
// frozen by an already-set lock (see LockRule).
type Operation struct {
	Name string

	// Mask selects the bits written by this operation.
	Mask uint32
	// Value supplies the new value of the selected bits.
	Value uint32

	// RequireMask/RequireValue form an optional precondition. A zero
	// RequireMask means the operation has no precondition.
	RequireMask  uint32
	RequireValue uint32
}

// Op is a short alias for Operation.
type Op = Operation

// LockRule associates a one-way lock bit with the bits it freezes.
//
// Bit is the position of the lock bit. Once it is one, no later operation may
// change any bit selected by Freezes. The operation that sets the lock is
// allowed to change frozen bits in the same atomic write; this is often how a
// configuration is committed. Lock rules are optional: lock bits listed in
// Problem.LockMask are always one-way even without a rule.
type LockRule struct {
	Bit     uint
	Freezes uint32
}

// Lock is a short alias for LockRule.
type Lock = LockRule

// Problem describes a register-planning instance.
//
// States are unsigned bit fields of width Bits. Every state, including the
// initial state and the target, must satisfy all forbidden-state checks.
type Problem struct {
	// Bits is the register width. It must be at most MaxStateBits. If Bits is
	// zero, the width is inferred from the highest bit used anywhere in the
	// problem; this is convenient for small tests and configuration files.
	Bits uint

	// Initial is the state before any write.
	Initial uint32
	// Target is the desired final state. It is used unless Goal is non-nil.
	Target uint32
	// TargetMask optionally makes Target a partial goal: only bits selected
	// by TargetMask must match. A zero TargetMask means the whole Target
	// state must match. Goal takes precedence over TargetMask.
	TargetMask uint32
	// Goal optionally replaces Target with a partial-state condition.
	Goal *Condition

	// LockMask lists one-way lock bits. A set bit in LockMask can never be
	// cleared. Lock bits named by Locks are also one-way.
	LockMask uint32
	// Locks optionally freeze additional bits while their lock bit is set.
	// If Locks is empty, the default policy is that setting any lock bit
	// freezes every non-lock bit (a locked register can no longer have its
	// permissions changed). Supply explicit rules for finer-grained locks,
	// or set SelfLockOnly to disable the default policy.
	Locks []LockRule
	// SelfLockOnly disables the default "any lock freezes all permissions"
	// policy. Lock bits still remain one-way.
	SelfLockOnly bool

	// Ops is the set of available writes. Operations are tried in slice order,
	// which provides deterministic tie-breaking among equally short plans.
	Ops []Operation

	// Forbidden lists state patterns that may never occur. A state matching
	// any entry is rejected.
	Forbidden []Condition
	// ForbiddenBits rejects any state in which at least one selected bit is
	// set. This is the common "these sensitive permissions must never be
	// opened" form of a forbidden zone.
	ForbiddenBits uint32
	// ForbiddenStates lists exact states that may never occur.
	ForbiddenStates []uint32
	// ForbiddenFunc is an optional arbitrary safety predicate. Returning true
	// marks the state as forbidden.
	ForbiddenFunc func(state uint32) bool

	// MaxSteps bounds the number of writes in a plan. Zero means unlimited.
	// Reaching the bound without finding the target produces an incomplete
	// result, not a proof of unreachability.
	MaxSteps int
	// MaxStates bounds the number of states remembered by the search. Zero
	// means unlimited. Exhausting it produces an incomplete result.
	MaxStates int
}

// Config and Register are aliases for Problem, for hosts that prefer those
// names.
type Config = Problem

// Register is an alias for Problem.
type Register = Problem

// normalized is an internal, validated form of Problem.
type normalized struct {
	problem      Problem
	full         uint32
	locks        uint32
	globalFreeze uint32
}

func (p Problem) normalize() (normalized, error) {
	used := p.Initial | p.LockMask | p.ForbiddenBits
	switch {
	case p.Goal != nil:
		used |= p.Goal.Mask | p.Goal.Value&p.Goal.Mask
	case p.TargetMask != 0:
		used |= p.TargetMask | p.Target&p.TargetMask
	default:
		used |= p.Target
	}
	for _, op := range p.Ops {
		used |= op.Mask | op.Value&op.Mask
		used |= op.RequireMask | op.RequireValue&op.RequireMask
	}
	for _, c := range p.Forbidden {
		used |= c.Mask | c.Value&c.Mask
	}
	for _, s := range p.ForbiddenStates {
		used |= s
	}
	for _, l := range p.Locks {
		if l.Bit >= MaxStateBits {
			return normalized{}, fmt.Errorf("%w: lock bit %d is out of range", ErrOutOfRange, l.Bit)
		}
		used |= l.Freezes | 1<<l.Bit
	}

	bitCount := p.Bits
	if bitCount == 0 {
		bitCount = uint(bits.Len32(used))
		if bitCount == 0 {
			bitCount = 1
		}
	}
	if bitCount > MaxStateBits {
		return normalized{}, fmt.Errorf("%w: got %d", ErrTooManyBits, bitCount)
	}

	full := uint32(1)<<bitCount - 1
	if used&^full != 0 {
		return normalized{}, fmt.Errorf("%w: width %d does not cover mask 0x%x", ErrOutOfRange, bitCount, used&^full)
	}
	if p.MaxSteps < 0 {
		return normalized{}, fmt.Errorf("%w: MaxSteps must not be negative", ErrInvalidProblem)
	}
	if p.MaxStates < 0 {
		return normalized{}, fmt.Errorf("%w: MaxStates must not be negative", ErrInvalidProblem)
	}
	for _, l := range p.Locks {
		if l.Freezes&^full != 0 {
			return normalized{}, fmt.Errorf("%w: lock %d freezes bits 0x%x", ErrOutOfRange, l.Bit, l.Freezes&^full)
		}
	}
	np := p
	np.Bits = bitCount
	np.TargetMask &= full
	np.Forbidden = append([]Condition(nil), p.Forbidden...)
	np.ForbiddenBits &= full
	np.ForbiddenStates = append([]uint32(nil), p.ForbiddenStates...)
	np.Locks = append([]LockRule(nil), p.Locks...)
	np.Ops = append([]Operation(nil), p.Ops...)
	locks := p.LockMask

	// A named lock bit is one-way even when the caller forgot to include it
	// in LockMask.
	for _, l := range np.Locks {
		locks |= uint32(1) << l.Bit
	}
	np.LockMask = locks & full

	var globalFreeze uint32
	if !p.SelfLockOnly && len(p.Locks) == 0 && np.LockMask != 0 {
		globalFreeze = full &^ np.LockMask
	}

	// Canonicalize conditions so callers can compare them and so values above
	// the register width do not accidentally affect matching.
	for i := range np.Forbidden {
		np.Forbidden[i].Mask &= full
		np.Forbidden[i].Value &= np.Forbidden[i].Mask
	}
	for i := range np.ForbiddenStates {
		np.ForbiddenStates[i] &= full
	}
	if p.Goal != nil {
		goal := *p.Goal
		goal.Mask &= full
		goal.Value &= goal.Mask
		np.Goal = &goal
	}
	for i := range np.Ops {
		np.Ops[i].Mask &= full
		np.Ops[i].Value &= np.Ops[i].Mask
		np.Ops[i].RequireMask &= full
		np.Ops[i].RequireValue &= np.Ops[i].RequireMask
	}

	return normalized{problem: np, full: full, locks: locks & full, globalFreeze: globalFreeze}, nil
}

func (n normalized) forbidden(state uint32) bool {
	p := &n.problem
	if state&p.ForbiddenBits != 0 {
		return true
	}
	for _, c := range p.Forbidden {
		if c.Match(state) {
			return true
		}
	}
	for _, s := range p.ForbiddenStates {
		if state == s {
			return true
		}
	}
	return p.ForbiddenFunc != nil && p.ForbiddenFunc(state)
}

func (n normalized) goal(state uint32) bool {
	if n.problem.Goal != nil {
		return n.problem.Goal.Match(state)
	}
	if n.problem.TargetMask != 0 {
		return state&n.problem.TargetMask == n.problem.Target&n.problem.TargetMask
	}
	return state == n.problem.Target
}

func (n normalized) goalDistance(state uint32) int {
	if n.problem.Goal != nil {
		return bits.OnesCount32((state ^ n.problem.Goal.Value) & n.problem.Goal.Mask)
	}
	if n.problem.TargetMask != 0 {
		return bits.OnesCount32((state ^ n.problem.Target) & n.problem.TargetMask)
	}
	return bits.OnesCount32((state ^ n.problem.Target) & n.full)
}

func (n normalized) partialGoal() bool {
	return n.problem.Goal != nil || n.problem.TargetMask != 0
}

// applyOp applies an operation to state. It reports false when the
// precondition fails or when a lock forbids the write.
func applyOp(state uint32, op Operation, lockMask uint32, locks []LockRule, globalFreeze uint32) (uint32, bool) {
	if state&op.RequireMask != op.RequireValue&op.RequireMask {
		return 0, false
	}
	next := (state &^ op.Mask) | (op.Value & op.Mask)
	if (state&lockMask)&^next != 0 {
		return 0, false
	}
	if globalFreeze != 0 {
		if state&lockMask != 0 && (next^state)&globalFreeze != 0 {
			return 0, false
		}
	}
	for _, l := range locks {
		if state&(uint32(1)<<l.Bit) != 0 && (next^state)&l.Freezes != 0 {
			return 0, false
		}
	}
	return next, true
}

// ApplyOperation applies op to state without requiring a Problem. lockMask
// and locks have the same meaning as in Problem. When locks is empty, the
// default policy freezes every bit outside lockMask once a lock bit is set.
func ApplyOperation(state uint32, op Operation, lockMask uint32, locks []LockRule) (uint32, bool) {
	var globalFreeze uint32
	if len(locks) == 0 && lockMask != 0 {
		globalFreeze = ^lockMask
	}
	return applyOp(state, op, lockMask, locks, globalFreeze)
}

// Next applies the operation at index op to state. The operation is validated
// against the problem's register width. A false result means the write is not
// applicable; an error means the problem or index is invalid.
func Next(p Problem, state uint32, op int) (uint32, bool, error) {
	n, err := p.normalize()
	if err != nil {
		return 0, false, err
	}
	if state&^n.full != 0 {
		return 0, false, fmt.Errorf("%w: state 0x%x", ErrOutOfRange, state)
	}
	if op < 0 || op >= len(n.problem.Ops) {
		return 0, false, fmt.Errorf("%w: operation index %d", ErrInvalidProblem, op)
	}
	next, ok := applyOp(state, n.problem.Ops[op], n.locks, n.problem.Locks, n.globalFreeze)
	return next, ok, nil
}

// Simulate applies a sequence of operation indices from Problem.Initial and
// returns every state visited, starting with the initial state. It verifies
// the goal and all safety constraints, making it suitable as a final check
// before a configuration host writes hardware registers.
func Simulate(p Problem, plan []int) ([]uint32, error) {
	n, err := p.normalize()
	if err != nil {
		return nil, err
	}
	state := n.problem.Initial
	states := []uint32{state}
	if n.forbidden(state) {
		return states, fmt.Errorf("%w: initial state 0x%x", ErrUnsafePlan, state)
	}
	for i, opIndex := range plan {
		if opIndex < 0 || opIndex >= len(n.problem.Ops) {
			return states, fmt.Errorf("%w: operation index %d at step %d", ErrInvalidProblem, opIndex, i)
		}
		next, ok := applyOp(state, n.problem.Ops[opIndex], n.locks, n.problem.Locks, n.globalFreeze)
		if !ok {
			return states, fmt.Errorf("%w: operation %d at step %d", ErrNotApplicable, opIndex, i)
		}
		if n.forbidden(next) {
			return states, fmt.Errorf("%w: state 0x%x at step %d", ErrUnsafePlan, next, i)
		}
		state = next
		states = append(states, state)
	}
	if !n.goal(state) {
		return states, fmt.Errorf("%w: final state 0x%x", ErrGoalNotReached, state)
	}
	return states, nil
}
