package policysafe

import (
	"fmt"
	"sort"
)

// SafetyError describes a rejected unsafe rule. The error returned by
// Compile may be a CompileError aggregating several SafetyError values.
type SafetyError struct {
	Rule     Rule
	Variable string
	Reason   string
}

func (e SafetyError) Error() string {
	return fmt.Sprintf("policysafe: unsafe variable %q in rule %s: %s", e.Variable, e.Rule.String(), e.Reason)
}

// checkSafety enforces the Datalog safety property:
//
//  1. every named variable in a rule must occur in a positive body atom;
//  2. every head variable must be bound by a positive body atom;
//  3. facts (rules without a body) must be fully ground.
//
// A variable that appears only inside a negated literal is not safe:
// negation never produces bindings, so allowing it would let an
// unconstrained variable escape its local not-scope.
func checkSafety(prog *Program) []SafetyError {
	var errs []SafetyError
	for _, fact := range prog.Facts {
		errs = append(errs, checkGroundAtom(fact, fact.String())...)
	}
	for _, rule := range prog.Rules {
		errs = append(errs, checkRuleSafety(rule)...)
	}
	return errs
}

func checkGroundAtom(atom Atom, label string) []SafetyError {
	var errs []SafetyError
	for _, term := range atom.Args {
		switch t := term.(type) {
		case *Var:
			errs = append(errs, SafetyError{
				Rule:     Rule{Head: atom},
				Variable: t.Name,
				Reason:   "fact " + label + " must be fully ground",
			})
		case *Wildcard:
			errs = append(errs, SafetyError{
				Rule:     Rule{Head: atom},
				Variable: "_",
				Reason:   "wildcard is not allowed in a fact or rule head: " + label,
			})
		}
	}
	return errs
}

func checkRuleSafety(rule Rule) []SafetyError {
	var errs []SafetyError

	for _, term := range rule.Head.Args {
		if wildcard, ok := term.(*Wildcard); ok {
			_ = wildcard
			errs = append(errs, SafetyError{
				Rule:     rule,
				Variable: "_",
				Reason:   "wildcard cannot appear in a rule head",
			})
		}
	}

	bound := map[string]bool{}
	for _, lit := range rule.Body {
		if lit.Neg {
			continue
		}
		for _, term := range lit.Atom.Args {
			if variable, ok := term.(*Var); ok {
				bound[variable.Name] = true
			}
		}
	}

	used := map[string]bool{}
	for _, lit := range rule.Body {
		for _, term := range lit.Atom.Args {
			if variable, ok := term.(*Var); ok {
				used[variable.Name] = true
			}
		}
	}
	for _, term := range rule.Head.Args {
		if variable, ok := term.(*Var); ok {
			used[variable.Name] = true
		}
	}

	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !bound[name] {
			errs = append(errs, SafetyError{
				Rule:     rule,
				Variable: name,
				Reason:   "variable is not bound by any positive body atom (unbound or leaked from a 'not' literal)",
			})
		}
	}
	return errs
}
