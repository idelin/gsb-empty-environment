package netsec

import (
	"fmt"
	"strings"
)

// VerifyError describes the first rejection found during verification.
// Path is the instruction-index trace from the method entry to the point
// of failure; PC is the instruction (or merge target) that was rejected.
type VerifyError struct {
	PC   int
	Path []int
	Msg  string
}

func (e *VerifyError) Error() string {
	steps := make([]string, len(e.Path))
	for i, pc := range e.Path {
		steps[i] = fmt.Sprintf("%d", pc)
	}
	return fmt.Sprintf("netsec: verify failed at pc %d (path %s): %s",
		e.PC, strings.Join(steps, "->"), e.Msg)
}

type typeKind int

const (
	kindUnavailable   typeKind = iota // slot holds nothing usable
	kindUninitialized                 // reference produced by new, tagged with the new pc
	kindInitialized                   // fully constructed reference of a known class
)

// verType is the abstract verification type of one stack slot or local.
type verType struct {
	kind  typeKind
	id    int    // kindUninitialized: pc of the new instruction that created it
	class string // kindUninitialized / kindInitialized: the class
}

var typeUnavailable = verType{kind: kindUnavailable}

func uninitialized(id int, class string) verType {
	return verType{kind: kindUninitialized, id: id, class: class}
}

func initialized(class string) verType {
	return verType{kind: kindInitialized, class: class}
}

func (t verType) String() string {
	switch t.kind {
	case kindUnavailable:
		return "unavailable"
	case kindUninitialized:
		return fmt.Sprintf("uninit(%s@%d)", t.class, t.id)
	default:
		return t.class
	}
}

// frame is the abstract state at one instruction: local slots + operand stack.
type frame struct {
	locals []verType
	stack  []verType
}

func newFrame(numLocals int) *frame {
	f := &frame{locals: make([]verType, numLocals)}
	for i := range f.locals {
		f.locals[i] = typeUnavailable
	}
	return f
}

func (f *frame) clone() *frame {
	g := &frame{
		locals: make([]verType, len(f.locals)),
		stack:  make([]verType, len(f.stack)),
	}
	copy(g.locals, f.locals)
	copy(g.stack, f.stack)
	return g
}

func (f *frame) hasUninitialized() bool {
	for _, t := range f.locals {
		if t.kind == kindUninitialized {
			return true
		}
	}
	for _, t := range f.stack {
		if t.kind == kindUninitialized {
			return true
		}
	}
	return false
}

func (f *frame) pop() (verType, bool) {
	if len(f.stack) == 0 {
		return typeUnavailable, false
	}
	t := f.stack[len(f.stack)-1]
	f.stack = f.stack[:len(f.stack)-1]
	return t, true
}

// edge is one verified control-flow transfer to a successor instruction.
type edge struct {
	target int
	frame  *frame
}

// Verify runs the worklist fixpoint over all normal and exception edges of
// the program. It returns nil when every reachable edge verifies, or the
// first rejection (with its path) otherwise.
func Verify(h *Hierarchy, p *Program) *VerifyError {
	if err := validateProgram(h, p); err != nil {
		return err
	}
	n := len(p.Code)
	states := make([]*frame, n)
	paths := make([][]int, n)

	states[0] = newFrame(p.NumLocals)
	paths[0] = []int{0}

	worklist := []int{0}
	inQueue := make([]bool, n)
	inQueue[0] = true

	for len(worklist) > 0 {
		pc := worklist[0]
		worklist = worklist[1:]
		inQueue[pc] = false

		edges, verr := transfer(h, p, pc, states[pc])
		if verr != nil {
			verr.Path = appendPath(paths[pc])
			return verr
		}
		for _, e := range edges {
			// A back edge may never carry an uninitialized reference.
			if e.target <= pc && e.frame.hasUninitialized() {
				return &VerifyError{
					PC:   e.target,
					Path: appendPath(paths[pc], e.target),
					Msg:  "back edge carries an uninitialized reference",
				}
			}
			merged, changed, merr := mergeFrame(h, states[e.target], e.frame)
			if merr != "" {
				return &VerifyError{
					PC:   e.target,
					Path: appendPath(paths[pc], e.target),
					Msg:  merr,
				}
			}
			if changed {
				states[e.target] = merged
				paths[e.target] = appendPath(paths[pc], e.target)
				if !inQueue[e.target] {
					inQueue[e.target] = true
					worklist = append(worklist, e.target)
				}
			}
		}
	}
	return nil
}

func appendPath(path []int, more ...int) []int {
	out := make([]int, 0, len(path)+len(more))
	out = append(out, path...)
	out = append(out, more...)
	return out
}

// mergeFrame folds an incoming edge frame into the recorded state at its
// target. Locals merge to unavailable when incompatible; stack entries must
// be compatible, with initialized classes merged to their nearest common
// ancestor. It reports whether the recorded state changed.
func mergeFrame(h *Hierarchy, cur, in *frame) (merged *frame, changed bool, errMsg string) {
	if cur == nil {
		return in.clone(), true, ""
	}
	if len(cur.stack) != len(in.stack) {
		return nil, false, fmt.Sprintf("operand stack height mismatch: %d vs %d",
			len(cur.stack), len(in.stack))
	}
	out := cur.clone()
	for i := range out.locals {
		if out.locals[i] != in.locals[i] {
			out.locals[i] = typeUnavailable
		}
	}
	for i := range out.stack {
		a, b := out.stack[i], in.stack[i]
		if a == b {
			continue
		}
		if a.kind == kindInitialized && b.kind == kindInitialized {
			lca, ok := h.CommonAncestor(a.class, b.class)
			if !ok {
				return nil, false, fmt.Sprintf("no common ancestor for %s and %s",
					a.class, b.class)
			}
			out.stack[i] = initialized(lca)
			continue
		}
		return nil, false, fmt.Sprintf("incompatible stack types %s and %s", a, b)
	}
	changed = !frameEqual(cur, out)
	return out, changed, ""
}

func frameEqual(a, b *frame) bool {
	if len(a.stack) != len(b.stack) {
		return false
	}
	for i := range a.locals {
		if a.locals[i] != b.locals[i] {
			return false
		}
	}
	for i := range a.stack {
		if a.stack[i] != b.stack[i] {
			return false
		}
	}
	return true
}

// transfer executes the instruction at pc abstractly and returns the
// successor edges, including every exception-table edge.
func transfer(h *Hierarchy, p *Program, pc int, in *frame) ([]edge, *VerifyError) {
	ins := p.Code[pc]
	fail := func(format string, args ...any) *VerifyError {
		return &VerifyError{PC: pc, Msg: fmt.Sprintf(format, args...)}
	}
	fall := func(f *frame) []edge {
		return []edge{{target: pc + 1, frame: f}}
	}
	// handlerEdges builds one edge per exception handler. The operand stack
	// is cleared and an initialized Throwable is pushed; poison is applied
	// to the locals first (used by init to kill aliases of the failed object).
	handlerEdges := func(poisonID int) []edge {
		var out []edge
		for _, hp := range p.Handlers[pc] {
			f := in.clone()
			if poisonID >= 0 {
				for i := range f.locals {
					if f.locals[i].kind == kindUninitialized && f.locals[i].id == poisonID {
						f.locals[i] = typeUnavailable
					}
				}
			}
			f.stack = []verType{initialized(ThrowableClass)}
			out = append(out, edge{target: hp, frame: f})
		}
		return out
	}

	switch ins.Op {
	case OpNew:
		f := in.clone()
		f.stack = append(f.stack, uninitialized(pc, ins.Class))
		// A failed new produces no object; the handler edges are generic.
		return append(fall(f), handlerEdges(-1)...), nil

	case OpDup:
		if len(in.stack) == 0 {
			return nil, fail("dup on empty operand stack")
		}
		f := in.clone()
		f.stack = append(f.stack, f.stack[len(f.stack)-1])
		return fall(f), nil

	case OpAstore:
		f := in.clone()
		t, ok := f.pop()
		if !ok {
			return nil, fail("astore on empty operand stack")
		}
		f.locals[ins.Slot] = t
		return fall(f), nil

	case OpAload:
		t := in.locals[ins.Slot]
		if t.kind == kindUnavailable {
			return nil, fail("aload of unavailable local slot %d", ins.Slot)
		}
		f := in.clone()
		f.stack = append(f.stack, t)
		return fall(f), nil

	case OpInit:
		f := in.clone()
		recv, ok := f.pop()
		if !ok {
			return nil, fail("init on empty operand stack")
		}
		if recv.kind != kindUninitialized || recv.class != ins.Class {
			return nil, fail("init %s requires an uninitialized %s receiver, got %s",
				ins.Class, ins.Class, recv)
		}
		// Normal edge: every alias of this new id in the frame initializes.
		normal := f.clone()
		for i := range normal.locals {
			if normal.locals[i].kind == kindUninitialized && normal.locals[i].id == recv.id {
				normal.locals[i] = initialized(recv.class)
			}
		}
		for i := range normal.stack {
			if normal.stack[i].kind == kindUninitialized && normal.stack[i].id == recv.id {
				normal.stack[i] = initialized(recv.class)
			}
		}
		// Exception edge: constructor failed, so aliases of the failed
		// object in the locals become unavailable.
		return append(fall(normal), handlerEdges(recv.id)...), nil

	case OpUse:
		f := in.clone()
		t, ok := f.pop()
		if !ok {
			return nil, fail("use on empty operand stack")
		}
		if t.kind != kindInitialized {
			return nil, fail("use %s requires an initialized reference, got %s", ins.Class, t)
		}
		if !h.IsSubclass(t.class, ins.Class) {
			return nil, fail("use %s requires %s or a subclass, got %s",
				ins.Class, ins.Class, t.class)
		}
		return fall(f), nil

	case OpJmp:
		return []edge{{target: ins.Target, frame: in.clone()}}, nil

	case OpBranch:
		return []edge{
			{target: ins.Target, frame: in.clone()},
			{target: ins.Target2, frame: in.clone()},
		}, nil

	case OpReturn:
		if len(in.stack) != 0 {
			return nil, fail("return with non-empty operand stack (%d entries)", len(in.stack))
		}
		return nil, nil

	case OpThrow:
		f := in.clone()
		t, ok := f.pop()
		if !ok {
			return nil, fail("throw on empty operand stack")
		}
		if t.kind != kindInitialized || !h.IsSubclass(t.class, ThrowableClass) {
			return nil, fail("throw requires an initialized %s, got %s", ThrowableClass, t)
		}
		return handlerEdges(-1), nil

	default:
		return nil, fail("unknown opcode %d", int(ins.Op))
	}
}

// validateProgram checks structural constraints before the fixpoint runs.
func validateProgram(h *Hierarchy, p *Program) *VerifyError {
	if len(p.Code) == 0 {
		return &VerifyError{PC: -1, Msg: "empty program"}
	}
	if p.NumLocals < 0 {
		return &VerifyError{PC: -1, Msg: "negative local count"}
	}
	n := len(p.Code)
	checkTarget := func(pc, target int) *VerifyError {
		if target < 0 || target >= n {
			return &VerifyError{PC: pc, Path: []int{pc},
				Msg: fmt.Sprintf("branch target %d out of range", target)}
		}
		return nil
	}
	checkClass := func(pc int, class string) *VerifyError {
		if !h.Has(class) {
			return &VerifyError{PC: pc, Path: []int{pc},
				Msg: fmt.Sprintf("unknown class %q", class)}
		}
		return nil
	}
	for pc, ins := range p.Code {
		switch ins.Op {
		case OpNew, OpInit, OpUse:
			if err := checkClass(pc, ins.Class); err != nil {
				return err
			}
		case OpAstore, OpAload:
			if ins.Slot < 0 || ins.Slot >= p.NumLocals {
				return &VerifyError{PC: pc, Path: []int{pc},
					Msg: fmt.Sprintf("local slot %d out of range (%d locals)", ins.Slot, p.NumLocals)}
			}
		case OpJmp:
			if err := checkTarget(pc, ins.Target); err != nil {
				return err
			}
		case OpBranch:
			if err := checkTarget(pc, ins.Target); err != nil {
				return err
			}
			if err := checkTarget(pc, ins.Target2); err != nil {
				return err
			}
		case OpDup, OpReturn, OpThrow:
		default:
			return &VerifyError{PC: pc, Path: []int{pc},
				Msg: fmt.Sprintf("unknown opcode %d", int(ins.Op))}
		}
		switch ins.Op {
		case OpNew, OpDup, OpAstore, OpAload, OpInit, OpUse:
			if pc+1 >= n {
				return &VerifyError{PC: pc, Path: []int{pc},
					Msg: "instruction falls off the end of the method"}
			}
		}
	}
	for pc, handlers := range p.Handlers {
		if pc < 0 || pc >= n {
			return &VerifyError{PC: pc, Path: []int{pc},
				Msg: "exception table entry out of range"}
		}
		for _, hp := range handlers {
			if err := checkTarget(pc, hp); err != nil {
				return err
			}
		}
	}
	return nil
}
