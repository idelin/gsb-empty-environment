package taskpkg_test

import (
	"testing"

	taskpkg "example.org/domainreissue/q005"
)

// TestSourceAlias verifies that an alias source hosting the same path is a
// separate node and that edges pinning the original source cannot consume it.
func TestSourceAlias(t *testing.T) {
	internalLib := taskpkg.NodeID{Source: srcInternal, Path: libPath}
	aliasLib := taskpkg.NodeID{Source: srcAlias, Path: libPath}

	rootCand := mkT(t, rootNode, "v1.0.0", signInternal, "dig-app-alias")
	internalCand := mkT(t, internalLib, "v1.4.0", signInternal, "dig-alias-internal")
	aliasCand := mkT(t, aliasLib, "v1.4.0", signPublic, "dig-alias-public")

	registry := taskpkg.MapRegistry{Data: map[taskpkg.NodeID][]taskpkg.Candidate{
		rootNode:    {rootCand},
		internalLib: {internalCand},
		aliasLib:    {aliasCand},
	}}
	verifier := verifierFor(rootCand, internalCand, aliasCand)

	t.Run("pinned source rejects alias node", func(t *testing.T) {
		graph := taskpkg.Graph{
			Root:  rootNode,
			Edges: []taskpkg.Edge{edge(t, rootNode, internalLib, "v1.0.0", "v2.0.0", signInternal)},
		}
		r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifier}
		res, err := r.Resolve(taskpkg.Options{})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if _, ok := res.Selected[aliasLib]; ok {
			t.Fatalf("alias node must not be selected by an internal-pinned edge")
		}
		if res.Selected[internalLib].Candidate.Digest != internalCand.Digest {
			t.Fatalf("internal candidate not selected")
		}
	})

	t.Run("explicit alias edge stays isolated", func(t *testing.T) {
		graph := taskpkg.Graph{
			Root: rootNode,
			Edges: []taskpkg.Edge{
				edge(t, rootNode, internalLib, "v1.0.0", "v2.0.0", signInternal),
				edge(t, rootNode, aliasLib, "v1.0.0", "v2.0.0", signPublic),
			},
		}
		r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifier}
		res, err := r.Resolve(taskpkg.Options{})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if len(res.Selected) != 3 {
			t.Fatalf("selected %d nodes, want root + two distinct same-path nodes", len(res.Selected))
		}
		if res.Selected[internalLib].Candidate.Node == res.Selected[aliasLib].Candidate.Node {
			t.Fatalf("same-name nodes from different sources merged")
		}
	})

	t.Run("edge pinning mismatched source is rejected at validation", func(t *testing.T) {
		bad := edge(t, rootNode, internalLib, "v1.0.0", "v2.0.0", signInternal)
		bad.Constraint.TrustedSource = srcAlias
		graph := taskpkg.Graph{Root: rootNode, Edges: []taskpkg.Edge{bad}}
		r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifier}
		_, err := r.Resolve(taskpkg.Options{})
		if err == nil {
			t.Fatalf("expected validation error for source/edge mismatch")
		}
	})
}
