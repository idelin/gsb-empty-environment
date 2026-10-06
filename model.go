// Package rescheck checks interleavings of finite action scripts against
// resource state machines, looking for traces that terminate with
// unreleased resources.
package rescheck

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

// Result values recorded on every executed step. Both successful and
// failed actions produce a public, observable transition.
const (
	ResultOK   = "ok"
	ResultFail = "fail"
)

// Status values of a check report.
const (
	StatusViolated = "violated" // counterexample found and replay-verified
	StatusSafe     = "safe"     // all interleavings terminate fully released
	StatusUnknown  = "unknown"  // budget exhausted before a verdict
)

// DefaultBudget bounds explored states when the spec does not set one.
const DefaultBudget = 100000

// Transition is one labelled edge of a resource state machine. Result is
// ResultOK for a normal edge and ResultFail for an explicit failure edge;
// both are public and appear in traces.
type Transition struct {
	From   string `json:"from"`
	Op     string `json:"op"`
	To     string `json:"to"`
	Result string `json:"result,omitempty"`
}

// Resource is a single state machine. Final lists the states in which the
// resource counts as fully released.
type Resource struct {
	Name        string       `json:"name"`
	States      []string     `json:"states"`
	Initial     string       `json:"initial"`
	Final       []string     `json:"final"`
	Transitions []Transition `json:"transitions"`
}

// Action is one step of a script: an operation applied to a resource.
type Action struct {
	Resource string `json:"resource"`
	Op       string `json:"op"`
}

// Script is a finite, ordered sequence of actions.
type Script struct {
	Name    string   `json:"name"`
	Actions []Action `json:"actions"`
}

// Spec is the full checker input: resource machines plus scripts.
type Spec struct {
	Resources []Resource `json:"resources"`
	Scripts   []Script   `json:"scripts"`
	Budget    int        `json:"budget,omitempty"`
}

// Step records one executed action in a trace.
type Step struct {
	Script   string `json:"script"`
	Resource string `json:"resource"`
	Op       string `json:"op"`
	Result   string `json:"result"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// Report is the checker verdict.
type Report struct {
	Status   string   `json:"status"`
	Trace    []Step   `json:"trace,omitempty"`
	Leaked   []string `json:"leaked,omitempty"`
	Explored int      `json:"explored"`
	Reason   string   `json:"reason,omitempty"`
}

// LoadSpec reads and validates a JSON spec file.
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

// Validate checks structural well-formedness of the spec.
func (s *Spec) Validate() error {
	if len(s.Resources) == 0 {
		return fmt.Errorf("no resources defined")
	}
	names := map[string]bool{}
	for i := range s.Resources {
		r := &s.Resources[i]
		if r.Name == "" {
			return fmt.Errorf("resource %d: empty name", i)
		}
		if names[r.Name] {
			return fmt.Errorf("duplicate resource %q", r.Name)
		}
		names[r.Name] = true
		if len(r.States) == 0 {
			return fmt.Errorf("resource %q: no states", r.Name)
		}
		stateSet := map[string]bool{}
		for _, st := range r.States {
			if stateSet[st] {
				return fmt.Errorf("resource %q: duplicate state %q", r.Name, st)
			}
			stateSet[st] = true
		}
		if !stateSet[r.Initial] {
			return fmt.Errorf("resource %q: initial state %q not in states", r.Name, r.Initial)
		}
		if len(r.Final) == 0 {
			return fmt.Errorf("resource %q: no final (released) states", r.Name)
		}
		for _, f := range r.Final {
			if !stateSet[f] {
				return fmt.Errorf("resource %q: final state %q not in states", r.Name, f)
			}
		}
		seen := map[string]map[string]bool{}
		for _, t := range r.Transitions {
			if !stateSet[t.From] || !stateSet[t.To] {
				return fmt.Errorf("resource %q: transition %q -%s-> %q uses unknown state", r.Name, t.From, t.Op, t.To)
			}
			if t.Op == "" {
				return fmt.Errorf("resource %q: transition with empty op", r.Name)
			}
			res := t.Result
			if res == "" {
				res = ResultOK
			}
			if res != ResultOK && res != ResultFail {
				return fmt.Errorf("resource %q: bad result %q", r.Name, t.Result)
			}
			if seen[t.From] == nil {
				seen[t.From] = map[string]bool{}
			}
			if seen[t.From][t.Op] {
				return fmt.Errorf("resource %q: nondeterministic transitions for (%q, %q)", r.Name, t.From, t.Op)
			}
			seen[t.From][t.Op] = true
		}
	}
	scriptNames := map[string]bool{}
	for i := range s.Scripts {
		sc := &s.Scripts[i]
		if sc.Name == "" {
			return fmt.Errorf("script %d: empty name", i)
		}
		if scriptNames[sc.Name] {
			return fmt.Errorf("duplicate script %q", sc.Name)
		}
		scriptNames[sc.Name] = true
		for j, a := range sc.Actions {
			if !names[a.Resource] {
				return fmt.Errorf("script %q action %d: unknown resource %q", sc.Name, j, a.Resource)
			}
			if a.Op == "" {
				return fmt.Errorf("script %q action %d: empty op", sc.Name, j)
			}
		}
	}
	if s.Budget < 0 {
		return fmt.Errorf("negative budget")
	}
	return nil
}

// machine is a compiled resource: a deterministic transition table where
// every action, including failures, has a public transition.
type machine struct {
	res   *Resource
	table map[string]map[string]Transition
	final map[string]bool
}

func compile(r *Resource) *machine {
	m := &machine{
		res:   r,
		table: map[string]map[string]Transition{},
		final: map[string]bool{},
	}
	for _, t := range r.Transitions {
		if m.table[t.From] == nil {
			m.table[t.From] = map[string]Transition{}
		}
		m.table[t.From][t.Op] = t
	}
	for _, f := range r.Final {
		m.final[f] = true
	}
	return m
}

// step applies op in the given state. An explicitly declared failure edge
// is taken as-is; if no edge is declared, the action fails publicly in
// place (observable self-loop) rather than silently blocking.
func (m *machine) step(state, op string) (next, result string) {
	if t, ok := m.table[state][op]; ok {
		res := t.Result
		if res == "" {
			res = ResultOK
		}
		return t.To, res
	}
	return state, ResultFail
}

// machines compiles all resources of the spec.
func (s *Spec) machines() []*machine {
	ms := make([]*machine, len(s.Resources))
	for i := range s.Resources {
		ms[i] = compile(&s.Resources[i])
	}
	return ms
}

// resourceIndex maps resource name to its index in Resources.
func (s *Spec) resourceIndex() map[string]int {
	idx := map[string]int{}
	for i, r := range s.Resources {
		idx[r.Name] = i
	}
	return idx
}
