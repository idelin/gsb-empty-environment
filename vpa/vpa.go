// Package vpa implements visibly pushdown automata (VPA): recognition,
// shortest-distinguishing-word search between two VPAs, and independent
// brute-force verification of the search results.
//
// The input alphabet is explicitly partitioned into call, return and
// internal symbols. Calls push exactly one stack symbol, returns pop one,
// internals never touch the stack. A return on an empty stack does not
// crash: it is handled by a dedicated empty-stack return rule.
//
// Depth limits are a range bound of the analysis, not part of the
// automaton: a word that would grow the stack beyond the configured depth
// is "out of range", which is deliberately different from "rejected".
package vpa

import (
	"fmt"
	"sort"
	"strings"
)

// Kind classifies an input symbol as required by a visibly pushdown alphabet.
type Kind int

const (
	Internal Kind = iota // never touches the stack
	Call                 // pushes exactly one symbol
	Return               // pops one symbol; empty stack uses a special rule
)

func (k Kind) String() string {
	switch k {
	case Call:
		return "call"
	case Return:
		return "return"
	default:
		return "internal"
	}
}

type callKey struct {
	state int
	sym   string
}

type callTarget struct {
	push string
	next int
}

type returnKey struct {
	state int
	sym   string
	top   string
}

type stateSym struct {
	state int
	sym   string
}

// VPA is a deterministic visibly pushdown automaton.
//
// Transition semantics:
//   - call:     (q, c) -> (q', push g)          via CallTrans
//   - return:   (q, r, top g) -> q'             via ReturnTrans (stack non-empty)
//     (q, r) on empty stack -> q'     via ReturnEmpty (special rule)
//   - internal: (q, i) -> q'                    via InternalTrans
//
// A missing transition rejects the word (implicit trap state, reported as
// state -1). The automaton itself never blocks on stack depth; depth limits
// are imposed by the analysis and mean "out of range", not "rejected".
type VPA struct {
	Name          string
	NumStates     int
	Initial       int
	Finals        map[int]bool
	Alphabet      map[string]Kind
	CallTrans     map[callKey]callTarget
	ReturnTrans   map[returnKey]int
	ReturnEmpty   map[stateSym]int
	InternalTrans map[stateSym]int
}

// New creates an empty VPA with the given name, state count and initial state.
func New(name string, numStates, initial int) *VPA {
	return &VPA{
		Name:          name,
		NumStates:     numStates,
		Initial:       initial,
		Finals:        map[int]bool{},
		Alphabet:      map[string]Kind{},
		CallTrans:     map[callKey]callTarget{},
		ReturnTrans:   map[returnKey]int{},
		ReturnEmpty:   map[stateSym]int{},
		InternalTrans: map[stateSym]int{},
	}
}

func (v *VPA) AddFinal(q int) *VPA               { v.Finals[q] = true; return v }
func (v *VPA) AddSymbol(sym string, k Kind) *VPA { v.Alphabet[sym] = k; return v }

func (v *VPA) AddCall(q int, sym, push string, next int) *VPA {
	v.CallTrans[callKey{q, sym}] = callTarget{push, next}
	return v
}

func (v *VPA) AddReturn(q int, sym, top string, next int) *VPA {
	v.ReturnTrans[returnKey{q, sym, top}] = next
	return v
}

// AddReturnEmpty installs the special rule for a return symbol read while
// the stack is empty: the state changes, nothing is popped.
func (v *VPA) AddReturnEmpty(q int, sym string, next int) *VPA {
	v.ReturnEmpty[stateSym{q, sym}] = next
	return v
}

func (v *VPA) AddInternal(q int, sym string, next int) *VPA {
	v.InternalTrans[stateSym{q, sym}] = next
	return v
}

// Symbols returns the alphabet in sorted order for deterministic enumeration.
func (v *VPA) Symbols() []string {
	out := make([]string, 0, len(v.Alphabet))
	for s := range v.Alphabet {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// SameAlphabet reports whether both automata share the same visibly
// pushdown alphabet (same symbols, same kinds).
func SameAlphabet(a, b *VPA) bool {
	if len(a.Alphabet) != len(b.Alphabet) {
		return false
	}
	for s, k := range a.Alphabet {
		if kb, ok := b.Alphabet[s]; !ok || kb != k {
			return false
		}
	}
	return true
}

// Step records one transition of a simulation.
type Step struct {
	Pos        int      // 1-based position in the word
	Sym        string   // input symbol read
	Kind       Kind     // its visibly-pushdown kind
	From, To   int      // states before/after; -1 is the implicit trap
	StackAfter []string // stack contents after the step, bottom first
	Note       string   // human-readable detail (push/pop, special rule, ...)
}

// Outcome of a run.
type Outcome int

const (
	Accept Outcome = iota
	Reject
	// OutOfRange means the word needed a stack deeper than the configured
	// limit. It is NOT a rejection: the word lies outside the analysed range.
	OutOfRange
)

func (o Outcome) String() string {
	switch o {
	case Accept:
		return "accept"
	case Reject:
		return "reject"
	default:
		return "out-of-range"
	}
}

// Run is the result of simulating one word.
type Run struct {
	Outcome    Outcome
	FinalState int    // state where the run stopped; -1 is the trap
	Steps      []Step // one entry per consumed symbol (plus the limit hit)
	ExceededAt int    // 1-based position where the depth limit was hit, 0 if never
}

// Run simulates the automaton on word. If consuming a call would grow the
// stack beyond maxDepth, the run stops and reports OutOfRange: the word is
// outside the analysed range, which is deliberately different from Reject.
func (v *VPA) Run(word []string, maxDepth int) Run {
	q := v.Initial
	stack := []string{}
	run := Run{FinalState: q}

	reject := func(step Step) Run {
		step.To = -1
		step.StackAfter = cloneStack(stack)
		run.Steps = append(run.Steps, step)
		run.Outcome = Reject
		run.FinalState = -1
		return run
	}

	for i, sym := range word {
		kind, ok := v.Alphabet[sym]
		step := Step{Pos: i + 1, Sym: sym, Kind: kind, From: q}
		if !ok {
			step.Note = "symbol not in alphabet: reject"
			return reject(step)
		}
		switch kind {
		case Call:
			if len(stack)+1 > maxDepth {
				step.To = q
				step.StackAfter = cloneStack(stack)
				step.Note = fmt.Sprintf("call would reach depth %d > limit %d: out of range", len(stack)+1, maxDepth)
				run.Steps = append(run.Steps, step)
				run.Outcome = OutOfRange
				run.ExceededAt = i + 1
				run.FinalState = q
				return run
			}
			t, ok := v.CallTrans[callKey{q, sym}]
			if !ok {
				step.Note = "no call transition: reject"
				return reject(step)
			}
			stack = append(stack, t.push)
			q = t.next
			step.To = q
			step.Note = fmt.Sprintf("push %s", t.push)
		case Return:
			if len(stack) == 0 {
				next, ok := v.ReturnEmpty[stateSym{q, sym}]
				if !ok {
					step.Note = "empty stack and no empty-return rule: reject"
					return reject(step)
				}
				q = next
				step.To = q
				step.Note = "empty-stack return rule (no pop)"
			} else {
				top := stack[len(stack)-1]
				next, ok := v.ReturnTrans[returnKey{q, sym, top}]
				if !ok {
					step.Note = fmt.Sprintf("no return transition for top %s: reject", top)
					return reject(step)
				}
				stack = stack[:len(stack)-1]
				q = next
				step.To = q
				step.Note = fmt.Sprintf("pop %s", top)
			}
		default: // Internal
			next, ok := v.InternalTrans[stateSym{q, sym}]
			if !ok {
				step.Note = "no internal transition: reject"
				return reject(step)
			}
			q = next
			step.To = q
		}
		step.StackAfter = cloneStack(stack)
		run.Steps = append(run.Steps, step)
	}
	run.FinalState = q
	if v.Finals[q] {
		run.Outcome = Accept
	} else {
		run.Outcome = Reject
	}
	return run
}

func cloneStack(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	return out
}

// FormatStack renders a stack bottom-first, e.g. "[S S]"; empty is "[]".
func FormatStack(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	return "[" + strings.Join(s, " ") + "]"
}
