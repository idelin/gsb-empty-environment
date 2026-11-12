package taskpkg_test

import (
	"testing"

	taskpkg "example.org/domainreissue/q005"
)

// TestSameNameHigherVersion covers the dependency-confusion scenario: a public
// mirror publishes a higher version of an internal package path. A correctly
// pinned edge must still select the internal release, and the two sources must
// remain distinct nodes rather than merging by package name.
func TestSameNameHigherVersion(t *testing.T) {
	internalLib := taskpkg.NodeID{Source: srcInternal, Path: libPath}
	publicLib := taskpkg.NodeID{Source: srcPublic, Path: libPath}

	rootCand := mkT(t, rootNode, "v1.0.0", signInternal, "dig-app-1")
	internalCand := mkT(t, internalLib, "v1.2.0", signInternal, "dig-lib-internal-1.2")
	internalOld := mkT(t, internalLib, "v1.1.0", signInternal, "dig-lib-internal-1.1")
	publicHigh := mkT(t, publicLib, "v9.9.9", signPublic, "dig-lib-public-9.9.9")
	publicLow := mkT(t, publicLib, "v1.0.0", signPublic, "dig-lib-public-1")

	registry := taskpkg.MapRegistry{Data: map[taskpkg.NodeID][]taskpkg.Candidate{
		rootNode:    {rootCand},
		internalLib: {internalCand, internalOld},
		publicLib:   {publicHigh, publicLow},
	}}
	verifier := verifierFor(rootCand, internalCand, internalOld, publicHigh, publicLow)

	t.Run("pinned edge ignores higher public version", func(t *testing.T) {
		graph := taskpkg.Graph{
			Root:  rootNode,
			Edges: []taskpkg.Edge{edge(t, rootNode, internalLib, "v1.0.0", "v2.0.0", signInternal)},
		}
		r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifier}
		res, err := r.Resolve(taskpkg.Options{})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		sel, ok := res.Selected[internalLib]
		if !ok {
			t.Fatalf("internal node not selected")
		}
		if got := sel.Candidate.Version.String(); got != "v1.2.0" {
			t.Fatalf("selected %s, want internal v1.2.0", got)
		}
		if _, leaked := res.Selected[publicLib]; leaked {
			t.Fatalf("public same-name node must never merge into the internal node")
		}
	})

	t.Run("misconfigured edge admits unauthorized public high version", func(t *testing.T) {
		graph := taskpkg.Graph{
			Root:  rootNode,
			Edges: []taskpkg.Edge{edge(t, rootNode, publicLib, "v1.0.0", "v10.0.0", signPublic)},
		}
		r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifier}
		policy := taskpkg.NewPolicy(
			[]taskpkg.SourceID{srcInternal},
			[]taskpkg.SignerID{signInternal, signReleng},
		)

		res, err := r.Resolve(taskpkg.Options{})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if sel := res.Selected[publicLib]; sel.Candidate.Digest != publicHigh.Digest {
			t.Fatalf("selected %s, want the highest public candidate", sel.Candidate.Digest)
		}

		report, err := r.Enumerate(taskpkg.AuditConfig{Bits: 1}, policy)
		if err != nil {
			t.Fatalf("enumerate: %v", err)
		}
		if report.Minimal == nil {
			t.Fatalf("expected an unauthorized finding")
		}
		if report.Minimal.Popcount != 0 || report.Minimal.Platform != 0 {
			t.Fatalf("minimal = %s popcount %d, want zero-bit config",
				report.Minimal.Platform, report.Minimal.Popcount)
		}
		if report.Minimal.Reason != taskpkg.ReasonUntrustedSource {
			t.Fatalf("reason = %q, want %q", report.Minimal.Reason, taskpkg.ReasonUntrustedSource)
		}
		wantPath := []taskpkg.NodeID{rootNode, publicLib}
		assertPath(t, report.Minimal.Path.Nodes, wantPath)
	})
}

func assertPath(t *testing.T, got, want []taskpkg.NodeID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("path = %v, want %v", got, want)
	}
	for i, n := range want {
		if got[i] != n {
			t.Fatalf("path[%d] = %v, want %v", i, got[i], n)
		}
	}
}
