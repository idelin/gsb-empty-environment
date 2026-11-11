package netsec

import (
	"errors"
	"reflect"
	"testing"
)

func TestNewTrustGraphPartialOrder(t *testing.T) {
	graph, err := NewTrustGraph(
		[]SourceID{"instance", "task", "env", "isolated"},
		[]TrustEdge{
			{Higher: "env", Lower: "task"},
			{Higher: "task", Lower: "instance"},
			{Higher: "env", Lower: "instance"},
		},
	)
	if err != nil {
		t.Fatalf("NewTrustGraph returned error: %v", err)
	}

	if !graph.Dominates("env", "instance") {
		t.Fatal("expected transitive dominance env -> instance")
	}
	if graph.Dominates("instance", "env") {
		t.Fatal("lower source must not dominate higher source")
	}
	if graph.Dominates("env", "env") {
		t.Fatal("a source must not dominate itself")
	}

	cases := []struct {
		name string
		a, b SourceID
		want Order
	}{
		{"self", "task", "task", Equal},
		{"direct", "env", "task", MoreTrusted},
		{"reverse direct", "task", "env", LessTrusted},
		{"transitive", "env", "instance", MoreTrusted},
		{"isolated", "isolated", "env", Incomparable},
		{"unknown", "env", "ghost", Incomparable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := graph.Compare(tc.a, tc.b); got != tc.want {
				t.Fatalf("Compare(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}

	// A direct env -> instance edge exists, so the shortest proof is the
	// direct edge rather than the two-hop chain through task.
	wantPath := []SourceID{"env", "instance"}
	if got := graph.Path("env", "instance"); !reflect.DeepEqual(got, wantPath) {
		t.Fatalf("Path(env, instance) = %v, want %v", got, wantPath)
	}
	if got := graph.Path("instance", "env"); got != nil {
		t.Fatalf("reverse Path = %v, want nil", got)
	}
	if got := graph.Path("isolated", "env"); got != nil {
		t.Fatalf("incomparable Path = %v, want nil", got)
	}

	wantNodes := []SourceID{"env", "instance", "isolated", "task"}
	if got := graph.Nodes(); !reflect.DeepEqual(got, wantNodes) {
		t.Fatalf("Nodes() = %v, want %v", got, wantNodes)
	}
}

func TestNewTrustGraphRejectsInvalidInputs(t *testing.T) {
	t.Run("empty node", func(t *testing.T) {
		if _, err := NewTrustGraph([]SourceID{""}, nil); !errors.Is(err, ErrEmptySource) {
			t.Fatalf("err = %v, want ErrEmptySource", err)
		}
	})
	t.Run("unknown endpoint", func(t *testing.T) {
		_, err := NewTrustGraph(
			[]SourceID{"a"},
			[]TrustEdge{{Higher: "a", Lower: "b"}},
		)
		if err == nil {
			t.Fatal("edge referencing an unknown source must be rejected")
		}
	})
	t.Run("self edge", func(t *testing.T) {
		_, err := NewTrustGraph(
			[]SourceID{"a"},
			[]TrustEdge{{Higher: "a", Lower: "a"}},
		)
		if err == nil {
			t.Fatal("self edge must be rejected")
		}
	})
	t.Run("cycle", func(t *testing.T) {
		_, err := NewTrustGraph(
			[]SourceID{"a", "b", "c"},
			[]TrustEdge{
				{Higher: "a", Lower: "b"},
				{Higher: "b", Lower: "c"},
				{Higher: "c", Lower: "a"},
			},
		)
		if !errors.Is(err, ErrTrustCycle) {
			t.Fatalf("err = %v, want ErrTrustCycle", err)
		}
	})
}
