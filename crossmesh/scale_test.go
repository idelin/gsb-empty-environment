package crossmesh

import (
	"math/rand"
	"testing"
)

// TestMaxNodeScale builds the full supported graph with diamonds, shared
// sub-chains and back edges, then performs a CAS revocation. The solver must
// finish quickly without any path enumeration.
func TestMaxNodeScale(t *testing.T) {
	const n = MaxNodes
	nodes := make([]string, n)
	for i := 0; i < n; i++ {
		nodes[i] = "n" + itoa(i)
	}

	edges := make([]DelegationEdge, 0, 3*n)
	edges = append(edges, edge("e-root", nodes[0], nodes[1],
		[]Name{dom("example.com")}, nil,
		[]string{"a0", "a1"}, n, 0, 1_000))
	for i := 1; i < n-1; i++ {
		edges = append(edges,
			edge("e-chain-"+itoa(i), nodes[i], nodes[i+1],
				[]Name{dom("example.com")}, nil,
				[]string{"a0", "a1"}, n-i, 0, 1_000),
			edge("e-skip-"+itoa(i), nodes[i], nodes[(i+2)%(n-1)+1],
				[]Name{dom("example.com"), dom("example.org")},
				nil, []string{"a0"}, n, 0, 1_000),
		)
	}
	// Back edges create rootless loops and diamond reconvergence.
	edges = append(edges,
		edge("e-back-1", nodes[n-1], nodes[2], []Name{dom("example.com")},
			nil, []string{"a1"}, 0, 0, 1_000),
		edge("e-back-2", nodes[n-2], nodes[1], []Name{dom("example.com")},
			nil, []string{"a1"}, 0, 0, 1_000),
	)

	g := Graph{Nodes: nodes, Edges: edges, TrustedRoots: []string{nodes[0]}}
	eng := mustBuild(t, g)

	vs, err := eng.AuthorizedVoxels(nodes[n-1])
	if err != nil {
		t.Fatalf("AuthorizedVoxels: %v", err)
	}
	if vs.isEmpty() {
		t.Fatal("last node should be reachable through the main chain")
	}

	report, err := eng.Revoke([]Revocation{
		{EdgeID: "e-root", ExpectedDigest: eng.Digest()},
	})
	if err != nil || !report.Applied {
		t.Fatalf("scale revoke: applied=%v err=%v", report.Applied, err)
	}
	for _, n := range nodes[1:] {
		got, err := eng.IsAuthorized(n, dom("x.example.com"), "a0", 500)
		if err != nil {
			t.Fatalf("post-revoke IsAuthorized: %v", err)
		}
		if got {
			t.Fatalf("node %s retained authority after root edge removal", n)
		}
	}
}

// TestRandomSmallGraphEquivalence runs many small randomized graphs through
// both the polynomial engine and the exhaustive independent acceptor. Every
// node's solved voxel set must match exactly.
func TestRandomSmallGraphEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(20261127))
	scopes := []Name{
		dom("example.com"),
		dom("a.example.com"),
		dom("b.example.com"),
		uri("https://host/p"),
	}
	excludes := []Name{dom("secret.example.com")}
	actions := []string{"read", "write", "admin"}

	for iter := 0; iter < 200; iter++ {
		n := 4 + rng.Intn(6) // 4..9 nodes, inside acceptor cap
		nodes := make([]string, n)
		for i := range nodes {
			nodes[i] = "n" + itoa(i)
		}
		rootCount := 1 + rng.Intn(2)
		roots := append([]string(nil), nodes[:rootCount]...)

		var ed []DelegationEdge
		edgeCount := n + rng.Intn(2*n)
		for k := 0; k < edgeCount; k++ {
			from := rng.Intn(n)
			to := rng.Intn(n - 1)
			if to >= from {
				to++
			}
			inc := []Name{scopes[rng.Intn(len(scopes))]}
			var exc []Name
			if rng.Intn(4) == 0 {
				exc = append(exc, excludes...)
			}
			acts := []string{actions[rng.Intn(len(actions))]}
			if rng.Intn(2) == 0 {
				acts = append(acts, actions[rng.Intn(len(actions))])
			}
			nbf := int64(rng.Intn(3) * 10)
			naf := nbf + int64(10+rng.Intn(30))
			ed = append(ed, edge("e"+itoa(k), nodes[from], nodes[to],
				inc, exc, acts, rng.Intn(4), nbf, naf))
		}

		eng, err := New(Graph{Nodes: nodes, Edges: ed, TrustedRoots: roots})
		if err != nil {
			t.Fatalf("iter %d: New: %v", iter, err)
		}
		acc, err := NewIndependentAcceptor(eng)
		if err != nil {
			t.Fatalf("iter %d: acceptor: %v", iter, err)
		}
		for _, node := range nodes {
			ok, err := acc.Accept(node)
			if err != nil {
				t.Fatalf("iter %d Accept(%s): %v", iter, node, err)
			}
			if !ok {
				t.Fatalf("iter %d: engine/acceptor mismatch on %s", iter, node)
			}
		}
	}
}
