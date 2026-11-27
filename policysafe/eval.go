package policysafe

import (
	"fmt"
	"sort"
)

// relation stores the derived ground tuples of one predicate.
type relation struct {
	tuples map[string]Tuple
}

func newRelation() *relation {
	return &relation{tuples: map[string]Tuple{}}
}

func (r *relation) add(t Tuple) bool {
	key := t.key()
	if _, ok := r.tuples[key]; ok {
		return false
	}
	r.tuples[key] = append(Tuple{}, t...)
	return true
}

func (r *relation) has(t Tuple) bool {
	_, ok := r.tuples[t.key()]
	return ok
}

type database struct {
	relations map[string]*relation
}

func newDatabase() *database {
	return &database{relations: map[string]*relation{}}
}

func (db *database) get(pred string) *relation {
	rel, ok := db.relations[pred]
	if !ok {
		rel = newRelation()
		db.relations[pred] = rel
	}
	return rel
}

func (db *database) insert(atom Atom) error {
	tuple, err := groundTuple(atom)
	if err != nil {
		return err
	}
	db.get(atom.Pred).add(tuple)
	return nil
}

// GroundTuple converts a fully ground atom into its constant vector.
func groundTuple(atom Atom) (Tuple, error) {
	tuple := make(Tuple, 0, len(atom.Args))
	for _, term := range atom.Args {
		constant, ok := term.(*Const)
		if !ok {
			return nil, fmt.Errorf("policysafe: atom %s is not ground", atom.String())
		}
		tuple = append(tuple, constant.Value)
	}
	return tuple, nil
}

// evaluate computes the least fixed point of the program against an
// additional set of request-scoped (session) facts. The database is local
// to the call, so concurrent requests never share state.
func (c *Compiled) evaluate(session []Atom) (*database, error) {
	db := newDatabase()

	for _, fact := range c.program.Facts {
		if err := db.insert(fact); err != nil {
			return nil, err
		}
	}
	for _, atom := range session {
		if expected, ok := c.arity[atom.Pred]; ok && expected != atom.Arity() {
			return nil, fmt.Errorf("policysafe: session fact %s has arity %d, policy expects %d",
				atom.String(), atom.Arity(), expected)
		}
		if err := db.insert(atom); err != nil {
			return nil, err
		}
	}

	predsByStratum := make([][]string, c.maxStratum+1)
	for pred, level := range c.strata {
		predsByStratum[level] = append(predsByStratum[level], pred)
	}

	for level := 0; level <= c.maxStratum; level++ {
		group := predsByStratum[level]
		sort.Strings(group)
		rules := make([]Rule, 0)
		for _, pred := range group {
			rules = append(rules, c.rules[pred]...)
		}
		if len(rules) == 0 {
			continue
		}
		for {
			added := 0
			for _, rule := range rules {
				delta, err := c.evalRule(rule, db)
				if err != nil {
					return nil, err
				}
				added += delta
			}
			if added == 0 {
				break
			}
		}
	}
	return db, nil
}

// evalRule derives all head tuples of one rule and returns the number of
// tuples that were newly added to the relation.
func (c *Compiled) evalRule(rule Rule, db *database) (int, error) {
	added := 0
	var walk func(int, map[string]string) error
	walk = func(index int, env map[string]string) error {
		if index == len(rule.Body) {
			tuple := make(Tuple, 0, len(rule.Head.Args))
			for _, term := range rule.Head.Args {
				switch t := term.(type) {
				case *Const:
					tuple = append(tuple, t.Value)
				case *Var:
					tuple = append(tuple, env[t.Name])
				default:
					return fmt.Errorf("policysafe: internal error: non-ground head term in %s", rule.String())
				}
			}
			if db.get(rule.Head.Pred).add(tuple) {
				added++
			}
			return nil
		}

		lit := rule.Body[index]

		// A negated literal is a pure local test over the variables bound
		// so far: it succeeds when no ground tuple matches and never yields
		// new bindings. Variables inside it are guaranteed safe (bound by an
		// earlier positive atom) by the compile-time safety check.
		if lit.Neg {
			if !c.matchExists(lit.Atom, env, db) {
				if err := walk(index+1, env); err != nil {
					return err
				}
			}
			return nil
		}

		rel := db.relations[lit.Atom.Pred]
		var tuples []Tuple
		if rel != nil {
			for _, tuple := range rel.tuples {
				tuples = append(tuples, tuple)
			}
			sort.Slice(tuples, func(i, j int) bool { return tuples[i].key() < tuples[j].key() })
		}

		for _, tuple := range tuples {
			bindings, ok := unify(lit.Atom.Args, tuple, env)
			if !ok {
				continue
			}
			if err := walk(index+1, bindings); err != nil {
				return err
			}
		}
		return nil
	}

	err := walk(0, map[string]string{})
	return added, err
}

func (c *Compiled) matchExists(atom Atom, env map[string]string, db *database) bool {
	rel := db.relations[atom.Pred]
	if rel == nil {
		return false
	}
	for _, tuple := range rel.tuples {
		if _, ok := unify(atom.Args, tuple, env); ok {
			return true
		}
	}
	return false
}

// unify matches atom terms against a ground tuple under env. Wildcards
// match anything; variables must agree with prior bindings.
func unify(args []Term, tuple Tuple, env map[string]string) (map[string]string, bool) {
	if len(args) != len(tuple) {
		return nil, false
	}
	next := make(map[string]string, len(env))
	for key, value := range env {
		next[key] = value
	}
	for i, term := range args {
		switch t := term.(type) {
		case *Const:
			if t.Value != tuple[i] {
				return nil, false
			}
		case *Wildcard:
			continue
		case *Var:
			if existing, ok := next[t.Name]; ok {
				if existing != tuple[i] {
					return nil, false
				}
			} else {
				next[t.Name] = tuple[i]
			}
		}
	}
	return next, true
}

// Binding is one successful match of a query goal against derived facts.
type Binding map[string]string

// Query evaluates the policy with the given session facts and returns
// every match of the goal atom. An empty result means undefined, which
// callers must treat as failure (the Authorize API does so automatically).
func (c *Compiled) Query(goal Atom, session []Atom) ([]Binding, error) {
	if expected, ok := c.arity[goal.Pred]; ok && expected != goal.Arity() {
		return nil, fmt.Errorf("policysafe: query goal %s has arity %d, policy expects %d",
			goal.String(), goal.Arity(), expected)
	}
	db, err := c.evaluate(session)
	if err != nil {
		return nil, err
	}
	rel := db.relations[goal.Pred]
	if rel == nil {
		return nil, nil
	}

	keys := make([]string, 0, len(rel.tuples))
	for key := range rel.tuples {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var results []Binding
	for _, key := range keys {
		tuple := rel.tuples[key]
		bindings, ok := unify(goal.Args, tuple, map[string]string{})
		if !ok {
			continue
		}
		if len(bindings) == 0 {
			results = append(results, Binding{})
			continue
		}
		out := Binding{}
		for name, value := range bindings {
			out[name] = value
		}
		results = append(results, out)
	}
	return results, nil
}
