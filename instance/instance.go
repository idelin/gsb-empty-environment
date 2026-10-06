// Package instance defines finite-domain constraint satisfaction
// instances: a set of variables, each with a finite domain, and table
// constraints listing the allowed value tuples for their variables.
package instance

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

// Assignment maps a variable name to one of its domain values.
type Assignment map[string]string

// Variable declares a finite-domain variable.
type Variable struct {
	Name   string   `json:"name"`
	Domain []string `json:"domain"`
}

// Constraint is a table constraint: the tuple of values taken by Vars
// (in the listed order) must be one of the Allowed tuples. A constraint
// with no Vars is a constant: Allowed [[ ]] means true, Allowed [] means
// false.
type Constraint struct {
	Name    string     `json:"name"`
	Vars    []string   `json:"vars"`
	Allowed [][]string `json:"allowed"`
}

// Instance is a finite-domain constraint satisfaction problem.
type Instance struct {
	Variables   []Variable   `json:"variables"`
	Constraints []Constraint `json:"constraints"`
}

// Load reads and validates an instance from a JSON file ("-" reads stdin).
func Load(path string) (*Instance, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes and validates an instance from JSON.
func Parse(data []byte) (*Instance, error) {
	var inst Instance
	if err := json.Unmarshal(data, &inst); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	if err := inst.Validate(); err != nil {
		return nil, err
	}
	return &inst, nil
}

// Validate checks the instance for structural consistency.
func (inst *Instance) Validate() error {
	seen := map[string]bool{}
	domains := map[string]map[string]bool{}
	for _, v := range inst.Variables {
		if v.Name == "" {
			return fmt.Errorf("variable with empty name")
		}
		if seen[v.Name] {
			return fmt.Errorf("duplicate variable %q", v.Name)
		}
		seen[v.Name] = true
		if len(v.Domain) == 0 {
			return fmt.Errorf("variable %q has an empty domain", v.Name)
		}
		d := map[string]bool{}
		for _, val := range v.Domain {
			if d[val] {
				return fmt.Errorf("variable %q: duplicate domain value %q", v.Name, val)
			}
			d[val] = true
		}
		domains[v.Name] = d
	}
	cnames := map[string]bool{}
	for _, c := range inst.Constraints {
		if c.Name == "" {
			return fmt.Errorf("constraint with empty name")
		}
		if cnames[c.Name] {
			return fmt.Errorf("duplicate constraint %q", c.Name)
		}
		cnames[c.Name] = true
		for _, v := range c.Vars {
			if !seen[v] {
				return fmt.Errorf("constraint %q: unknown variable %q", c.Name, v)
			}
		}
		for _, t := range c.Allowed {
			if len(t) != len(c.Vars) {
				return fmt.Errorf("constraint %q: tuple %v has arity %d, want %d",
					c.Name, t, len(t), len(c.Vars))
			}
			for i, val := range t {
				if !domains[c.Vars[i]][val] {
					return fmt.Errorf("constraint %q: value %q not in domain of variable %q",
						c.Name, val, c.Vars[i])
				}
			}
		}
	}
	return nil
}

// TupleKey renders a value tuple unambiguously for map lookup.
func TupleKey(tuple []string) string {
	return fmt.Sprintf("%q", tuple)
}

// Compiled precomputes per-constraint lookup sets for fast evaluation.
type Compiled struct {
	Inst     *Instance
	allowed  []map[string]struct{}
	varIndex map[string]int
}

// Compile builds the lookup structures used by Satisfies and Scope.
// The instance must already be validated (see Parse).
func Compile(inst *Instance) *Compiled {
	c := &Compiled{Inst: inst, varIndex: map[string]int{}}
	for i, v := range inst.Variables {
		c.varIndex[v.Name] = i
	}
	for _, con := range inst.Constraints {
		set := make(map[string]struct{}, len(con.Allowed))
		for _, t := range con.Allowed {
			set[TupleKey(t)] = struct{}{}
		}
		c.allowed = append(c.allowed, set)
	}
	return c
}

// Satisfies reports whether assign satisfies constraint ci.
func (c *Compiled) Satisfies(ci int, assign Assignment) bool {
	con := c.Inst.Constraints[ci]
	tuple := make([]string, len(con.Vars))
	for i, v := range con.Vars {
		tuple[i] = assign[v]
	}
	_, ok := c.allowed[ci][TupleKey(tuple)]
	return ok
}

// SatisfiesAll reports whether assign satisfies every constraint in set.
func (c *Compiled) SatisfiesAll(set []int, assign Assignment) bool {
	for _, ci := range set {
		if !c.Satisfies(ci, assign) {
			return false
		}
	}
	return true
}

// Scope returns the indices (in declaration order) of the variables
// referenced by at least one constraint in set.
func (c *Compiled) Scope(set []int) []int {
	used := make([]bool, len(c.Inst.Variables))
	for _, ci := range set {
		for _, v := range c.Inst.Constraints[ci].Vars {
			used[c.varIndex[v]] = true
		}
	}
	var scope []int
	for i := range used {
		if used[i] {
			scope = append(scope, i)
		}
	}
	return scope
}
