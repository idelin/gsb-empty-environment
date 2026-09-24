package canonicalkey

import (
	"errors"
	"testing"
)

func cyclicGraph() Graph {
	nodes := map[string]Value{
		"root": Object{
			"self":   Ref{"root"},
			"first":  Ref{"shared"},
			"second": Ref{"shared"},
		},
		"shared": Object{"leaf": Ref{"leaf"}},
		"leaf":   "done",
	}
	return Graph{Root: Ref{"root"}, Nodes: nodes}
}

func TestCyclicSharedGraphCanonicalizesDeterministically(t *testing.T) {
	request := Request{"tenant-a", "search", PresentString("cfg-3"), PresentParams(cyclicGraph())}
	left, err := CanonicalRequest(request, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	request.Params = PresentParams(cyclicGraph())
	right, err := CanonicalRequest(request, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if string(left) != string(right) {
		t.Fatalf("graph canonicalization was not deterministic:\n%s\n%s", left, right)
	}
}

func TestGraphReferenceIdentityIsExplicit(t *testing.T) {
	original := cyclicGraph()
	renamed := Graph{
		Root: Ref{"r"},
		Nodes: map[string]Value{
			"r": Object{"self": Ref{"r"}, "first": Ref{"s"}, "second": Ref{"s"}},
			"s": Object{"leaf": Ref{"l"}},
			"l": "done",
		},
	}
	request := Request{"tenant-a", "search", PresentString("cfg-3"), AbsentParams()}
	request.Params = PresentParams(original)
	originalKey, err := CanonicalRequest(request, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	request.Params = PresentParams(renamed)
	renamedKey, err := CanonicalRequest(request, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if string(originalKey) == string(renamedKey) {
		t.Fatal("graphs with different node reference identities collided")
	}

	request.Params = PresentParams(Ref{"missing"})
	_, err = CanonicalRequest(request, DefaultLimits())
	if !errors.Is(err, ErrDanglingReference) {
		t.Fatalf("dangling ref error = %v, want %v", err, ErrDanglingReference)
	}

	request.Params = PresentParams(Graph{
		Root: Ref{"outer"},
		Nodes: map[string]Value{
			"outer": Graph{
				Root: Ref{"inner"},
				Nodes: map[string]Value{
					"inner": Ref{"outer"},
				},
			},
		},
	})
	_, err = CanonicalRequest(request, DefaultLimits())
	if !errors.Is(err, ErrDanglingReference) {
		t.Fatalf("inner graph incorrectly resolved outer reference: %v", err)
	}
}
