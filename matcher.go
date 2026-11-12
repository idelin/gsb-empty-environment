package netsec

import "unicode/utf8"

// Status is the definitive outcome of a match.
type Status uint8

const (
	// StatusNoMatch means the pattern definitely did not match.
	StatusNoMatch Status = iota
	// StatusMatch means the pattern definitely matched.
	StatusMatch
	// StatusBudgetExhausted means the caller's step budget was used before a
	// definitive answer could be produced.
	StatusBudgetExhausted
)

// MatchResult is a match decision suitable for a WAF gate. A budget exhaustion
// is fail-closed: Blocked and FailClosed are both true, even though Matched is
// false because no definitive match was found.
type MatchResult struct {
	Status     Status
	Matched    bool
	Blocked    bool
	Allowed    bool
	Exhausted  bool
	FailClosed bool
	Steps      int
	Budget     int
}

// Result is an alias for MatchResult.
type Result = MatchResult

// Matcher is an immutable compiled regular expression. It may be shared by
// concurrent goroutines; each Match call uses its own NFA state sets.
type Matcher struct {
	pattern string
	prog    *program
}

// Compile parses and compiles a pattern from the supported subset.
func Compile(pattern string) (*Matcher, error) {
	root, err := parsePattern(pattern)
	if err != nil {
		return nil, err
	}
	return &Matcher{pattern: pattern, prog: buildProgram(root)}, nil
}

// CompilePattern is an alias for Compile.
func CompilePattern(pattern string) (*Matcher, error) {
	return Compile(pattern)
}

// NewMatcher is an alias for Compile.
func NewMatcher(pattern string) (*Matcher, error) {
	return Compile(pattern)
}

// MustCompile is like Compile but panics on an invalid pattern.
func MustCompile(pattern string) *Matcher {
	m, err := Compile(pattern)
	if err != nil {
		panic(err)
	}
	return m
}

// Pattern returns the source pattern.
func (m *Matcher) Pattern() string {
	return m.pattern
}

// NumStates returns the number of Thompson NFA states.
func (m *Matcher) NumStates() int {
	return len(m.prog.states)
}

// Match runs the pattern against input using at most budget NFA state
// expansions. Every closure visit and character-state check consumes one step.
// A negative budget is treated as zero. If the budget is exhausted, the result
// is fail-closed and Steps equals Budget.
func (m *Matcher) Match(input string, budget int) MatchResult {
	if budget < 0 {
		budget = 0
	}
	stateCount := len(m.prog.states)
	e := executor{
		input:   input,
		prog:    m.prog,
		budget:  budget,
		current: newStateSet(stateCount),
		next:    newStateSet(stateCount),
	}
	return e.run()
}

// MatchBytes is Match for byte input.
func (m *Matcher) MatchBytes(input []byte, budget int) MatchResult {
	return m.Match(string(input), budget)
}

// Match compiles pattern and matches it against input.
func Match(pattern, input string, budget int) (MatchResult, error) {
	m, err := Compile(pattern)
	if err != nil {
		return MatchResult{}, err
	}
	return m.Match(input, budget), nil
}

type stateSet struct {
	dense      []int
	generation []uint32
	gen        uint32
}

func newStateSet(size int) stateSet {
	return stateSet{dense: make([]int, 0, size), generation: make([]uint32, size), gen: 1}
}

func (s *stateSet) reset() {
	s.dense = s.dense[:0]
	s.gen++
	if s.gen == 0 {
		clear(s.generation)
		s.gen = 1
	}
}

func (s *stateSet) add(idx int) {
	if s.generation[idx] == s.gen {
		return
	}
	s.generation[idx] = s.gen
	s.dense = append(s.dense, idx)
}

func (s *stateSet) contains(idx int) bool {
	return s.generation[idx] == s.gen
}

type executor struct {
	input   string
	prog    *program
	budget  int
	steps   int
	current stateSet
	next    stateSet
	matched bool
}

func (e *executor) run() MatchResult {
	e.current.add(e.prog.start)
	pos := 0

	if !e.closure(pos) {
		return e.exhaustedResult()
	}
	if e.matched {
		return e.matchResult()
	}

	for pos < len(e.input) {
		r, size := utf8.DecodeRuneInString(e.input[pos:])
		e.next.reset()

		for i := 0; i < len(e.current.dense); i++ {
			state := &e.prog.states[e.current.dense[i]]
			if state.op != nfaChar {
				continue
			}
			if !e.charge() {
				return e.exhaustedResult()
			}
			if state.class.matches(r) {
				e.next.add(state.out1)
			}
		}

		pos += size
		e.next.add(e.prog.start)
		e.current, e.next = e.next, e.current

		if !e.closure(pos) {
			return e.exhaustedResult()
		}
		if e.matched {
			return e.matchResult()
		}
	}

	return MatchResult{
		Status:  StatusNoMatch,
		Allowed: true,
		Steps:   e.steps,
		Budget:  e.budget,
	}
}

// closure follows epsilon, split, and zero-width anchor edges at pos. It
// reports false only when the step budget is exhausted.
func (e *executor) closure(pos int) bool {
	e.matched = false
	endOfInput := pos == len(e.input)

	for i := 0; i < len(e.current.dense); i++ {
		if !e.charge() {
			return false
		}

		idx := e.current.dense[i]
		state := &e.prog.states[idx]
		switch state.op {
		case nfaMatch:
			e.matched = true
			return true
		case nfaEpsilon:
			e.current.add(state.out1)
		case nfaSplit:
			e.current.add(state.out1)
			e.current.add(state.out2)
		case nfaAssertStart:
			if pos == 0 {
				e.current.add(state.out1)
			}
		case nfaAssertEnd:
			if endOfInput {
				e.current.add(state.out1)
			}
		case nfaChar:
			// Character transitions are evaluated in run.
		}
	}
	return true
}

func (e *executor) charge() bool {
	if e.steps >= e.budget {
		return false
	}
	e.steps++
	return true
}

func (e *executor) matchResult() MatchResult {
	return MatchResult{
		Status:  StatusMatch,
		Matched: true,
		Blocked: true,
		Steps:   e.steps,
		Budget:  e.budget,
	}
}

func (e *executor) exhaustedResult() MatchResult {
	return MatchResult{
		Status:     StatusBudgetExhausted,
		Blocked:    true,
		Exhausted:  true,
		FailClosed: true,
		Steps:      e.steps,
		Budget:     e.budget,
	}
}
