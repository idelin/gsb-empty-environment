// Package statelearning infers a Mealy-machine model of a deterministic
// system under learning (SUL) by active querying, following the L* algorithm.
//
// The input alphabet is supplied by the caller. Every membership query first
// resets the SUL and then replays the whole input word, collecting one output
// per step. An observation table records the observed rows; distinguishing
// suffixes split rows that look equal so that separate states are introduced.
// Conformance is checked by exhaustive exploration up to Config.MaxDepth and
// any counterexample is fed back into the table as additional suffixes until
// the hypothesis agrees with the SUL. Coverage is only claimed within
// MaxDepth; exceeding the query budget returns StatusIncomplete rather than a
// falsely complete model.
package statelearning

import (
	"errors"
	"fmt"
)

// Input is a single alphabet symbol handed to the SUL.
type Input struct {
	// Name identifies the symbol, for example "login_ok".
	Name string
}

// Output is the observation produced by one SUL step.
type Output struct {
	// Response is the service response code or message.
	Response string
	// Authorized reports whether the operation was authorized.
	Authorized bool
}

// SUL is the deterministic service whose behavior is being learned.
//
// A learning session alternates resets with step sequences. Reset returns
// the service to a fixed initial state; every membership query starts with a
// Reset. If Reset or Step fails, learning aborts and the failure is reported
// to the caller instead of being treated as an observation.
type SUL interface {
	Reset() error
	Step(in Input) (Output, error)
}

// Status reports whether the learned model is fully validated.
type Status int

const (
	// StatusIncomplete means the budget was exhausted before every word up
	// to Config.MaxDepth could be checked against the SUL.
	StatusIncomplete Status = iota
	// StatusComplete means every word of length at most MaxDepth agrees
	// between the SUL and the inferred model.
	StatusComplete
)

func (s Status) String() string {
	switch s {
	case StatusComplete:
		return "complete"
	default:
		return "incomplete"
	}
}

// Config parameterizes a learning run.
type Config struct {
	// Alphabet is the caller-supplied input alphabet.
	Alphabet []Input
	// MaxDepth is the maximum word length used for conformance checking.
	// Coverage is only claimed for words no longer than MaxDepth.
	MaxDepth int
	// MaxQueries bounds the number of membership queries (each query
	// resets the SUL once). Zero means unlimited.
	MaxQueries int
}

// Result is the outcome of a learning run.
type Result struct {
	// Status is StatusComplete only when conformance holds up to MaxDepth.
	Status Status
	// Model is the best hypothesis available. When Status is
	// StatusIncomplete it may still be useful for debugging but must not be
	// treated as fully covering.
	Model *MealyMachine
	// Queries is the number of membership queries actually issued.
	Queries int
	// DepthReached is the greatest word depth fully explored during
	// conformance checking. When Status is StatusComplete it equals
	// MaxDepth.
	DepthReached int
	// Err carries a non-budget failure (SUL reset/step error or bad config).
	Err error
}

// ErrBudgetExhausted is wrapped by Result.Err when the query budget is spent
// before conformance could be established. Use errors.Is to detect it.
var ErrBudgetExhausted = errors.New("statelearning: query budget exhausted")

// MealyMachine is an inferred deterministic Mealy machine over the caller's
// alphabet. State 0 is the initial state.
type MealyMachine struct {
	alphabet  []Input
	trans     map[int]map[string]mealyEdge
	stateName map[int]string
}

type mealyEdge struct {
	to     int
	output Output
}

// StateCount returns the number of states in the model.
func (m *MealyMachine) StateCount() int { return len(m.stateName) }

// StateName returns the learner-assigned label of a state.
func (m *MealyMachine) StateName(state int) (string, bool) {
	name, ok := m.stateName[state]
	return name, ok
}

// Transition reports the destination state and output for an input.
func (m *MealyMachine) Transition(state int, in Input) (int, Output, bool) {
	edges, ok := m.trans[state]
	if !ok {
		return 0, Output{}, false
	}
	edge, ok := edges[in.Name]
	if !ok {
		return 0, Output{}, false
	}
	return edge.to, edge.output, true
}

// Trace replays an input word from the initial state and returns the output
// emitted at every step.
func (m *MealyMachine) Trace(word []Input) ([]Output, error) {
	state := 0
	out := make([]Output, 0, len(word))
	for _, in := range word {
		next, o, ok := m.Transition(state, in)
		if !ok {
			return nil, fmt.Errorf("statelearning: no transition for input %q in state %d", in.Name, state)
		}
		state = next
		out = append(out, o)
	}
	return out, nil
}
