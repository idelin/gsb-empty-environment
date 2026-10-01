// Package eval evaluates actual telemetry spans against a declared
// expected span structure and reports missing, duplicate, unclosed,
// misplaced and unexpected spans together with a public score.
package eval

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

// Node kinds in the expected structure graph.
const (
	KindSpan   = "span"   // a concrete expected span occurrence rule
	KindAll    = "all"    // all children are expected
	KindChoice = "choice" // exactly one child branch is expected (mutually exclusive)
)

// Node is one rule in the expected span structure graph. The graph is a
// finite tree: span nodes carry a name and a repeat range [Min, Max],
// choice nodes encode mutually exclusive branches, and When conditions
// prune nodes that do not apply to the current trace attributes.
type Node struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	Name     string            `json:"name,omitempty"`
	Min      int               `json:"min,omitempty"`
	Max      int               `json:"max,omitempty"`
	When     map[string]string `json:"when,omitempty"`
	Children []*Node           `json:"children,omitempty"`
}

// Event is a locally synthesized telemetry event replayed from a fixture.
type Event struct {
	Type   string            `json:"type"` // "start" | "end"
	ID     string            `json:"id"`
	Name   string            `json:"name,omitempty"`
	Parent string            `json:"parent,omitempty"`
	Ts     int64             `json:"ts"`
	Attrs  map[string]string `json:"attrs,omitempty"`
}

// Fixture declares the expected structure plus a replayable event stream.
// EvaluateAfter splits the stream: events [0, EvaluateAfter) are evaluated
// first, the rest arrive "late" and trigger score revisions.
type Fixture struct {
	Name          string            `json:"name"`
	TraceAttrs    map[string]string `json:"trace_attrs"`
	Expected      *Node             `json:"expected"`
	Events        []Event           `json:"events"`
	EvaluateAfter int               `json:"evaluate_after,omitempty"`
	Budget        int               `json:"budget,omitempty"`
}

// LoadFixture reads and validates a fixture file.
func LoadFixture(path string) (*Fixture, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f Fixture
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %v", path, err)
	}
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("validate %s: %v", path, err)
	}
	return &f, nil
}

// Validate normalizes defaults and checks the expected structure graph.
func (f *Fixture) Validate() error {
	if f.Expected == nil {
		return fmt.Errorf("missing expected structure")
	}
	if err := f.Expected.normalize("root", map[string]bool{}); err != nil {
		return err
	}
	for i, ev := range f.Events {
		if ev.Type != "start" && ev.Type != "end" {
			return fmt.Errorf("event %d: unknown type %q", i, ev.Type)
		}
		if ev.ID == "" {
			return fmt.Errorf("event %d: missing span id", i)
		}
	}
	return nil
}

func (n *Node) normalize(path string, seen map[string]bool) error {
	if n.ID == "" {
		n.ID = path
	}
	if seen[n.ID] {
		return fmt.Errorf("duplicate node id %q", n.ID)
	}
	seen[n.ID] = true
	switch n.Kind {
	case KindSpan:
		if n.Name == "" {
			return fmt.Errorf("node %s: span kind requires a name", n.ID)
		}
		if n.Max == 0 {
			if n.Min == 0 {
				n.Min = 1
			}
			n.Max = n.Min
		}
		if n.Min < 0 || n.Max < 0 || n.Min > n.Max {
			return fmt.Errorf("node %s: invalid repeat range [%d,%d]", n.ID, n.Min, n.Max)
		}
	case KindAll:
		if len(n.Children) == 0 {
			return fmt.Errorf("node %s: all kind requires children", n.ID)
		}
	case KindChoice:
		if len(n.Children) == 0 {
			return fmt.Errorf("node %s: choice kind requires branches", n.ID)
		}
	default:
		return fmt.Errorf("node %s: unknown kind %q", n.ID, n.Kind)
	}
	for i, c := range n.Children {
		if err := c.normalize(fmt.Sprintf("%s/%d", n.ID, i), seen); err != nil {
			return err
		}
	}
	return nil
}
