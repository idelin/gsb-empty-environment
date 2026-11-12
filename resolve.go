package taskpkg

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrUnsatisfiable means no candidate satisfies the combined incoming
	// constraints or a required node has no candidates.
	ErrUnsatisfiable = errors.New("taskpkg: unsatisfiable dependency")
	// ErrVerification means a candidate's injected proof check failed.
	ErrVerification = errors.New("taskpkg: proof verification failed")
	// ErrCycle means a dependency cycle was reached on the active edges.
	ErrCycle = errors.New("taskpkg: dependency cycle")
	// ErrInvalidInput means the graph or resolver configuration was malformed.
	ErrInvalidInput = errors.New("taskpkg: invalid input")
)

// ResolveError carries a typed cause together with contextual detail.
type ResolveError struct {
	Op     string
	Node   NodeID
	Cause  error
	Detail string
}

func (e *ResolveError) Error() string {
	var b strings.Builder
	b.WriteString(e.Cause.Error())
	if e.Op != "" {
		fmt.Fprintf(&b, " (%s", e.Op)
		if e.Node.Path != "" {
			fmt.Fprintf(&b, " node=%s", e.Node)
		}
		b.WriteString(")")
	}
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	return b.String()
}

// Unwrap exposes the sentinel cause for errors.Is.
func (e *ResolveError) Unwrap() error { return e.Cause }

// Options tunes a resolve.
type Options struct {
	// Platform selects which condition-gated edges are active.
	Platform PlatformConfig
	// AllowCycles turns cycle detection into a recorded warning instead of a
	// hard failure. Default behavior rejects cycles.
	AllowCycles bool
}

// Resolver performs admission-aware dependency resolution.
type Resolver struct {
	Graph    Graph
	Registry Registry
	Verifier Verifier
}

// Selection records the single candidate chosen for a reachable node together
// with every active edge that constrains it.
type Selection struct {
	Candidate Candidate
	Incoming  []Edge
}

// Resolution is the verified, fully constrained result of a resolve.
type Resolution struct {
	Platform PlatformConfig
	// Selected maps every reachable node to its chosen candidate.
	Selected map[NodeID]Selection
	// Order lists the selected nodes in DFS discovery order.
	Order []NodeID
	// Cycles reports any cycles observed when Options.AllowCycles is set.
	Cycles [][]NodeID
}

const (
	colorWhite = iota
	colorGray
	colorBlack
)

// Resolve runs a single independent resolution under opts.Platform.
func (r *Resolver) Resolve(opts Options) (*Resolution, error) {
	if r.Registry == nil {
		return nil, &ResolveError{Op: "resolve", Cause: ErrInvalidInput, Detail: "nil registry"}
	}
	if r.Verifier == nil {
		return nil, &ResolveError{Op: "resolve", Cause: ErrInvalidInput, Detail: "nil verifier"}
	}
	if err := r.Graph.Validate(); err != nil {
		return nil, &ResolveError{Op: "validate", Cause: ErrInvalidInput, Detail: err.Error()}
	}

	state := &resolveState{
		r:        r,
		opts:     opts,
		colors:   map[NodeID]int{},
		incoming: map[NodeID][]Edge{},
		order:    nil,
	}

	// Phase A: walk the active edges, collect every incoming edge per node and
	// detect cycles. All reachable nodes end up in state.incoming.
	if err := state.visit(r.Graph.Root, nil); err != nil {
		return nil, err
	}

	// Phase B: pick exactly one candidate per reachable node that satisfies the
	// intersection of all incoming constraints and passes proof verification.
	selected := make(map[NodeID]Selection, len(state.order))
	for _, node := range state.order {
		sel, err := r.selectCandidate(node, state.incoming[node])
		if err != nil {
			return nil, err
		}
		selected[node] = sel
	}

	return &Resolution{
		Platform: opts.Platform,
		Selected: selected,
		Order:    append([]NodeID(nil), state.order...),
		Cycles:   state.cycles,
	}, nil
}

type resolveState struct {
	r        *Resolver
	opts     Options
	colors   map[NodeID]int
	incoming map[NodeID][]Edge
	order    []NodeID
	stack    []NodeID
	cycles   [][]NodeID
}

func (s *resolveState) visit(node NodeID, via *Edge) error {
	if via != nil {
		s.incoming[node] = append(s.incoming[node], *via)
	} else {
		// Ensure the root is present even with no incoming edges.
		if _, ok := s.incoming[node]; !ok {
			s.incoming[node] = nil
		}
	}

	switch s.colors[node] {
	case colorGray:
		cycle := s.pathTo(node)
		if s.opts.AllowCycles {
			s.cycles = append(s.cycles, cycle)
			return nil
		}
		return &ResolveError{
			Op:     "cycle",
			Node:   node,
			Cause:  ErrCycle,
			Detail: "cycle: " + formatPath(cycle),
		}
	case colorBlack:
		return nil
	}

	s.colors[node] = colorGray
	s.order = append(s.order, node)
	s.stack = append(s.stack, node)

	for _, edge := range s.r.Graph.activeEdges(node, s.opts.Platform) {
		if err := s.visit(edge.To, &edge); err != nil {
			return err
		}
	}
	s.colors[node] = colorBlack
	s.stack = s.stack[:len(s.stack)-1]
	return nil
}

// pathTo reconstructs the active DFS stack ending at the gray node.
func (s *resolveState) pathTo(target NodeID) []NodeID {
	start := 0
	for i, node := range s.stack {
		if node == target {
			start = i
			break
		}
	}
	cycle := append([]NodeID(nil), s.stack[start:]...)
	return append(cycle, target)
}

func formatPath(nodes []NodeID) string {
	parts := make([]string, 0, len(nodes))
	for _, n := range nodes {
		parts = append(parts, n.String())
	}
	return strings.Join(parts, " -> ")
}

func (r *Resolver) selectCandidate(node NodeID, incoming []Edge) (Selection, error) {
	candidates, err := r.Registry.Candidates(node)
	if err != nil {
		return Selection{}, &ResolveError{
			Op: node.String(), Node: node, Cause: ErrUnsatisfiable, Detail: err.Error(),
		}
	}

	var verified bool
	for _, cand := range candidates {
		if cand.Node != node {
			continue
		}
		if !satisfiesAll(cand, incoming) {
			continue
		}
		if err := r.Verifier.Verify(cand); err != nil {
			// A candidate may fail verification while a lower version passes;
			// keep scanning, but remember that something was offered.
			verified = true
			continue
		}
		return Selection{Candidate: cand, Incoming: append([]Edge(nil), incoming...)}, nil
	}

	detail := "no candidate satisfies the intersection of incoming constraints"
	if verified {
		detail = "every otherwise admissible candidate failed proof verification"
		return Selection{}, &ResolveError{
			Op: node.String(), Node: node, Cause: ErrVerification, Detail: detail,
		}
	}
	return Selection{}, &ResolveError{
		Op: node.String(), Node: node, Cause: ErrUnsatisfiable, Detail: detail,
	}
}

// satisfiesAll enforces that every active incoming edge accepts the candidate.
// A node with no incoming edges (the root) has only its identity to satisfy.
func satisfiesAll(cand Candidate, incoming []Edge) bool {
	for _, edge := range incoming {
		if !edge.Constraint.accepts(cand) {
			return false
		}
	}
	return true
}
