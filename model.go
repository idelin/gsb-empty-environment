package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

type stateSpec struct {
	Name   string   `json:"name"`
	Labels []string `json:"labels"`
}

type transitionSpec struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type machineSpec struct {
	Initial     string           `json:"initial"`
	States      []stateSpec      `json:"states"`
	Transitions []transitionSpec `json:"transitions"`
}

// Machine is a finite state graph with propositional labels on states.
type Machine struct {
	Initial string
	Order   []string // declaration order, used for deterministic output
	Labels  map[string]map[string]bool
	Adj     map[string][]string
}

// HasLabel reports whether the given state carries the proposition.
func (m *Machine) HasLabel(state, prop string) bool {
	return m.Labels[state][prop]
}

// LoadMachine parses a JSON state machine specification.
func LoadMachine(path string) (*Machine, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var spec machineSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	return buildMachine(&spec)
}

func buildMachine(spec *machineSpec) (*Machine, error) {
	m := &Machine{
		Initial: spec.Initial,
		Labels:  make(map[string]map[string]bool),
		Adj:     make(map[string][]string),
	}
	for _, st := range spec.States {
		if st.Name == "" {
			return nil, fmt.Errorf("state with empty name")
		}
		if _, dup := m.Labels[st.Name]; dup {
			return nil, fmt.Errorf("duplicate state %q", st.Name)
		}
		labels := make(map[string]bool)
		for _, l := range st.Labels {
			labels[l] = true
		}
		m.Labels[st.Name] = labels
		m.Order = append(m.Order, st.Name)
	}
	if _, ok := m.Labels[spec.Initial]; !ok {
		return nil, fmt.Errorf("initial state %q is not declared", spec.Initial)
	}
	for _, tr := range spec.Transitions {
		if _, ok := m.Labels[tr.From]; !ok {
			return nil, fmt.Errorf("transition from undeclared state %q", tr.From)
		}
		if _, ok := m.Labels[tr.To]; !ok {
			return nil, fmt.Errorf("transition to undeclared state %q", tr.To)
		}
		m.Adj[tr.From] = append(m.Adj[tr.From], tr.To)
	}
	return m, nil
}
