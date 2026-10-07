// Package vpa implements recognition and bounded difference search for
// deterministic visibly pushdown automata (VPA), a classic model for
// protocol parsers. Input symbols are explicitly classified as calls,
// returns, or internals; returns on an empty stack are handled by a
// dedicated rule. During bounded analysis, exceeding the stack-depth
// bound means "out of range", never a rejection.
package vpa

import (
	"fmt"
	"strings"
)

// Kind classifies an input symbol.
type Kind int

const (
	Internal Kind = iota
	Call
	Return
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

// CallTrans is a call transition: push Push, move to Next.
type CallTrans struct {
	Next int
	Push int
}

// Automaton is a deterministic visibly pushdown automaton with a
// dedicated empty-stack return rule. All transition maps must be total
// over their domains (checked by Validate).
type Automaton struct {
	Name       string
	NumStates  int
	Initial    int
	Finals     map[int]bool
	Kinds      []Kind   // Kinds[s] classifies input symbol s
	SymNames   []string // display name per input symbol
	StackNames []string // display name per stack symbol
	NumStack   int

	Calls     map[[2]int]CallTrans // (state, symbol)        -> push & next
	Returns   map[[3]int]int       // (state, symbol, top)   -> next
	RetEmpty  map[[2]int]int       // (state, symbol)        -> next, empty-stack rule
	Internals map[[2]int]int       // (state, symbol)        -> next
}

// New creates an empty automaton; add transitions with OnCall/OnReturn/
// OnReturnEmpty/OnInternal, then check it with Validate.
func New(name string, numStates, initial int, finals []int, kinds []Kind, symNames, stackNames []string) *Automaton {
	a := &Automaton{
		Name: name, NumStates: numStates, Initial: initial,
		Finals: map[int]bool{}, Kinds: kinds, SymNames: symNames, StackNames: stackNames,
		NumStack:  len(stackNames),
		Calls:     map[[2]int]CallTrans{},
		Returns:   map[[3]int]int{},
		RetEmpty:  map[[2]int]int{},
		Internals: map[[2]int]int{},
	}
	for _, f := range finals {
		a.Finals[f] = true
	}
	return a
}

func (a *Automaton) OnCall(q, sym, next, push int)  { a.Calls[[2]int{q, sym}] = CallTrans{next, push} }
func (a *Automaton) OnReturn(q, sym, top, next int) { a.Returns[[3]int{q, sym, top}] = next }
func (a *Automaton) OnReturnEmpty(q, sym, next int) { a.RetEmpty[[2]int{q, sym}] = next }
func (a *Automaton) OnInternal(q, sym, next int)    { a.Internals[[2]int{q, sym}] = next }

// Validate checks well-formedness and completeness of all transitions.
func (a *Automaton) Validate() error {
	if a.NumStates <= 0 {
		return fmt.Errorf("%s: no states", a.Name)
	}
	if a.Initial < 0 || a.Initial >= a.NumStates {
		return fmt.Errorf("%s: initial state out of range", a.Name)
	}
	badState := func(q int) bool { return q < 0 || q >= a.NumStates }
	for s, k := range a.Kinds {
		switch k {
		case Call:
			for q := 0; q < a.NumStates; q++ {
				t, ok := a.Calls[[2]int{q, s}]
				if !ok {
					return fmt.Errorf("%s: missing call transition (q%d, %s)", a.Name, q, a.SymName(s))
				}
				if badState(t.Next) || t.Push < 0 || t.Push >= a.NumStack {
					return fmt.Errorf("%s: bad call transition (q%d, %s)", a.Name, q, a.SymName(s))
				}
			}
		case Return:
			for q := 0; q < a.NumStates; q++ {
				n, ok := a.RetEmpty[[2]int{q, s}]
				if !ok {
					return fmt.Errorf("%s: missing empty-stack return rule (q%d, %s)", a.Name, q, a.SymName(s))
				}
				if badState(n) {
					return fmt.Errorf("%s: bad empty-stack return rule (q%d, %s)", a.Name, q, a.SymName(s))
				}
				for top := 0; top < a.NumStack; top++ {
					n, ok := a.Returns[[3]int{q, s, top}]
					if !ok {
						return fmt.Errorf("%s: missing return transition (q%d, %s, top=%s)", a.Name, q, a.SymName(s), a.StackName(top))
					}
					if badState(n) {
						return fmt.Errorf("%s: bad return transition (q%d, %s, top=%s)", a.Name, q, a.SymName(s), a.StackName(top))
					}
				}
			}
		case Internal:
			for q := 0; q < a.NumStates; q++ {
				n, ok := a.Internals[[2]int{q, s}]
				if !ok {
					return fmt.Errorf("%s: missing internal transition (q%d, %s)", a.Name, q, a.SymName(s))
				}
				if badState(n) {
					return fmt.Errorf("%s: bad internal transition (q%d, %s)", a.Name, q, a.SymName(s))
				}
			}
		}
	}
	return nil
}

// SymName renders an input symbol.
func (a *Automaton) SymName(s int) string {
	if s >= 0 && s < len(a.SymNames) && a.SymNames[s] != "" {
		return a.SymNames[s]
	}
	return fmt.Sprintf("a%d", s)
}

// StackName renders a stack symbol.
func (a *Automaton) StackName(s int) string {
	if s >= 0 && s < len(a.StackNames) && a.StackNames[s] != "" {
		return a.StackNames[s]
	}
	return fmt.Sprintf("g%d", s)
}

// FormatStack renders a stack, bottom on the left, top on the right.
func (a *Automaton) FormatStack(st []int) string {
	if len(st) == 0 {
		return "[]"
	}
	parts := make([]string, len(st))
	for i, x := range st {
		parts[i] = a.StackName(x)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// WordString renders a word with this automaton's symbol names.
func (a *Automaton) WordString(w []int) string {
	if len(w) == 0 {
		return "(empty word)"
	}
	parts := make([]string, len(w))
	for i, s := range w {
		parts[i] = a.SymName(s)
	}
	return strings.Join(parts, " ")
}

// Step is one observed move of the automaton.
type Step struct {
	Pos         int
	Sym         int
	Kind        Kind
	StateBefore int
	StateAfter  int
	StackBefore []int
	StackAfter  []int
	Note        string
}

// step applies one symbol. outOfRange reports that the move would push
// beyond maxDepth: the word leaves the bounded analysis range, which is
// not a rejection.
func (a *Automaton) step(state int, stack []int, sym int, maxDepth int) (next int, nstack []int, note string, outOfRange bool) {
	switch a.Kinds[sym] {
	case Call:
		if len(stack)+1 > maxDepth {
			return state, stack, "", true
		}
		t := a.Calls[[2]int{state, sym}]
		ns := append(append([]int{}, stack...), t.Push)
		return t.Next, ns, fmt.Sprintf("call: push %s", a.StackName(t.Push)), false
	case Return:
		if len(stack) == 0 {
			n := a.RetEmpty[[2]int{state, sym}]
			return n, stack, "return on empty stack: dedicated empty-stack rule", false
		}
		top := stack[len(stack)-1]
		n := a.Returns[[3]int{state, sym, top}]
		return n, stack[:len(stack)-1], fmt.Sprintf("return: pop %s", a.StackName(top)), false
	default:
		n := a.Internals[[2]int{state, sym}]
		return n, stack, "internal: stack untouched", false
	}
}

// RunResult is the outcome of running one word.
type RunResult struct {
	Accepted     bool
	FinalState   int
	FinalStack   []int
	Steps        []Step
	OutOfRange   bool // depth bound exceeded: out of range, NOT a rejection
	OutOfRangeAt int  // word position where the run left the range
}

// Run simulates word step by step, recording every stack configuration.
// If a call would exceed maxDepth the run stops and reports OutOfRange.
func (a *Automaton) Run(word []int, maxDepth int) RunResult {
	state := a.Initial
	stack := []int{}
	res := RunResult{}
	for i, sym := range word {
		before := append([]int{}, stack...)
		next, ns, note, oor := a.step(state, stack, sym, maxDepth)
		if oor {
			res.OutOfRange = true
			res.OutOfRangeAt = i
			res.FinalState = state
			res.FinalStack = before
			return res
		}
		res.Steps = append(res.Steps, Step{
			Pos: i, Sym: sym, Kind: a.Kinds[sym],
			StateBefore: state, StateAfter: next,
			StackBefore: before, StackAfter: append([]int{}, ns...),
			Note: note,
		})
		state, stack = next, ns
	}
	res.FinalState = state
	res.FinalStack = append([]int{}, stack...)
	res.Accepted = a.Finals[state]
	return res
}

// Simulate is an independent, minimal re-implementation of the VPA
// semantics. It exists so results of Run and of the product-automaton
// search can be cross-checked word by word by code that shares no logic
// with them. inRange is false iff the word exceeds maxDepth.
func Simulate(a *Automaton, word []int, maxDepth int) (accept, inRange bool) {
	q := a.Initial
	st := make([]int, 0, maxDepth)
	for _, s := range word {
		switch a.Kinds[s] {
		case Call:
			if len(st) == maxDepth {
				return false, false
			}
			t := a.Calls[[2]int{q, s}]
			st = append(st, t.Push)
			q = t.Next
		case Return:
			if len(st) == 0 {
				q = a.RetEmpty[[2]int{q, s}]
			} else {
				q = a.Returns[[3]int{q, s, st[len(st)-1]}]
				st = st[:len(st)-1]
			}
		default:
			q = a.Internals[[2]int{q, s}]
		}
	}
	return a.Finals[q], true
}
