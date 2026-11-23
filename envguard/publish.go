package envguard

import (
	"fmt"
	"strings"
)

// Export declares how one named environment value is published.
type Export struct {
	// Name is the environment variable name exposed to the service.
	Name string
	// Value is the rope whose output is exported.
	Value NodeID
	// IsPath enables POSIX segment normalization and approved-prefix proof.
	IsPath bool
	// ApprovedPrefixes are pre-normalized, trusted path prefixes. Only
	// meaningful when IsPath is set.
	ApprovedPrefixes []string
}

// Publisher runs the three independent export constraints over a graph.
type Publisher struct {
	graph *Graph
}

// NewPublisher binds checks to a graph.
func NewPublisher(g *Graph) *Publisher { return &Publisher{graph: g} }

// Check verifies content origin, control origin, and (for path exports) the
// approved-prefix constraint. The earliest violation across all three is
// returned; the kinds are distinct so callers can tell which constraint
// rejected the value.
func (p *Publisher) Check(ex Export) (*Violation, error) {
	g := p.graph
	if int(ex.Value) <= 0 || int(ex.Value) >= len(g.nodes) {
		return nil, fmt.Errorf("envguard: export %q references unknown node", ex.Name)
	}
	content := g.checkContent(ex.Value)
	control := g.checkControl(ex.Value)
	var path *Violation
	if ex.IsPath {
		path = g.checkPath(ex.Value, ex.ApprovedPrefixes)
	}
	return earliest(content, control, path), nil
}

func earliest(vs ...*Violation) *Violation {
	var best *Violation
	for _, v := range vs {
		if v == nil {
			continue
		}
		if best == nil || v.From < best.From || (v.From == best.From && v.Kind < best.Kind) {
			best = v
		}
	}
	return best
}

// Env is the materialized view the Go launcher hands to the service.
type Env struct {
	values map[string]string
}

// Build validates every export and materializes accepted values. Materializing
// happens only after checks pass, so rejected ropes are never expanded.
func (p *Publisher) Build(exports []Export) (*Env, error) {
	env := &Env{values: map[string]string{}}
	for _, ex := range exports {
		v, err := p.Check(ex)
		if err != nil {
			return nil, err
		}
		if v != nil {
			return nil, &RejectError{
				Export:    ex.Name,
				Violation: v,
			}
		}
		env.values[ex.Name] = materialize(p.graph, ex.Value)
	}
	return env, nil
}

// RejectError carries the earliest unapproved output interval.
type RejectError struct {
	Export    string
	Violation *Violation
}

func (e *RejectError) Error() string {
	return fmt.Sprintf(
		"envguard: export %q rejected by %s constraint at [%d,%d)",
		e.Export, e.Violation.Kind, e.Violation.From, e.Violation.To,
	)
}

// Preview renders the first runes of the offending interval for diagnostics,
// capped to keep messages small.
func (v *Violation) Preview(g *Graph, id NodeID) string { return v.explain(g, id) }

func materialize(g *Graph, id NodeID) string {
	var b strings.Builder
	n := g.nodes[id].len
	for i := int64(0); i < n; i++ {
		b.WriteRune(g.RuneAt(id, i))
	}
	return b.String()
}

// Get returns a built environment value.
func (e *Env) Get(name string) (string, bool) {
	v, ok := e.values[name]
	return v, ok
}

// Names returns the exported variable names in sorted-independent order.
func (e *Env) Names() []string {
	out := make([]string, 0, len(e.values))
	for k := range e.values {
		out = append(out, k)
	}
	return out
}
