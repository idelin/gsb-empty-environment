package taskpkg_test

import (
	"errors"
	"testing"

	taskpkg "example.org/domainreissue/q005"
)

func libNode(source taskpkg.SourceID) taskpkg.NodeID {
	return taskpkg.NodeID{Source: source, Path: libPath}
}

// TestClosedIntervalSignersAndProof covers interval boundaries, signer
// allow lists and injected-verifier failures in one reachable graph.
func TestClosedIntervalSignersAndProof(t *testing.T) {
	lib := libNode(srcInternal)

	rootCand := mkT(t, rootNode, "v1.0.0", signInternal, "dig-con-app")
	cands := []taskpkg.Candidate{
		mkT(t, lib, "v1.0.0", signInternal, "dig-con-1.0"),
		mkT(t, lib, "v1.5.0", signReleng, "dig-con-1.5"),
		mkT(t, lib, "v2.0.0", signInternal, "dig-con-2.0"),
		mkT(t, lib, "v2.1.0", signInternal, "dig-con-2.1"),
	}
	registry := taskpkg.MapRegistry{Data: map[taskpkg.NodeID][]taskpkg.Candidate{
		rootNode: {rootCand},
		lib:      cands,
	}}

	t.Run("inclusive bounds pick highest in interval", func(t *testing.T) {
		graph := taskpkg.Graph{
			Root:  rootNode,
			Edges: []taskpkg.Edge{edge(t, rootNode, lib, "v1.5.0", "v2.0.0", signInternal, signReleng)},
		}
		verifier := verifierFor(append([]taskpkg.Candidate{rootCand}, cands...)...)
		r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifier}
		res, err := r.Resolve(taskpkg.Options{})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		// v2.1.0 is above the max; v2.0.0 is the highest inclusive match.
		if got := res.Selected[lib].Candidate.Version.String(); got != "v2.0.0" {
			t.Fatalf("selected %s, want v2.0.0 at the inclusive upper bound", got)
		}
	})

	t.Run("signer not allowed falls through to lower admissible candidate", func(t *testing.T) {
		graph := taskpkg.Graph{
			Root:  rootNode,
			Edges: []taskpkg.Edge{edge(t, rootNode, lib, "v1.0.0", "v2.0.0", signReleng)},
		}
		verifier := verifierFor(append([]taskpkg.Candidate{rootCand}, cands...)...)
		r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifier}
		res, err := r.Resolve(taskpkg.Options{})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got := res.Selected[lib].Candidate.Version.String(); got != "v1.5.0" {
			t.Fatalf("selected %s, want the only releng-signed v1.5.0", got)
		}
	})

	t.Run("no candidate in interval is unsatisfiable", func(t *testing.T) {
		graph := taskpkg.Graph{
			Root:  rootNode,
			Edges: []taskpkg.Edge{edge(t, rootNode, lib, "v3.0.0", "v4.0.0", signInternal)},
		}
		verifier := verifierFor(append([]taskpkg.Candidate{rootCand}, cands...)...)
		r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifier}
		_, err := r.Resolve(taskpkg.Options{})
		if !errors.Is(err, taskpkg.ErrUnsatisfiable) {
			t.Fatalf("err = %v, want ErrUnsatisfiable", err)
		}
	})

	t.Run("failed proof verification surfaces ErrVerification", func(t *testing.T) {
		graph := taskpkg.Graph{
			Root:  rootNode,
			Edges: []taskpkg.Edge{edge(t, rootNode, lib, "v1.0.0", "v2.0.0", signInternal)},
		}
		// The verifier only trusts the root; every lib proof fails.
		verifier := verifierFor(rootCand)
		r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifier}
		_, err := r.Resolve(taskpkg.Options{})
		if !errors.Is(err, taskpkg.ErrVerification) {
			t.Fatalf("err = %v, want ErrVerification", err)
		}
	})

	t.Run("injected verifier can reject a spoofed signer", func(t *testing.T) {
		spoofed := mkT(t, lib, "v2.0.0", signInternal, "dig-con-spoof")
		spoofed.Proof.Payload = []byte("tampered")
		spoofedRegistry := taskpkg.MapRegistry{Data: map[taskpkg.NodeID][]taskpkg.Candidate{
			rootNode: {rootCand},
			lib: append(append([]taskpkg.Candidate{spoofed}, cands...),
				mkT(t, lib, "v2.0.0", signInternal, "dig-con-2.0")),
		}}
		verifier := taskpkg.VerifierFunc(func(c taskpkg.Candidate) error {
			if string(c.Proof.Payload) == "tampered" {
				return errors.New("bad signature")
			}
			return nil
		})
		graph := taskpkg.Graph{
			Root:  rootNode,
			Edges: []taskpkg.Edge{edge(t, rootNode, lib, "v1.0.0", "v2.0.0", signInternal)},
		}
		r := &taskpkg.Resolver{Graph: graph, Registry: spoofedRegistry, Verifier: verifier}
		res, err := r.Resolve(taskpkg.Options{})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		// The spoofed v2.0.0 ranks highest but fails verification; the genuine
		// v2.0.0 at the same version is then selected by digest tie-break order.
		if got := res.Selected[lib].Candidate.Digest; got != "dig-con-2.0" {
			t.Fatalf("selected %s, want genuine dig-con-2.0 after spoof rejected", got)
		}
	})

	t.Run("all proofs failing yields ErrVerification", func(t *testing.T) {
		rejectAll := taskpkg.VerifierFunc(func(taskpkg.Candidate) error {
			return errors.New("no trusted key")
		})
		graph := taskpkg.Graph{
			Root:  rootNode,
			Edges: []taskpkg.Edge{edge(t, rootNode, lib, "v1.0.0", "v2.0.0", signInternal)},
		}
		r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: rejectAll}
		_, err := r.Resolve(taskpkg.Options{})
		if !errors.Is(err, taskpkg.ErrVerification) {
			t.Fatalf("err = %v, want ErrVerification", err)
		}
	})
}

// TestIncomingIntersection ensures a node reached by two edges must satisfy
// both constraint sets simultaneously rather than either one.
func TestIncomingIntersection(t *testing.T) {
	lib := libNode(srcInternal)
	other := taskpkg.NodeID{Source: srcInternal, Path: "example.net/acme/other"}

	rootCand := mkT(t, rootNode, "v1.0.0", signInternal, "dig-int-app")
	otherCand := mkT(t, other, "v1.0.0", signInternal, "dig-int-other")
	libCands := []taskpkg.Candidate{
		mkT(t, lib, "v1.0.0", signInternal, "dig-int-1.0"),
		mkT(t, lib, "v1.4.0", signReleng, "dig-int-1.4"),
		mkT(t, lib, "v1.9.0", signInternal, "dig-int-1.9"),
	}
	registry := taskpkg.MapRegistry{Data: map[taskpkg.NodeID][]taskpkg.Candidate{
		rootNode: {rootCand},
		other:    {otherCand},
		lib:      libCands,
	}}
	all := append([]taskpkg.Candidate{rootCand, otherCand}, libCands...)

	// Root allows up to v1.9.0 internal; other requires the releng signer.
	// Only v1.4.0 satisfies both constraints.
	graph := taskpkg.Graph{
		Root: rootNode,
		Edges: []taskpkg.Edge{
			edge(t, rootNode, lib, "v1.0.0", "v1.9.0", signInternal, signReleng),
			edge(t, rootNode, other, "v1.0.0", "v1.0.0", signInternal),
			edge(t, other, lib, "v1.0.0", "v1.5.0", signReleng),
		},
	}
	r := &taskpkg.Resolver{Graph: graph, Registry: registry, Verifier: verifierFor(all...)}
	res, err := r.Resolve(taskpkg.Options{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	sel := res.Selected[lib]
	if got := sel.Candidate.Version.String(); got != "v1.4.0" {
		t.Fatalf("selected %s, want intersection-only v1.4.0", got)
	}
	if len(sel.Incoming) != 2 {
		t.Fatalf("incoming edges recorded = %d, want 2", len(sel.Incoming))
	}

	t.Run("disjoint constraints fail", func(t *testing.T) {
		badGraph := taskpkg.Graph{
			Root: rootNode,
			Edges: []taskpkg.Edge{
				edge(t, rootNode, lib, "v1.8.0", "v1.9.0", signInternal),
				edge(t, rootNode, other, "v1.0.0", "v1.0.0", signInternal),
				edge(t, other, lib, "v1.0.0", "v1.5.0", signReleng),
			},
		}
		rr := &taskpkg.Resolver{Graph: badGraph, Registry: registry, Verifier: verifierFor(all...)}
		_, err := rr.Resolve(taskpkg.Options{})
		if !errors.Is(err, taskpkg.ErrUnsatisfiable) {
			t.Fatalf("err = %v, want ErrUnsatisfiable for disjoint constraints", err)
		}
	})
}

// TestSemver covers prerelease precedence and closed-interval semantics.
func TestSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.1", -1},
		{"v1.0.0", "v1.0.0-rc.1", 1},
		{"v1.0.0-rc.1", "v1.0.0-rc.2", -1},
		{"v1.0.0-rc.2", "v1.0.0-rc.11", -1},
		{"v1.0.0-alpha", "v1.0.0-alpha.1", -1},
		{"v1.0.0-alpha", "v1.0.0-beta", -1},
		{"2.0.0", "v2.0.0+build.7", 0},
	}
	for _, tc := range cases {
		va, vb := mustV(t, tc.a), mustV(t, tc.b)
		if got := va.Compare(vb); got != tc.want {
			t.Fatalf("Compare(%s,%s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	min, max := mustV(t, "v1.0.0"), mustV(t, "v2.0.0")
	for _, in := range []string{"v1.0.0", "v1.5.0", "v2.0.0"} {
		if !mustV(t, in).InClosedInterval(min, max) {
			t.Fatalf("%s should be inside inclusive interval", in)
		}
	}
	for _, out := range []string{"v0.9.9", "v2.0.1"} {
		if mustV(t, out).InClosedInterval(min, max) {
			t.Fatalf("%s should be outside interval", out)
		}
	}
	if _, err := taskpkg.ParseVersion("v1.2"); err == nil {
		t.Fatalf("malformed version must error")
	}
}
