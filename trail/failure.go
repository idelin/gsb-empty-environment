package trail

import (
	"fmt"
	"strings"
)

// FailureKind classifies why no Eulerian trail exists.
type FailureKind int

const (
	// FailureDegree means the in/out degree balance forbids any trail
	// that uses every edge exactly once from the requested start.
	FailureDegree FailureKind = iota
	// FailureConnectivity means some edges cannot be reached from the
	// start node in the underlying undirected graph.
	FailureConnectivity
)

func (k FailureKind) String() string {
	switch k {
	case FailureDegree:
		return "degree"
	case FailureConnectivity:
		return "connectivity"
	}
	return "unknown"
}

// NodeImbalance records a node whose in/out degrees break the Eulerian
// trail condition.
type NodeImbalance struct {
	Node string
	In   int
	Out  int
}

// Failure describes why a route cannot be built. It satisfies error.
type Failure struct {
	Kind FailureKind
	// Nodes lists imbalanced nodes; populated for FailureDegree.
	Nodes []NodeImbalance
	// EdgeIDs lists edges unreachable from the start; populated for
	// FailureConnectivity, sorted ascending.
	EdgeIDs []string
}

func (f *Failure) Error() string {
	switch f.Kind {
	case FailureDegree:
		parts := make([]string, 0, len(f.Nodes))
		for _, n := range f.Nodes {
			parts = append(parts, fmt.Sprintf("%s(in=%d,out=%d)", n.Node, n.In, n.Out))
		}
		return fmt.Sprintf("trail: degree imbalance forbids a route: %s", strings.Join(parts, ", "))
	case FailureConnectivity:
		return fmt.Sprintf("trail: %d edge(s) unreachable from start: %s",
			len(f.EdgeIDs), strings.Join(f.EdgeIDs, ", "))
	}
	return "trail: unknown failure"
}

// DuplicateEdgeIDError reports a repeated edge ID.
type DuplicateEdgeIDError struct {
	ID string
}

func (e *DuplicateEdgeIDError) Error() string {
	return fmt.Sprintf("trail: duplicate edge id %q", e.ID)
}
