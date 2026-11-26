package delegation

import (
	"slices"
	"strconv"
	"strings"
)

type patternNFA struct {
	tokens []string
}

func newPatternNFA(pattern string) (patternNFA, error) {
	tokens, err := patternTokens(pattern)
	if err != nil {
		return patternNFA{}, err
	}
	return patternNFA{tokens: tokens}, nil
}

func (nfa patternNFA) startStates() []int {
	return nfa.epsilonClosure([]int{0})
}

func (nfa patternNFA) epsilonClosure(states []int) []int {
	reachable := append([]int(nil), states...)
	for _, state := range reachable {
		if state < len(nfa.tokens) && nfa.tokens[state] == "**" {
			next := state + 1
			if !slices.Contains(reachable, next) {
				reachable = append(reachable, next)
			}
		}
	}
	slices.Sort(reachable)
	return reachable
}

func (nfa patternNFA) transition(states []int, symbol string) []int {
	var next []int
	for _, state := range states {
		if state >= len(nfa.tokens) {
			continue
		}
		token := nfa.tokens[state]
		switch {
		case token == "**":
			next = append(next, state)
			if state+1 < len(nfa.tokens) && nfa.tokens[state+1] == symbol {
				next = append(next, state+2)
			} else if state+1 == len(nfa.tokens) {
				next = append(next, state+1)
			}
		case token == "*" || token == symbol:
			next = append(next, state+1)
		}
	}
	slices.Sort(next)
	return nfa.epsilonClosure(slices.Compact(next))
}

func (nfa patternNFA) accepts(states []int) bool {
	return slices.Contains(states, len(nfa.tokens))
}

type ruleDFA struct {
	nfa         patternNFA
	stateIndex  map[string]int
	states      [][]int
	transitions [][]int
}

func newRuleDFA(nfa patternNFA, alphabet []string) *ruleDFA {
	dfa := &ruleDFA{
		nfa:        nfa,
		stateIndex: map[string]int{},
	}
	dfa.addState(nfa.startStates())
	for queue := 0; queue < len(dfa.states); queue++ {
		state := dfa.states[queue]
		row := make([]int, len(alphabet))
		for i, symbol := range alphabet {
			row[i] = dfa.addState(nfa.transition(state, symbol))
		}
		dfa.transitions = append(dfa.transitions, row)
	}
	return dfa
}

func (dfa *ruleDFA) addState(state []int) int {
	key := encodeState(state)
	if index, ok := dfa.stateIndex[key]; ok {
		return index
	}
	index := len(dfa.states)
	dfa.stateIndex[key] = index
	dfa.states = append(dfa.states, append([]int(nil), state...))
	return index
}

func (dfa *ruleDFA) accepts(state int) bool {
	return dfa.nfa.accepts(dfa.states[state])
}

type pathMachine struct {
	alphabet    []string
	automata    []*ruleDFA
	stateIndex  map[string]int
	states      [][]int
	transitions [][]int
	matchCache  map[string][]int
}

func newPathMachine(alphabet []string, rules []Rule) (*pathMachine, error) {
	automata := make([]*ruleDFA, 0, len(rules))
	for _, rule := range rules {
		nfa, err := newPatternNFA(rule.Pattern)
		if err != nil {
			return nil, err
		}
		automata = append(automata, newRuleDFA(nfa, alphabet))
	}
	machine := &pathMachine{
		alphabet:   alphabet,
		automata:   automata,
		stateIndex: map[string]int{},
		matchCache: map[string][]int{},
	}
	start := make([]int, len(automata))
	for i := range automata {
		start[i] = 0
	}
	machine.addState(start)
	for queue := 0; queue < len(machine.states); queue++ {
		state := machine.states[queue]
		row := make([]int, len(alphabet))
		for symbolIndex := range alphabet {
			next := make([]int, len(automata))
			for i, automaton := range automata {
				next[i] = automaton.transitions[state[i]][symbolIndex]
			}
			row[symbolIndex] = machine.addState(next)
		}
		machine.transitions = append(machine.transitions, row)
	}
	return machine, nil
}

func (machine *pathMachine) addState(state []int) int {
	key := encodeState(state)
	if index, ok := machine.stateIndex[key]; ok {
		return index
	}
	index := len(machine.states)
	machine.stateIndex[key] = index
	machine.states = append(machine.states, append([]int(nil), state...))
	return index
}

func (machine *pathMachine) startState() int {
	return 0
}

func (machine *pathMachine) transition(state int, symbol string) int {
	symbolIndex := slices.Index(machine.alphabet, symbol)
	if symbolIndex < 0 {
		return -1
	}
	return machine.transitions[state][symbolIndex]
}

func (machine *pathMachine) matchingRules(state int) []int {
	key := strconv.Itoa(state)
	if matches, ok := machine.matchCache[key]; ok {
		return matches
	}
	current := machine.states[state]
	var matches []int
	for i, automaton := range machine.automata {
		if automaton.accepts(current[i]) {
			matches = append(matches, i)
		}
	}
	machine.matchCache[key] = matches
	return matches
}

func encodeState(state []int) string {
	parts := make([]string, len(state))
	for i, value := range state {
		parts[i] = strconv.Itoa(value)
	}
	return strings.Join(parts, ",")
}

func patternMatchesPath(pattern string, path []string) bool {
	nfa, err := newPatternNFA(pattern)
	if err != nil {
		return false
	}
	states := nfa.startStates()
	for _, symbol := range path {
		states = nfa.transition(states, symbol)
		if len(states) == 0 {
			return false
		}
	}
	return nfa.accepts(states)
}
