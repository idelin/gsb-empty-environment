package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

// VarSpec describes one string variable: length bounds plus optional
// required prefix and suffix.
type VarSpec struct {
	Name   string `json:"name"`
	MinLen int    `json:"minLen"`
	MaxLen int    `json:"maxLen"`
	Prefix string `json:"prefix,omitempty"`
	Suffix string `json:"suffix,omitempty"`
}

// Equality constrains the concatenation of the Left variables to equal the
// concatenation of the Right variables (one or two variables per side).
type Equality struct {
	Left  []string `json:"left"`
	Right []string `json:"right"`
}

// Problem is the full solver input. Budget counts search states; a value
// <= 0 means unlimited.
type Problem struct {
	Alphabet   string     `json:"alphabet"`
	Variables  []VarSpec  `json:"variables"`
	Forbidden  []string   `json:"forbidden,omitempty"`
	Equalities []Equality `json:"equalities,omitempty"`
	Budget     int64      `json:"budget"`
}

// Solution is the solver output. Assignment is present iff Status == SAT and
// can be checked independently with Verify.
type Solution struct {
	Status      string            `json:"status"`
	Assignment  map[string]string `json:"assignment,omitempty"`
	TotalLength int               `json:"totalLength,omitempty"`
	States      int64             `json:"states"`
}

const (
	StatusSAT     = "SAT"
	StatusUNSAT   = "UNSAT"
	StatusUNKNOWN = "UNKNOWN"
)

// LoadProblem reads and validates a problem file.
func LoadProblem(path string) (*Problem, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Problem
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate checks structural well-formedness of the problem.
func (p *Problem) Validate() error {
	if len(p.Alphabet) == 0 {
		return fmt.Errorf("alphabet must not be empty")
	}
	seen := make(map[byte]bool)
	for i := 0; i < len(p.Alphabet); i++ {
		c := p.Alphabet[i]
		if seen[c] {
			return fmt.Errorf("alphabet contains duplicate character %q", c)
		}
		seen[c] = true
	}
	inAlpha := func(s string) bool {
		for i := 0; i < len(s); i++ {
			if !seen[s[i]] {
				return false
			}
		}
		return true
	}
	names := make(map[string]bool)
	for _, v := range p.Variables {
		if v.Name == "" {
			return fmt.Errorf("variable name must not be empty")
		}
		if names[v.Name] {
			return fmt.Errorf("duplicate variable %q", v.Name)
		}
		names[v.Name] = true
		if v.MinLen < 0 || v.MaxLen < v.MinLen {
			return fmt.Errorf("variable %q: invalid length bounds [%d,%d]", v.Name, v.MinLen, v.MaxLen)
		}
		if !inAlpha(v.Prefix) {
			return fmt.Errorf("variable %q: prefix uses characters outside the alphabet", v.Name)
		}
		if !inAlpha(v.Suffix) {
			return fmt.Errorf("variable %q: suffix uses characters outside the alphabet", v.Name)
		}
	}
	for _, f := range p.Forbidden {
		if f == "" {
			return fmt.Errorf("forbidden substring must not be empty")
		}
		if !inAlpha(f) {
			return fmt.Errorf("forbidden substring %q uses characters outside the alphabet", f)
		}
	}
	for _, eq := range p.Equalities {
		for _, side := range [][]string{eq.Left, eq.Right} {
			if len(side) < 1 || len(side) > 2 {
				return fmt.Errorf("equality sides must list one or two variables")
			}
			for _, n := range side {
				if !names[n] {
					return fmt.Errorf("equality references unknown variable %q", n)
				}
			}
		}
	}
	return nil
}
