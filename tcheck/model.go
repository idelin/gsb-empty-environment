// Package tcheck implements a temporal property checker for finite
// state machines whose states carry proposition labels.
//
// Two properties are checked over the reachable state graph:
//
//   Safety:   every transition into a "danger" state must originate
//             from an "authorized" state (authorization before danger).
//   Liveness: on every infinite execution, whenever "request" holds,
//             "response" eventually holds afterwards.
//
// Liveness violations are reported as a prefix plus a cycle (a lasso
// shaped counterexample). Finite paths that simply end (deadlock) are
// NOT treated as permanent failures, and unreachable states never
// influence the verdicts.
package tcheck

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sort"
)

// Machine is a finite state machine with proposition-labelled states.
type Machine struct {
	Initial string
	props   map[string]map[string]bool // state -> proposition set
	edges   map[string][]string        // adjacency list, insertion ordered
	names   []string                   // all declared states, insertion ordered
}

type jsonState struct {
	Name  string   `json:"name"`
	Props []string `json:"props"`
}

type jsonMachine struct {
	Initial     string      `json:"initial"`
	States      []jsonState `json:"states"`
	Transitions [][2]string `json:"transitions"`
}

// Load parses a machine from a JSON document of the form:
//
//	{
//	  "initial": "s0",
//	  "states": [{"name": "s0", "props": ["idle"]}, ...],
//	  "transitions": [["s0", "s1"], ...]
//	}
func Load(data []byte) (*Machine, error) {
	var jm jsonMachine
	if err := json.Unmarshal(data, &jm); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	m := &Machine{
		Initial: jm.Initial,
		props:   make(map[string]map[string]bool),
		edges:   make(map[string][]string),
	}
	for _, s := range jm.States {
		if s.Name == "" {
			return nil, fmt.Errorf("state with empty name")
		}
		if _, dup := m.props[s.Name]; dup {
			return nil, fmt.Errorf("duplicate state %q", s.Name)
		}
		set := make(map[string]bool)
		for _, p := range s.Props {
			set[p] = true
		}
		m.props[s.Name] = set
		m.names = append(m.names, s.Name)
	}
	if _, ok := m.props[m.Initial]; !ok {
		return nil, fmt.Errorf("initial state %q is not declared", m.Initial)
	}
	for _, t := range jm.Transitions {
		from, to := t[0], t[1]
		if _, ok := m.props[from]; !ok {
			return nil, fmt.Errorf("transition from undeclared state %q", from)
		}
		if _, ok := m.props[to]; !ok {
			return nil, fmt.Errorf("transition to undeclared state %q", to)
		}
		m.edges[from] = append(m.edges[from], to)
	}
	return m, nil
}

// LoadFile reads and parses a machine description from a file.
func LoadFile(path string) (*Machine, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Load(data)
}

// Has reports whether the given proposition labels the state.
func (m *Machine) Has(state, prop string) bool {
	return m.props[state][prop]
}

// Successors returns the outgoing neighbours of a state.
func (m *Machine) Successors(state string) []string {
	return m.edges[state]
}

// States returns all declared state names in declaration order.
func (m *Machine) States() []string {
	out := make([]string, len(m.names))
	copy(out, m.names)
	return out
}

// Reachable returns the set of states reachable from the initial state.
func (m *Machine) Reachable() map[string]bool {
	seen := map[string]bool{m.Initial: true}
	queue := []string{m.Initial}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, t := range m.edges[s] {
			if !seen[t] {
				seen[t] = true
				queue = append(queue, t)
			}
		}
	}
	return seen
}

// Path is a sequence of states; a witness trace.
type Path []string

func (p Path) String() string {
	out := ""
	for i, s := range p {
		if i > 0 {
			out += " -> "
		}
		out += s
	}
	return out
}

// shortestPath finds a shortest path from src to dst, only visiting
// states for which allow returns true. Returns nil if unreachable.
// Predecessor ties are broken lexicographically for deterministic output.
func (m *Machine) shortestPath(src, dst string, allow func(string) bool) Path {
	if !allow(src) {
		return nil
	}
	prev := map[string]string{src: ""}
	visited := map[string]bool{src: true}
	queue := []string{src}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if s == dst {
			// Reconstruct.
			rev := []string{s}
			for prev[rev[len(rev)-1]] != "" {
				rev = append(rev, prev[rev[len(rev)-1]])
			}
			path := make(Path, len(rev))
			for i, v := range rev {
				path[len(rev)-1-i] = v
			}
			return path
		}
		succ := append([]string(nil), m.edges[s]...)
		sort.Strings(succ)
		for _, t := range succ {
			if !visited[t] && allow(t) {
				visited[t] = true
				prev[t] = s
				queue = append(queue, t)
			}
		}
	}
	return nil
}
