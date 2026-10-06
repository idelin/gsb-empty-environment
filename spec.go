package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sort"
)

// Transition is one public state transition of an action. The Outcome
// (e.g. "ok" or "fail") is observable and recorded in traces.
type Transition struct {
	Outcome string            `json:"outcome"`
	When    map[string]string `json:"when"`
	Set     map[string]string `json:"set"`
}

// Action is a named operation with one or more possible transitions,
// including failure transitions.
type Action struct {
	Transitions []Transition `json:"transitions"`
}

// Script is a finite, ordered list of action names.
type Script struct {
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

// Budget bounds the exploration.
type Budget struct {
	MaxStates int `json:"max_states"`
	MaxDepth  int `json:"max_depth"`
}

// Spec is the full checker input: a resource state machine plus the
// finite action scripts to interleave.
type Spec struct {
	Resources []string            `json:"resources"`
	Initial   map[string]string   `json:"initial"`
	Terminals map[string][]string `json:"terminals"`
	Actions   map[string]Action   `json:"actions"`
	Scripts   []Script            `json:"scripts"`
	Budget    Budget              `json:"budget"`
}

// LoadSpec reads and validates a spec from a JSON file.
func LoadSpec(path string) (*Spec, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse %s: %v", path, err)
	}
	if err := spec.Validate(); err != nil {
		return nil, fmt.Errorf("invalid spec %s: %v", path, err)
	}
	return &spec, nil
}

// Validate checks structural consistency of the spec.
func (s *Spec) Validate() error {
	if len(s.Resources) == 0 {
		return fmt.Errorf("no resources declared")
	}
	known := map[string]bool{}
	for _, r := range s.Resources {
		if known[r] {
			return fmt.Errorf("duplicate resource %q", r)
		}
		known[r] = true
	}
	for _, r := range s.Resources {
		if _, ok := s.Initial[r]; !ok {
			return fmt.Errorf("resource %q missing initial state", r)
		}
		if len(s.Terminals[r]) == 0 {
			return fmt.Errorf("resource %q has no terminal (released) states", r)
		}
	}
	for name, a := range s.Actions {
		if len(a.Transitions) == 0 {
			return fmt.Errorf("action %q has no transitions", name)
		}
		for i, t := range a.Transitions {
			for r := range t.When {
				if !known[r] {
					return fmt.Errorf("action %q transition %d guards unknown resource %q", name, i, r)
				}
			}
			for r := range t.Set {
				if !known[r] {
					return fmt.Errorf("action %q transition %d sets unknown resource %q", name, i, r)
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, sc := range s.Scripts {
		if sc.Name == "" {
			return fmt.Errorf("script with empty name")
		}
		if seen[sc.Name] {
			return fmt.Errorf("duplicate script name %q", sc.Name)
		}
		seen[sc.Name] = true
		for _, an := range sc.Actions {
			if _, ok := s.Actions[an]; !ok {
				return fmt.Errorf("script %q references unknown action %q", sc.Name, an)
			}
		}
	}
	if s.Budget.MaxStates <= 0 {
		s.Budget.MaxStates = 100000
	}
	if s.Budget.MaxDepth <= 0 {
		s.Budget.MaxDepth = 1024
	}
	return nil
}

// resourceOrder returns resources in a deterministic order.
func (s *Spec) resourceOrder() []string {
	out := append([]string(nil), s.Resources...)
	sort.Strings(out)
	return out
}
