package migrationadmission

import (
	"errors"
	"fmt"
	"sort"
)

// MaxNameLength is the inclusive upper bound on a raw name length.
const MaxNameLength = 64

// MaxLateness is the largest admissible number of canonical symbols that may
// remain unmatched on either side at any comparable prefix.
const MaxLateness = 4

// ReservedEndMarker is never emitted by a rule and is used only internally as
// the end-of-input marker.
const ReservedEndMarker Symbol = 0

// Symbol is a canonical output symbol. Zero is reserved.
type Symbol uint16

// Grammar is a deterministic finite automaton over bytes. Accepting the empty
// word is allowed when Start is an accepting state.
type Grammar struct {
	Name        string
	Start       int
	Accepts     []int
	Transitions []Rule
}

// Rule is one DFA transition.
type Rule struct {
	From int
	On   byte
	To   int
}

// TranscriptionTable is the deterministic per-character transcription. Every
// emitted slice has length 0, 1 or 2; the tail has length 0 or 1.
type TranscriptionTable struct {
	Name string
	// Rules maps an input byte to the canonical symbols it emits.
	Rules []TranscriptionRule
	// Tail is emitted exactly once when the end of a legal name is reached.
	Tail []Symbol
}

// TranscriptionRule binds one input byte to 0-2 canonical symbols.
type TranscriptionRule struct {
	On   byte
	Emit []Symbol
}

// Policy is one version of a tenant's normalization policy.
type Policy struct {
	Version       string
	Grammar       Grammar
	Transcription TranscriptionTable
}

type compiledGrammar struct {
	startState int
	accept     map[int]bool
	delta      map[int]map[byte]int
}

type compiledTable struct {
	emit map[byte][]Symbol
	tail []Symbol
}

type compiledPolicy struct {
	version string
	g       compiledGrammar
	t       compiledTable
}

func (p *Policy) compile() (*compiledPolicy, error) {
	cg, err := p.Grammar.compile()
	if err != nil {
		return nil, err
	}
	ct, err := p.Transcription.compile(cg)
	if err != nil {
		return nil, err
	}
	return &compiledPolicy{version: p.Version, g: *cg, t: *ct}, nil
}

func (g Grammar) compile() (*compiledGrammar, error) {
	if g.Name == "" {
		return nil, errors.New("migrationadmission: grammar has no name")
	}
	if g.Start < 0 {
		return nil, errors.New("migrationadmission: negative grammar start state")
	}
	states := map[int]struct{}{g.Start: {}}
	accept := make(map[int]bool, len(g.Accepts))
	for _, state := range g.Accepts {
		if state < 0 {
			return nil, fmt.Errorf("migrationadmission: negative accepting state %d", state)
		}
		if accept[state] {
			return nil, fmt.Errorf("migrationadmission: duplicate accepting state %d", state)
		}
		accept[state] = true
		states[state] = struct{}{}
	}
	delta := map[int]map[byte]int{}
	seen := map[[2]interface{}]bool{}
	for _, rule := range g.Transitions {
		if rule.From < 0 || rule.To < 0 {
			return nil, errors.New("migrationadmission: negative grammar state in transition")
		}
		key := [2]interface{}{rule.From, rule.On}
		if seen[key] {
			return nil, fmt.Errorf("migrationadmission: nondeterministic transition on state %d byte %d", rule.From, rule.On)
		}
		seen[key] = true
		next := delta[rule.From]
		if next == nil {
			next = map[byte]int{}
			delta[rule.From] = next
		}
		next[rule.On] = rule.To
		states[rule.From] = struct{}{}
		states[rule.To] = struct{}{}
	}
	// Record reachability for later language checks; unreachable accepting
	// states are structurally valid and simply make the language smaller.
	reachable := map[int]bool{g.Start: true}
	frontier := []int{g.Start}
	for len(frontier) > 0 {
		state := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for _, to := range delta[state] {
			if !reachable[to] {
				reachable[to] = true
				frontier = append(frontier, to)
			}
		}
	}
	return &compiledGrammar{startState: g.Start, accept: accept, delta: delta}, nil
}

func (t TranscriptionTable) compile(g *compiledGrammar) (*compiledTable, error) {
	if t.Name == "" {
		return nil, errors.New("migrationadmission: transcription table has no name")
	}
	if len(t.Tail) > 1 {
		return nil, errors.New("migrationadmission: tail may contain at most one symbol")
	}
	for _, symbol := range t.Tail {
		if symbol == ReservedEndMarker {
			return nil, errors.New("migrationadmission: tail uses the reserved end marker")
		}
	}
	emit := make(map[byte][]Symbol, len(t.Rules))
	alphabet := map[byte]bool{}
	for _, from := range g.delta {
		for on := range from {
			alphabet[on] = true
		}
	}
	for _, rule := range t.Rules {
		if len(rule.Emit) > 2 {
			return nil, fmt.Errorf("migrationadmission: rule on byte %d emits more than two symbols", rule.On)
		}
		for _, symbol := range rule.Emit {
			if symbol == ReservedEndMarker {
				return nil, fmt.Errorf("migrationadmission: rule on byte %d uses the reserved end marker", rule.On)
			}
		}
		if _, dup := emit[rule.On]; dup {
			return nil, fmt.Errorf("migrationadmission: duplicate transcription rule on byte %d", rule.On)
		}
		emit[rule.On] = append([]Symbol(nil), rule.Emit...)
	}
	for on := range alphabet {
		if _, ok := emit[on]; !ok {
			return nil, fmt.Errorf("migrationadmission: alphabet byte %d has no transcription rule", on)
		}
	}
	return &compiledTable{emit: emit, tail: append([]Symbol(nil), t.Tail...)}, nil
}

// step follows the DFA, returning false when the byte is not legal.
func (g *compiledGrammar) step(state int, on byte) (int, bool) {
	next, ok := g.delta[state][on]
	return next, ok
}

func (t *compiledTable) emitFor(on byte) []Symbol {
	return t.emit[on]
}

func sortedStates(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for state := range m {
		out = append(out, state)
	}
	sort.Ints(out)
	return out
}
