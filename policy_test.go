package taskpkg_test

import (
	"errors"
	"testing"

	taskpkg "example.org/domainreissue/q005"
)

// TestUnauthorizedRoot ensures the host policy also flags an untrusted root
// candidate and that the audit path for the root is a single-node path.
func TestUnauthorizedRoot(t *testing.T) {
	rootCand := mkT(t, rootNode, "v1.0.0", signPublic, "dig-root-public")
	registry := taskpkg.MapRegistry{Data: map[taskpkg.NodeID][]taskpkg.Candidate{
		rootNode: {rootCand},
	}}
	graph := taskpkg.Graph{Root: rootNode}
	r := &taskpkg.Resolver{
		Graph:    graph,
		Registry: registry,
		Verifier: verifierFor(rootCand),
	}
	policy := taskpkg.NewPolicy(
		[]taskpkg.SourceID{srcInternal},
		[]taskpkg.SignerID{signInternal},
	)
	res, err := r.Resolve(taskpkg.Options{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ok, reason := policy.Authorized(res.Selected[rootNode].Candidate)
	if ok || reason != taskpkg.ReasonUntrustedSigner {
		t.Fatalf("root should be unauthorized via signer, got ok=%v reason=%q", ok, reason)
	}

	report, err := r.Enumerate(taskpkg.AuditConfig{Bits: 1}, policy)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if report.Minimal == nil {
		t.Fatalf("expected root finding")
	}
	assertPath(t, report.Minimal.Path.Nodes, []taskpkg.NodeID{rootNode})
}

// TestInputValidation locks down malformed resolver and graph inputs.
func TestInputValidation(t *testing.T) {
	lib := libNode(srcInternal)
	cand := mkT(t, lib, "v1.0.0", signInternal, "dig-val")
	registry := taskpkg.MapRegistry{Data: map[taskpkg.NodeID][]taskpkg.Candidate{lib: {cand}}}

	t.Run("nil registry and verifier", func(t *testing.T) {
		r := &taskpkg.Resolver{Graph: taskpkg.Graph{Root: rootNode}}
		if _, err := r.Resolve(taskpkg.Options{}); !errors.Is(err, taskpkg.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
		r.Registry = registry
		if _, err := r.Resolve(taskpkg.Options{}); !errors.Is(err, taskpkg.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput for nil verifier", err)
		}
	})

	t.Run("empty signer set and inverted interval", func(t *testing.T) {
		bad := edge(t, rootNode, lib, "v2.0.0", "v1.0.0")
		bad.Constraint.AllowedSigners = nil
		r := &taskpkg.Resolver{
			Graph:    taskpkg.Graph{Root: rootNode, Edges: []taskpkg.Edge{bad}},
			Registry: registry,
			Verifier: verifierFor(cand),
		}
		_, err := r.Resolve(taskpkg.Options{})
		if !errors.Is(err, taskpkg.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("missing node in registry", func(t *testing.T) {
		graph := taskpkg.Graph{
			Root:  rootNode,
			Edges: []taskpkg.Edge{edge(t, rootNode, lib, "v1.0.0", "v1.0.0", signInternal)},
		}
		r := &taskpkg.Resolver{
			Graph:    graph,
			Registry: taskpkg.MapRegistry{Data: map[taskpkg.NodeID][]taskpkg.Candidate{}},
			Verifier: verifierFor(cand),
		}
		_, err := r.Resolve(taskpkg.Options{})
		if !errors.Is(err, taskpkg.ErrUnsatisfiable) {
			t.Fatalf("err = %v, want ErrUnsatisfiable", err)
		}
	})
}
