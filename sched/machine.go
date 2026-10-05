// Package sched implements a cooperative scheduler enumerator.
//
// Model: up to MaxThreads coroutines (threads) run deterministic programs.
// A thread yields to the scheduler ONLY at explicit scheduling points:
// Load, Store and Wait instructions. All other instructions (Assign, Jmp,
// JmpIf, Notify, Assert) execute atomically as part of the run-up to the
// next scheduling point. The enumerator explores every distinct schedule
// up to a bounded number of scheduling steps, merging branches that reach
// identical states.
//
// The merge key covers shared memory AND the full per-thread state:
// program counter, registers, and crucially the wait state (which
// condition variable the thread is blocked on, and whether a wakeup is
// pending). Two states with identical memory but different wait states
// have different runnability and are never merged.
package sched

import (
	"fmt"
	"strings"
)

// MaxThreads is the maximum number of coroutines supported.
const MaxThreads = 4

// NotWaiting marks a thread that is not blocked on any condition variable.
const NotWaiting = -1

// localStepCap guards against threads that diverge without ever reaching
// a scheduling point (an infinite purely-local loop).
const localStepCap = 1000

// ---------------------------------------------------------------------------
// Expressions (evaluated against the thread's registers)
// ---------------------------------------------------------------------------

type Expr interface {
	Eval(regs []int) int
	String() string
}

type Const int

func (c Const) Eval([]int) int { return int(c) }
func (c Const) String() string { return fmt.Sprintf("%d", int(c)) }

type Reg int

func (r Reg) Eval(regs []int) int { return regs[int(r)] }
func (r Reg) String() string      { return fmt.Sprintf("r%d", int(r)) }

type BinOp int

const (
	Add BinOp = iota
	Sub
	Mul
	Eq
	Ne
	Lt
	Le
	Gt
	Ge
)

type Bin struct {
	Op   BinOp
	L, R Expr
}

func (b Bin) Eval(regs []int) int {
	l, r := b.L.Eval(regs), b.R.Eval(regs)
	switch b.Op {
	case Add:
		return l + r
	case Sub:
		return l - r
	case Mul:
		return l * r
	case Eq:
		if l == r {
			return 1
		}
	case Ne:
		if l != r {
			return 1
		}
	case Lt:
		if l < r {
			return 1
		}
	case Le:
		if l <= r {
			return 1
		}
	case Gt:
		if l > r {
			return 1
		}
	case Ge:
		if l >= r {
			return 1
		}
	}
	return 0
}

func (b Bin) String() string {
	op := map[BinOp]string{
		Add: "+", Sub: "-", Mul: "*",
		Eq: "==", Ne: "!=", Lt: "<", Le: "<=", Gt: ">", Ge: ">=",
	}[b.Op]
	return fmt.Sprintf("(%s %s %s)", b.L, op, b.R)
}

// ---------------------------------------------------------------------------
// Instructions
// ---------------------------------------------------------------------------

type InstrKind int

const (
	Load   InstrKind = iota // scheduling point: regs[Dst] = mem[Addr]
	Store                   // scheduling point: mem[Addr] = Val
	Wait                    // scheduling point: block on Cv until notified
	Notify                  // local: wake every thread waiting on Cv
	Assert                  // local: violation if Val evaluates to 0
	Assign                  // local: regs[Dst] = Val
	JmpIf                   // local: if Cond != 0 jump to Target
	Jmp                     // local: jump to Target
)

type Instr struct {
	Kind   InstrKind
	Dst    int  // Load, Assign
	Addr   int  // Load, Store
	Val    Expr // Store, Assign, Assert
	Cv     int  // Wait, Notify
	Cond   Expr // JmpIf
	Target int  // JmpIf, Jmp
	Note   string
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

type ThreadState struct {
	PC      int
	Regs    []int
	Waiting int  // condition variable id, or NotWaiting
	Woken   bool // a Notify targeted us while we were waiting
}

type State struct {
	Mem     []int
	Threads []ThreadState
}

func (s State) Clone() State {
	out := State{Mem: append([]int(nil), s.Mem...), Threads: make([]ThreadState, len(s.Threads))}
	for i, th := range s.Threads {
		out.Threads[i] = ThreadState{
			PC:      th.PC,
			Regs:    append([]int(nil), th.Regs...),
			Waiting: th.Waiting,
			Woken:   th.Woken,
		}
	}
	return out
}

// Key encodes everything that can influence future behaviour: shared
// memory plus, per thread, PC, registers, wait target and pending wakeup.
// Wait state is part of the key precisely so that branches which differ
// only in runnability are never merged.
func (s State) Key() string {
	var b strings.Builder
	b.WriteString("m:")
	for _, v := range s.Mem {
		fmt.Fprintf(&b, "%d,", v)
	}
	for _, th := range s.Threads {
		fmt.Fprintf(&b, "|pc:%d w:%d k:%t r:", th.PC, th.Waiting, th.Woken)
		for _, v := range th.Regs {
			fmt.Fprintf(&b, "%d,", v)
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Machine
// ---------------------------------------------------------------------------

type Machine struct {
	Progs   [][]Instr // one instruction list per thread
	MemSize int
	Regs    int // registers per thread
}

func (m *Machine) Validate() error {
	if len(m.Progs) == 0 {
		return fmt.Errorf("need at least one thread")
	}
	if len(m.Progs) > MaxThreads {
		return fmt.Errorf("too many threads: %d (max %d)", len(m.Progs), MaxThreads)
	}
	if m.MemSize <= 0 || m.Regs <= 0 {
		return fmt.Errorf("MemSize and Regs must be positive")
	}
	for tid, prog := range m.Progs {
		for pc, in := range prog {
			switch in.Kind {
			case Load, Store:
				if in.Addr < 0 || in.Addr >= m.MemSize {
					return fmt.Errorf("thread %d pc %d: address %d out of range", tid, pc, in.Addr)
				}
			case Jmp, JmpIf:
				if in.Target < 0 || in.Target > len(prog) {
					return fmt.Errorf("thread %d pc %d: jump target %d out of range", tid, pc, in.Target)
				}
			}
		}
	}
	return nil
}

func (m *Machine) Initial() State {
	st := State{Mem: make([]int, m.MemSize), Threads: make([]ThreadState, len(m.Progs))}
	for i := range st.Threads {
		st.Threads[i] = ThreadState{Waiting: NotWaiting, Regs: make([]int, m.Regs)}
	}
	return st
}

// Done reports whether the thread has run off the end of its program.
func (m *Machine) Done(st *State, tid int) bool {
	return st.Threads[tid].PC >= len(m.Progs[tid])
}

// Runnable reports whether the scheduler may pick the thread: it must be
// neither finished nor blocked in Wait.
func (m *Machine) Runnable(st *State, tid int) bool {
	th := st.Threads[tid]
	return th.Waiting == NotWaiting && th.PC < len(m.Progs[tid])
}

// Step runs thread tid from its current PC until it has consumed exactly
// one scheduling point (Load/Store/Wait) or reached the end of its
// program. It returns the resulting state and a human-readable event
// describing everything the step did. If an Assert fails along the way,
// a Violation is returned instead.
func (m *Machine) Step(st State, tid int) (State, Event, *Violation) {
	st = st.Clone()
	th := &st.Threads[tid]
	prog := m.Progs[tid]
	var desc []string

	for local := 0; ; local++ {
		if local > localStepCap {
			return st, Event{Thread: tid, Text: "diverges (local loop)"}, &Violation{
				Kind:    ViolationDivergence,
				Message: fmt.Sprintf("thread %d: %d local instructions without a scheduling point", tid, localStepCap),
			}
		}
		if th.PC >= len(prog) {
			return st, Event{Thread: tid, Text: strings.Join(desc, "; ")}, nil
		}
		in := prog[th.PC]
		switch in.Kind {
		case Assign:
			th.Regs[in.Dst] = in.Val.Eval(th.Regs)
			desc = append(desc, fmt.Sprintf("r%d := %s", in.Dst, in.Val))
			th.PC++
		case Jmp:
			th.PC = in.Target
		case JmpIf:
			if in.Cond.Eval(th.Regs) != 0 {
				th.PC = in.Target
			} else {
				th.PC++
			}
		case Assert:
			if in.Val.Eval(th.Regs) == 0 {
				note := in.Note
				if note == "" {
					note = in.Val.String()
				}
				desc = append(desc, "ASSERT FAIL: "+note)
				return st, Event{Thread: tid, Text: strings.Join(desc, "; ")}, &Violation{
					Kind:    ViolationAssert,
					Message: fmt.Sprintf("thread %d: assertion failed: %s", tid, note),
				}
			}
			th.PC++
		case Notify:
			woken := 0
			for i := range st.Threads {
				if st.Threads[i].Waiting == in.Cv {
					st.Threads[i].Waiting = NotWaiting
					st.Threads[i].Woken = true
					woken++
				}
			}
			desc = append(desc, fmt.Sprintf("notify cv%d (woke %d)", in.Cv, woken))
			th.PC++
		case Load:
			th.Regs[in.Dst] = st.Mem[in.Addr]
			desc = append(desc, fmt.Sprintf("r%d := load mem[%d] (= %d)", in.Dst, in.Addr, th.Regs[in.Dst]))
			th.PC++
			return st, Event{Thread: tid, Text: strings.Join(desc, "; ")}, nil
		case Store:
			v := in.Val.Eval(th.Regs)
			st.Mem[in.Addr] = v
			desc = append(desc, fmt.Sprintf("store mem[%d] := %d", in.Addr, v))
			th.PC++
			return st, Event{Thread: tid, Text: strings.Join(desc, "; ")}, nil
		case Wait:
			if th.Woken {
				th.Woken = false
				th.PC++
				desc = append(desc, fmt.Sprintf("wait cv%d (wakeup pending, continues)", in.Cv))
			} else {
				th.Waiting = in.Cv
				desc = append(desc, fmt.Sprintf("wait cv%d (blocks)", in.Cv))
			}
			return st, Event{Thread: tid, Text: strings.Join(desc, "; ")}, nil
		}
	}
}

// Replay re-executes a trace of scheduling choices from the initial
// state, returning the violation the trace produces (if any). It is used
// to confirm that a reported counterexample is genuine.
func (m *Machine) Replay(trace []Event) (*Violation, error) {
	st := m.Initial()
	for i, ev := range trace {
		if ev.Thread < 0 || ev.Thread >= len(m.Progs) {
			return nil, fmt.Errorf("event %d: bad thread id %d", i, ev.Thread)
		}
		if !m.Runnable(&st, ev.Thread) {
			return nil, fmt.Errorf("event %d: thread %d is not runnable", i, ev.Thread)
		}
		var v *Violation
		st, _, v = m.Step(st, ev.Thread)
		if v != nil {
			if i != len(trace)-1 {
				return nil, fmt.Errorf("event %d: violation before end of trace", i)
			}
			return v, nil
		}
	}
	// A deadlock trace ends in a state with no runnable thread.
	for tid := range m.Progs {
		if m.Runnable(&st, tid) {
			return nil, nil
		}
	}
	for tid := range m.Progs {
		if !m.Done(&st, tid) {
			return &Violation{Kind: ViolationDeadlock, Message: "deadlock: every live thread is blocked"}, nil
		}
	}
	return nil, nil
}
