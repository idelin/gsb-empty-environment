package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

// LinkMode declares how a link's capacity is shared between directions.
type LinkMode string

const (
	// ModeUnidirectional: only From->To is usable.
	ModeUnidirectional LinkMode = "unidirectional"
	// ModeShared: bidirectional, both directions share one capacity budget.
	ModeShared LinkMode = "shared"
	// ModeSeparate: bidirectional, each direction has its own capacity budget.
	ModeSeparate LinkMode = "separate"
)

// Link is a network edge. Capacity is in packets per tick.
type Link struct {
	ID       string   `json:"id"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Capacity float64  `json:"capacity"`
	Mode     LinkMode `json:"mode"`
}

// Flow is a fixed-route traffic demand. Demand is in packets per tick.
type Flow struct {
	ID     string   `json:"id"`
	Src    string   `json:"src"`
	Dst    string   `json:"dst"`
	Demand float64  `json:"demand"`
	Route  []string `json:"route"`
}

// Failure disables the listed links for one scenario.
type Failure struct {
	Name  string   `json:"name"`
	Links []string `json:"links"`
}

// Scenario is the full input document.
type Scenario struct {
	Links    []Link    `json:"links"`
	Flows    []Flow    `json:"flows"`
	Failures []Failure `json:"failures"`
	SimTicks int       `json:"simTicks"`
}

// LoadScenario reads and validates a scenario JSON file.
func LoadScenario(path string) (*Scenario, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sc Scenario
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("parse %s: %v", path, err)
	}
	if err := sc.validate(); err != nil {
		return nil, fmt.Errorf("invalid scenario: %v", err)
	}
	return &sc, nil
}

func (sc *Scenario) validate() error {
	linkIDs := map[string]bool{}
	for i := range sc.Links {
		l := &sc.Links[i]
		if l.ID == "" {
			return fmt.Errorf("link with empty id")
		}
		if linkIDs[l.ID] {
			return fmt.Errorf("duplicate link id %q", l.ID)
		}
		linkIDs[l.ID] = true
		if l.From == "" || l.To == "" || l.From == l.To {
			return fmt.Errorf("link %q: bad endpoints %q->%q", l.ID, l.From, l.To)
		}
		if l.Capacity <= 0 {
			return fmt.Errorf("link %q: capacity must be > 0", l.ID)
		}
		if l.Mode == "" {
			l.Mode = ModeUnidirectional
		}
		switch l.Mode {
		case ModeUnidirectional, ModeShared, ModeSeparate:
		default:
			return fmt.Errorf("link %q: unknown mode %q", l.ID, l.Mode)
		}
	}
	flowIDs := map[string]bool{}
	for i := range sc.Flows {
		f := &sc.Flows[i]
		if f.ID == "" {
			return fmt.Errorf("flow with empty id")
		}
		if flowIDs[f.ID] {
			return fmt.Errorf("duplicate flow id %q", f.ID)
		}
		flowIDs[f.ID] = true
		if f.Demand <= 0 {
			return fmt.Errorf("flow %q: demand must be > 0", f.ID)
		}
		if len(f.Route) < 2 {
			return fmt.Errorf("flow %q: route needs at least 2 nodes", f.ID)
		}
		if f.Route[0] != f.Src || f.Route[len(f.Route)-1] != f.Dst {
			return fmt.Errorf("flow %q: route endpoints %s..%s do not match src/dst %s..%s",
				f.ID, f.Route[0], f.Route[len(f.Route)-1], f.Src, f.Dst)
		}
	}
	for _, fl := range sc.Failures {
		if fl.Name == "" {
			return fmt.Errorf("failure scenario with empty name")
		}
		for _, id := range fl.Links {
			if !linkIDs[id] {
				return fmt.Errorf("failure %q references unknown link %q", fl.Name, id)
			}
		}
	}
	return nil
}
