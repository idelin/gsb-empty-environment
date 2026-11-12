package taskpkg_test

import (
	"errors"
	"testing"

	taskpkg "example.org/domainreissue/q005"
)

// TestCycle covers hard cycle rejection and the opt-in recorded-cycle mode.
func TestCycle(t *testing.T) {
	nodeA := taskpkg.NodeID{Source: srcInternal, Path: "example.net/acme/a"}
	nodeB := taskpkg.NodeID{Source: srcInternal, Path: "example.net/acme/b"}
	nodeC := taskpkg.NodeID{Source: srcInternal, Path: "example.net/acme/c"}

	cands := []taskpkg.Candidate{
		mkT(t, rootNode, "v1.0.0", signInternal, "dig-cyc-app"),
		mkT(t, nodeA, "v1.0.0", signInternal, "dig-cyc-a"),
		mkT(t, nodeB, "v1.0.0", signInternal, "dig-cyc-b"),
		mkT(t, nodeC, "v1.0.0", signInternal, "dig-cyc-c"),
	}
	registry := taskpkg.MapRegistry{Data: map[taskpkg.NodeID][]taskpkg.Candidate{
		rootNode: {cands[0]},
		nodeA:    {cands[1]},
		nodeB:    {cands[2]},
		nodeC:    {cands[3]},
	}}
	graph := taskpkg.Graph{
		Root: rootNode,
		Edges: []taskpkg.Edge{
			edge(t, rootNode, nodeA, "v1.0.0", "v1.0.0", signInternal),
			edge(t, nodeA, nodeB, "v1.0.0", "v1.0.0", signInternal),
			edge(t, nodeB, nodeC, "v1.0.0", "v1.0.0", signInternal),
			edge(t, nodeC, nodeA, "v1.0.0", "v1.0.0", signInternal),
		},
	}

	t.Run("rejected by default", func(t *testing.T) {
		r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifierFor(cands...)}
		_, err := r.Resolve(taskpkg.Options{})
		if !errors.Is(err, taskpkg.ErrCycle) {
			t.Fatalf("err = %v, want ErrCycle", err)
		}
	})

	t.Run("recorded when allowed", func(t *testing.T) {
		r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifierFor(cands...)}
		res, err := r.Resolve(taskpkg.Options{AllowCycles: true})
		if err != nil {
			t.Fatalf("resolve with AllowCycles: %v", err)
		}
		if len(res.Cycles) != 1 {
			t.Fatalf("cycles = %d, want 1", len(res.Cycles))
		}
		want := []taskpkg.NodeID{nodeA, nodeB, nodeC, nodeA}
		assertPath(t, res.Cycles[0], want)
		if len(res.Selected) != 4 {
			t.Fatalf("selected %d nodes with cycle allowed, want 4", len(res.Selected))
		}
	})

	t.Run("self loop is detected", func(t *testing.T) {
		selfGraph := taskpkg.Graph{
			Root: rootNode,
			Edges: []taskpkg.Edge{
				edge(t, rootNode, nodeA, "v1.0.0", "v1.0.0", signInternal),
				edge(t, nodeA, nodeA, "v1.0.0", "v1.0.0", signInternal),
			},
		}
		r := &taskpkg.Resolver{Graph: selfGraph, Registry: registry, Verifier: verifierFor(cands...)}
		_, err := r.Resolve(taskpkg.Options{})
		if !errors.Is(err, taskpkg.ErrCycle) {
			t.Fatalf("err = %v, want ErrCycle", err)
		}
	})
}
