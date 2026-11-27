package policysafe

import (
	"errors"
	"fmt"
)

// ErrSearchLimit is returned when the bounded search exhausts its
// evaluation budget without finding a counterexample. It is distinct from
// a clean negative result (nil Counterexample): the scenario is then
// neither proven reachable nor proven safe.
var ErrSearchLimit = errors.New("policysafe: counterexample search budget exhausted without conclusive result")

// Scenario is a host-declared "must never be allowed" situation, expressed
// as the forbidden allow goal plus the request context that must hold in
// every attempted world (for example anonymous subjects performing writes,
// or cross-tenant resource access).
type Scenario struct {
	// Name identifies the scenario in violation reports.
	Name string
	// Goal is the forbidden derivable atom, typically allow(...) with
	// variables or wildcards whose reachability is being tested.
	Goal Atom
	// FixedFacts hold in every world; they encode the forbidden context
	// (e.g. subject(anonymous), action(write)).
	FixedFacts []Atom
	// CandidateFacts are the attacker-controllable input facts the search
	// may add (e.g. role tables, tenant memberships). Only base
	// predicates — predicates without deriving rules — are admissible.
	CandidateFacts []Atom
}

// SearchOptions bounds the search. Zero values select safe defaults.
type SearchOptions struct {
	// MaxFacts limits how many candidate facts may appear in one world.
	// Default: len(candidates).
	MaxFacts int
	// MaxEvaluations caps the number of policy evaluations performed.
	// Default: 5000.
	MaxEvaluations int
}

// Counterexample is a cardinality-minimal set of candidate facts that
// makes the forbidden goal derivable.
type Counterexample struct {
	Scenario    string
	Facts       []Atom
	Size        int
	Bindings    Binding
	AlsoDenied  bool
	Evaluations int
}

// Violation pairs a scenario with the counterexample that defeats it.
type Violation struct {
	Scenario       string
	Counterexample *Counterexample
}

// FindCounterexample searches for a minimal input fact set under which the
// scenario's goal becomes derivable. A nil counterexample with a nil error
// proves the scenario is unreachable within the declared universe of facts.
func (c *Compiled) FindCounterexample(scenario Scenario, opts SearchOptions) (*Counterexample, error) {
	candidates, err := c.prepareCandidates(scenario)
	if err != nil {
		return nil, err
	}

	maxFacts := opts.MaxFacts
	if maxFacts <= 0 || maxFacts > len(candidates) {
		maxFacts = len(candidates)
	}
	maxEvaluations := opts.MaxEvaluations
	if maxEvaluations <= 0 {
		maxEvaluations = 5000
	}

	evaluations := 0
	limitHit := false

	// Iterative deepening over subset size guarantees the first hit is
	// cardinality-minimal.
	for size := 0; size <= maxFacts; size++ {
		var found *Counterexample
		forEachCombination(len(candidates), size, func(indices []int) bool {
			if evaluations >= maxEvaluations {
				limitHit = true
				return false
			}
			evaluations++

			world := make([]Atom, 0, len(scenario.FixedFacts)+size)
			world = append(world, scenario.FixedFacts...)
			for _, idx := range indices {
				world = append(world, candidates[idx])
			}

			bindings, queryErr := c.Query(scenario.Goal, world)
			if queryErr != nil {
				return true
			}
			if len(bindings) == 0 {
				return true
			}

			denied, _ := c.relationTuples("deny", world)
			facts := make([]Atom, 0, size)
			for _, idx := range indices {
				facts = append(facts, candidates[idx])
			}
			found = &Counterexample{
				Scenario:    scenario.Name,
				Facts:       facts,
				Size:        size,
				Bindings:    bindings[0],
				AlsoDenied:  len(denied) > 0,
				Evaluations: evaluations,
			}
			return false
		})
		if found != nil {
			return found, nil
		}
		if limitHit {
			return nil, ErrSearchLimit
		}
	}
	return nil, nil
}

// Verify runs bounded searches for every scenario and returns the ones
// that admit a counterexample.
func (c *Compiled) Verify(scenarios []Scenario, opts SearchOptions) ([]Violation, error) {
	var violations []Violation
	for _, scenario := range scenarios {
		counterexample, err := c.FindCounterexample(scenario, opts)
		if err != nil {
			return nil, fmt.Errorf("policysafe: scenario %q: %w", scenario.Name, err)
		}
		if counterexample != nil {
			violations = append(violations, Violation{
				Scenario:       scenario.Name,
				Counterexample: counterexample,
			})
		}
	}
	return violations, nil
}

func (c *Compiled) prepareCandidates(scenario Scenario) ([]Atom, error) {
	dedup := map[string]Atom{}
	fixedKeys := map[string]bool{}
	var ordered []Atom

	add := func(atom Atom, where string) error {
		if expected, ok := c.arity[atom.Pred]; ok && expected != atom.Arity() {
			return fmt.Errorf("policysafe: %s fact %s has arity %d, policy expects %d",
				where, atom.String(), atom.Arity(), expected)
		}
		if _, ok := c.rules[atom.Pred]; ok {
			return fmt.Errorf("policysafe: %s fact %s targets derived predicate %q; only base predicates are admissible inputs",
				where, atom.String(), atom.Pred)
		}
		if _, err := groundTuple(atom); err != nil {
			return fmt.Errorf("policysafe: %s: %w", where, err)
		}
		key := atom.Pred + "\x00" + Tuple(termValues(atom.Args)).key()
		if where == "fixed" {
			fixedKeys[key] = true
		}
		if _, exists := dedup[key]; !exists {
			dedup[key] = atom
			ordered = append(ordered, atom)
		}
		return nil
	}

	for _, atom := range scenario.FixedFacts {
		if err := add(atom, "fixed"); err != nil {
			return nil, err
		}
	}
	for _, atom := range scenario.CandidateFacts {
		if err := add(atom, "candidate"); err != nil {
			return nil, err
		}
	}

	// Facts already asserted by the program itself need not be searched.
	var candidates []Atom
	for _, atom := range ordered {
		key := atom.Pred + "\x00" + Tuple(termValues(atom.Args)).key()
		if fixedKeys[key] {
			continue
		}
		if c.isBaseFact(atom) {
			continue
		}
		candidates = append(candidates, atom)
	}
	return candidates, nil
}

func (c *Compiled) isBaseFact(atom Atom) bool {
	for _, fact := range c.program.Facts {
		if fact.Pred != atom.Pred || fact.Arity() != atom.Arity() {
			continue
		}
		same := true
		for i := range fact.Args {
			left, _ := fact.Args[i].(*Const)
			right, _ := atom.Args[i].(*Const)
			if left == nil || right == nil || left.Value != right.Value {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

func termValues(terms []Term) []string {
	values := make([]string, 0, len(terms))
	for _, term := range terms {
		if constant, ok := term.(*Const); ok {
			values = append(values, constant.Value)
		} else {
			values = append(values, term.String())
		}
	}
	return values
}

// forEachCombination invokes fn with every index combination of the given
// size in lexicographic order. Returning false from fn stops iteration.
func forEachCombination(n, size int, fn func([]int) bool) {
	if size < 0 || size > n {
		return
	}
	if size == 0 {
		fn(nil)
		return
	}
	indices := make([]int, size)
	for i := range indices {
		indices[i] = i
	}
	for {
		if !fn(indices) {
			return
		}
		i := size - 1
		for i >= 0 && indices[i] == n-size+i {
			i--
		}
		if i < 0 {
			return
		}
		indices[i]++
		for j := i + 1; j < size; j++ {
			indices[j] = indices[j-1] + 1
		}
	}
}
