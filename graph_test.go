package netsec

import (
	"context"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name  string
		graph func() *Graph
		want  string
	}{
		{
			name:  "nil graph",
			graph: func() *Graph { return nil },
			want:  "nil graph",
		},
		{
			name:  "no nodes",
			graph: func() *Graph { return &Graph{} },
			want:  "no nodes",
		},
		{
			name: "no roots",
			graph: func() *Graph {
				return &Graph{Nodes: []Node{{ID: "t", Kind: KindTable}}}
			},
			want: "no roots",
		},
		{
			name: "empty node id",
			graph: func() *Graph {
				return &Graph{
					Nodes: []Node{{ID: "", Kind: KindTable}},
					Roots: []Root{{ID: "", Label: RootSandbox}},
				}
			},
			want: "empty ID",
		},
		{
			name: "duplicate node",
			graph: func() *Graph {
				return &Graph{
					Nodes: []Node{
						{ID: "t", Kind: KindTable},
						{ID: "t", Kind: KindTable},
					},
					Roots: []Root{{ID: "t", Label: RootSandbox}},
				}
			},
			want: "duplicate node ID",
		},
		{
			name: "edge unknown source",
			graph: func() *Graph {
				return &Graph{
					Nodes: []Node{{ID: "t", Kind: KindTable}},
					Edges: []Edge{{From: "ghost", To: "t", Kind: EdgeField, Key: "x"}},
					Roots: []Root{{ID: "t", Label: RootSandbox}},
				}
			},
			want: "source",
		},
		{
			name: "root unknown node",
			graph: func() *Graph {
				return &Graph{
					Nodes: []Node{{ID: "t", Kind: KindTable}},
					Roots: []Root{{ID: "ghost", Label: RootSandbox}},
				}
			},
			want: "references unknown node",
		},
		{
			name: "upvalue without index",
			graph: func() *Graph {
				return &Graph{
					Nodes: []Node{
						{ID: "fn", Kind: KindFunction},
						{ID: "t", Kind: KindTable},
					},
					Edges: []Edge{{From: "fn", To: "t", Kind: EdgeUpvalue}},
					Roots: []Root{{ID: "t", Label: RootSandbox}},
				}
			},
			want: "1-based index",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Audit(context.Background(), tc.graph())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBuilderIdempotentNodes(t *testing.T) {
	b := NewBuilder().
		Table("t", "").
		Table("t", "overwritten").
		Function("f", BuiltinLoad).
		Field("t", "f", "f").
		Root("t", RootSandbox)

	g := b.Build()
	if len(g.Nodes) != 2 {
		t.Fatalf("want 2 unique nodes, got %d", len(g.Nodes))
	}
	for _, n := range g.Nodes {
		if n.ID == "t" && n.Builtin != "overwritten" {
			t.Fatalf("table builtin = %q, want overwritten", n.Builtin)
		}
		if n.ID == "f" && n.Kind != KindFunction {
			t.Fatalf("node f must be a function")
		}
	}
}

func TestCancelledContext(t *testing.T) {
	g := NewBuilder().
		Table("sandbox", "").
		Function("load", BuiltinLoad).
		Field("sandbox", "load", "load").
		Root("sandbox", RootSandbox).
		Build()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Audit(ctx, g); err == nil {
		t.Fatal("cancelled context must return an error")
	}
}

func TestKindAndSinkLabels(t *testing.T) {
	if KindTable.String() != "table" || KindFunction.String() != "function" {
		t.Fatal("node kind labels changed")
	}
	want := map[SinkKind]string{
		SinkDebug:      "debug.*",
		SinkIO:         "io.*",
		SinkOSExecute:  "os.execute",
		SinkLoad:       "load",
		SinkLoadFile:   "loadfile",
		SinkDoFile:     "dofile",
		SinkRequire:    "require",
		SinkStringDump: "string.dump",
	}
	for kind, label := range want {
		if kind.String() != label {
			t.Fatalf("%v renders %q, want %q", kind, kind.String(), label)
		}
	}
}
