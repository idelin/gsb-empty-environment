package policysafe

import (
	"fmt"
	"sort"
	"strings"
)

// CompileError aggregates every static problem found in one pass, so a
// policy update cannot hide behind the first reported defect.
type CompileError struct {
	SafetyErrors        []SafetyError
	StratificationError *StratificationError
	ArityErrors         []string
}

func (e *CompileError) Error() string {
	var parts []string
	for _, msg := range e.ArityErrors {
		parts = append(parts, msg)
	}
	for _, se := range e.SafetyErrors {
		parts = append(parts, se.Error())
	}
	if e.StratificationError != nil {
		parts = append(parts, e.StratificationError.Error())
	}
	return "policysafe: policy rejected:\n  - " + strings.Join(parts, "\n  - ")
}

// Compiled is the statically validated, ready-to-evaluate policy. It is
// safe for concurrent Authorize/Search calls: evaluation builds a fresh
// relation database per request.
type Compiled struct {
	program *Program
	graph   *depGraph
	strata  map[string]int
	arity   map[string]int
	rules   map[string][]Rule

	maxStratum int
}

// Compile parses source and runs the full safety pipeline.
func Compile(source string) (*Compiled, error) {
	prog, err := Parse(source)
	if err != nil {
		return nil, err
	}
	return Build(prog)
}

// Build validates an already-parsed Program and returns a Compiled policy.
func Build(prog *Program) (*Compiled, error) {
	compileErr := &CompileError{}

	arity, arityErrs := checkArity(prog)
	compileErr.ArityErrors = arityErrs
	compileErr.SafetyErrors = checkSafety(prog)

	graph := buildDepGraph(prog)
	compileErr.StratificationError = checkStratification(graph)

	if len(compileErr.ArityErrors) > 0 ||
		len(compileErr.SafetyErrors) > 0 ||
		compileErr.StratificationError != nil {
		return nil, compileErr
	}

	strata := assignStrata(graph)
	rules := map[string][]Rule{}
	maxStratum := 0
	for _, rule := range prog.Rules {
		rules[rule.Head.Pred] = append(rules[rule.Head.Pred], rule)
	}
	for _, level := range strata {
		if level > maxStratum {
			maxStratum = level
		}
	}

	return &Compiled{
		program:    prog,
		graph:      graph,
		strata:     strata,
		arity:      arity,
		rules:      rules,
		maxStratum: maxStratum,
	}, nil
}

// Program returns the parsed rule set backing the compiled policy.
func (c *Compiled) Program() *Program { return c.program }

// Stratum returns the computed stratum of a predicate. Unknown predicates
// default to the base stratum.
func (c *Compiled) Stratum(pred string) int { return c.strata[pred] }

func checkArity(prog *Program) (map[string]int, []string) {
	arity := map[string]int{}
	var errs []string

	record := func(atom Atom, where string) {
		n := atom.Arity()
		if existing, ok := arity[atom.Pred]; ok && existing != n {
			errs = append(errs, fmt.Sprintf(
				"policysafe: predicate %q used with conflicting arities %d and %d at %s",
				atom.Pred, existing, n, where))
			return
		}
		arity[atom.Pred] = n
	}

	for _, fact := range prog.Facts {
		record(fact, "fact "+fact.String())
	}
	for _, rule := range prog.Rules {
		record(rule.Head, "rule head "+rule.Head.String())
		for _, lit := range rule.Body {
			record(lit.Atom, "rule body "+lit.String())
		}
	}
	sort.Strings(errs)
	return arity, errs
}
