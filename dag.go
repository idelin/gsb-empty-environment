package netsec

import (
	"encoding/json"
	"sort"
)

// NodeView is the JSON-friendly projection of one DAG node.
type NodeView struct {
	ID      TermID `json:"id"`
	Term    string `json:"term"`
	AtEvent int    `json:"at_event"`
	Known   bool   `json:"known"`
}

// EdgeView is the JSON-friendly projection of one derivation edge.
type EdgeView struct {
	Head     TermID   `json:"head"`
	HeadTerm string   `json:"head_term"`
	Rule     string   `json:"rule"`
	Deps     []TermID `json:"deps"`
	DepTerms []string `json:"dep_terms"`
	AtEvent  int      `json:"at_event"`
}

// DAG is the set of selected derivations reachable from a collection of
// roots (typically the registered targets).
type DAG struct {
	Roots []TermID   `json:"roots"`
	Nodes []NodeView `json:"nodes"`
	Edges []EdgeView `json:"edges"`
}

// DerivationDAG walks the selected best edges backwards from roots and
// returns every node and edge contributing to their derivation. Unknown
// roots appear as nodes without edges.
func (e *Engine) DerivationDAG(roots ...TermID) DAG {
	visited := make(map[TermID]bool)
	order := make([]TermID, 0)

	var walk func(id TermID)
	walk = func(id TermID) {
		if visited[id] {
			return
		}
		visited[id] = true
		if idx := e.bestEdge[id]; idx >= 0 {
			for _, dep := range e.edges[idx].deps {
				walk(dep)
			}
		}
		order = append(order, id)
	}

	for _, root := range roots {
		walk(root)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	dag := DAG{Roots: append([]TermID(nil), roots...)}
	for _, id := range order {
		at, known := e.Earliest(id)
		view := NodeView{
			ID:      id,
			Term:    e.TermString(id),
			AtEvent: at,
			Known:   known,
		}
		dag.Nodes = append(dag.Nodes, view)

		idx := e.bestEdge[id]
		if idx < 0 {
			continue
		}
		edge := &e.edges[idx]
		edgeView := EdgeView{
			Head:     id,
			HeadTerm: e.TermString(id),
			Rule:     edge.rule.String(),
			Deps:     append([]TermID(nil), edge.deps...),
			DepTerms: make([]string, len(edge.deps)),
			AtEvent:  at,
		}
		for i, dep := range edge.deps {
			edgeView.DepTerms[i] = e.TermString(dep)
		}
		dag.Edges = append(dag.Edges, edgeView)
	}

	sort.Slice(dag.Edges, func(i, j int) bool {
		if dag.Edges[i].Head != dag.Edges[j].Head {
			return dag.Edges[i].Head < dag.Edges[j].Head
		}
		return dag.Edges[i].Rule < dag.Edges[j].Rule
	})
	return dag
}

// TargetDAG returns the derivation DAG rooted at every registered target.
func (e *Engine) TargetDAG() DAG {
	return e.DerivationDAG(e.targets...)
}

// TargetResult reports the earliest knowability of one target.
type TargetResult struct {
	ID      TermID `json:"id"`
	Term    string `json:"term"`
	Known   bool   `json:"known"`
	AtEvent int    `json:"at_event,omitempty"`
	Rule    string `json:"rule,omitempty"`
}

// Report is the complete, JSON-serializable view of the engine state.
type Report struct {
	Events      int            `json:"event_count"`
	Facts       []Fact         `json:"facts"`
	Known       []KnownTerm    `json:"known_terms"`
	Targets     []TargetResult `json:"targets"`
	DAG         DAG            `json:"target_dag"`
	Consistent  bool           `json:"consistent"`
	CheckReason string         `json:"check_reason,omitempty"`
}

// Report assembles a host-facing snapshot, optionally running a fresh full
// recomputation check before serializing.
func (e *Engine) Report() Report {
	e.drain()
	check := e.Check()

	targets := make([]TargetResult, 0, len(e.targets))
	for _, id := range e.targets {
		result := TargetResult{
			ID:      id,
			Term:    e.TermString(id),
			AtEvent: e.earliest[id],
		}
		if e.earliest[id] != NoEvent {
			result.Known = true
			if idx := e.bestEdge[id]; idx >= 0 {
				result.Rule = e.edges[idx].rule.String()
			}
		} else {
			result.AtEvent = 0
		}
		targets = append(targets, result)
	}

	maxEvent := 0
	for _, fact := range e.facts {
		if fact.AtEvent > maxEvent {
			maxEvent = fact.AtEvent
		}
	}

	return Report{
		Events:      maxEvent,
		Facts:       e.Facts(),
		Known:       e.Known(),
		Targets:     targets,
		DAG:         e.TargetDAG(),
		Consistent:  check.OK,
		CheckReason: check.Reason,
	}
}

// JSON renders a Report as indented JSON for symbol-verification hosts that
// communicate through files or standard output.
func (e *Engine) JSON() ([]byte, error) {
	return json.MarshalIndent(e.Report(), "", "  ")
}

// DAGJSON renders an arbitrary DAG as indented JSON.
func (e *Engine) DAGJSON(roots ...TermID) ([]byte, error) {
	return json.MarshalIndent(e.DerivationDAG(roots...), "", "  ")
}
