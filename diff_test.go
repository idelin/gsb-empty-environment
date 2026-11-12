package netsec

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCompareReportsUnconditionalAddition(t *testing.T) {
	oldGraph := Graph{
		Root:  "root",
		Nodes: []Node{{ID: "root"}, {ID: "dep"}},
		Edges: []Edge{{From: "root", To: "dep"}},
	}
	newGraph := Graph{
		Root:  "root",
		Nodes: []Node{{ID: "root"}, {ID: "dep", Capabilities: []string{"postinstall"}}},
		Edges: []Edge{{From: "root", To: "dep"}},
	}

	report, err := Compare(oldGraph, newGraph)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 1 {
		t.Fatalf("Added = %#v, want one capability", report.Added)
	}
	addition := report.Added[0]
	if addition.Capability != "postinstall" {
		t.Fatalf("Capability = %q", addition.Capability)
	}
	if want := []string{"root", "dep"}; !reflect.DeepEqual(addition.Path, want) {
		t.Fatalf("Path = %#v, want %#v", addition.Path, want)
	}
	if len(addition.Config) != 0 {
		t.Fatalf("Config = %s, want empty configuration", addition.Config)
	}
}

func TestCompareIgnoresInactiveNodes(t *testing.T) {
	oldGraph := Graph{
		Root: "root",
		Nodes: []Node{
			{ID: "root"},
			{ID: "dead"},
			{ID: "orphan"},
		},
		Edges: []Edge{{From: "root", To: "dead", When: Never()}},
	}
	newGraph := Graph{
		Root: "root",
		Nodes: []Node{
			{ID: "root"},
			{ID: "dead", Capabilities: []string{"postinstall"}},
			{ID: "orphan", Capabilities: []string{"build-script"}},
		},
		Edges: []Edge{{From: "root", To: "dead", When: Never()}},
	}

	report, err := Compare(oldGraph, newGraph)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 0 {
		t.Fatalf("inactive capabilities were reported: %#v", report.Added)
	}
}

func TestCompareConditionalAddition(t *testing.T) {
	oldGraph := Graph{
		Root:  "root",
		Nodes: []Node{{ID: "root"}, {ID: "tool"}},
		Edges: []Edge{{From: "root", To: "tool", When: Flag("linux")}},
	}
	newGraph := Graph{
		Root:  "root",
		Nodes: []Node{{ID: "root"}, {ID: "tool", Capabilities: []string{"postinstall"}}},
		Edges: []Edge{{From: "root", To: "tool", When: Flag("linux")}},
	}

	report, err := Compare(oldGraph, newGraph)
	if err != nil {
		t.Fatal(err)
	}
	addition, ok := report.Find("postinstall")
	if !ok {
		t.Fatalf("postinstall not reported: %#v", report.Added)
	}
	if want := (Config{"linux": true}); !reflect.DeepEqual(addition.Config, want) {
		t.Fatalf("Config = %s, want %s", addition.Config, want)
	}
	if want := []string{"root", "tool"}; !reflect.DeepEqual(addition.Path, want) {
		t.Fatalf("Path = %#v, want %#v", addition.Path, want)
	}
}

func TestCompareSharedDependencyPerConfiguration(t *testing.T) {
	// postinstall exists in the old graph, but only on a node activated by
	// feature=true. Under feature=false it is a new capability of the shared
	// dependency. Comparing global capability unions would miss this.
	oldGraph := Graph{
		Root: "root",
		Nodes: []Node{
			{ID: "root"},
			{ID: "carrier", Capabilities: []string{"postinstall"}},
			{ID: "shared"},
		},
		Edges: []Edge{
			{From: "root", To: "carrier", When: Flag("feature")},
			{From: "root", To: "shared"},
		},
	}
	newGraph := Graph{
		Root: "root",
		Nodes: []Node{
			{ID: "root"},
			{ID: "carrier", Capabilities: []string{"postinstall"}},
			{ID: "shared", Capabilities: []string{"postinstall"}},
		},
		Edges: []Edge{
			{From: "root", To: "carrier", When: Flag("feature")},
			{From: "root", To: "shared"},
		},
	}

	report, err := Compare(oldGraph, newGraph)
	if err != nil {
		t.Fatal(err)
	}
	addition, ok := report.Find("postinstall")
	if !ok {
		t.Fatalf("shared dependency capability was missed: %#v", report.Added)
	}
	if want := (Config{"feature": false}); !reflect.DeepEqual(addition.Config, want) {
		t.Fatalf("Config = %s, want %s", addition.Config, want)
	}
	if want := []string{"root", "shared"}; !reflect.DeepEqual(addition.Path, want) {
		t.Fatalf("Path = %#v, want %#v", addition.Path, want)
	}
}

func TestCompareSharedNodeActivatedOnlyInSomeConfigs(t *testing.T) {
	// The shared node and its capability exist in both versions. The old graph
	// reaches it only when feature=true; the new graph reaches it
	// unconditionally. Under feature=false the capability is newly installed.
	oldGraph := Graph{
		Root:  "root",
		Nodes: []Node{{ID: "root"}, {ID: "shared", Capabilities: []string{"postinstall"}}},
		Edges: []Edge{{From: "root", To: "shared", When: Flag("feature")}},
	}
	newGraph := Graph{
		Root:  "root",
		Nodes: []Node{{ID: "root"}, {ID: "shared", Capabilities: []string{"postinstall"}}},
		Edges: []Edge{{From: "root", To: "shared"}},
	}

	report, err := Compare(oldGraph, newGraph)
	if err != nil {
		t.Fatal(err)
	}
	addition, ok := report.Find("postinstall")
	if !ok {
		t.Fatalf("newly activated shared node was missed: %#v", report.Added)
	}
	if want := (Config{"feature": false}); !reflect.DeepEqual(addition.Config, want) {
		t.Fatalf("Config = %s, want %s", addition.Config, want)
	}
	if want := []string{"root", "shared"}; !reflect.DeepEqual(addition.Path, want) {
		t.Fatalf("Path = %#v, want %#v", addition.Path, want)
	}
}

func TestCompareChoosesShortestPath(t *testing.T) {
	nodes := []Node{
		{ID: "root"},
		{ID: "a"},
		{ID: "b"},
		{ID: "c"},
		{ID: "target"},
	}
	edges := []Edge{
		{From: "root", To: "a"},
		{From: "a", To: "target"},
		{From: "root", To: "b"},
		{From: "b", To: "c"},
		{From: "c", To: "target"},
	}
	oldGraph := Graph{Root: "root", Nodes: nodes, Edges: edges}
	newNodes := append([]Node(nil), nodes...)
	newNodes[4].Capabilities = []string{"postinstall"}
	newGraph := Graph{Root: "root", Nodes: newNodes, Edges: edges}

	report, err := Compare(oldGraph, newGraph)
	if err != nil {
		t.Fatal(err)
	}
	addition, ok := report.Find("postinstall")
	if !ok {
		t.Fatalf("postinstall not reported: %#v", report.Added)
	}
	if want := []string{"root", "a", "target"}; !reflect.DeepEqual(addition.Path, want) {
		t.Fatalf("Path = %#v, want shortest path %#v", addition.Path, want)
	}
}

func TestCompareRejectsMoreThanSixteenPlatforms(t *testing.T) {
	var edges []Edge
	for i := 0; i < 17; i++ {
		edges = append(edges, Edge{
			From: "root",
			To:   "dep",
			When: Flag("p" + string(rune('a'+i))),
		})
	}
	graph := Graph{
		Root:  "root",
		Nodes: []Node{{ID: "root"}, {ID: "dep"}},
		Edges: edges,
	}

	_, err := Compare(graph, graph)
	if err == nil {
		t.Fatal("expected an error for 17 platform conditions")
	}
	if !strings.Contains(err.Error(), "maximum is 16") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompareAcceptsSixteenPlatforms(t *testing.T) {
	platforms := make([]string, 16)
	conditions := make([]Condition, 16)
	for i := range platforms {
		platforms[i] = "p" + strconv.Itoa(i)
		conditions[i] = Flag(platforms[i])
	}
	edges := []Edge{{
		From: "root",
		To:   "dep",
		When: And(conditions...),
	}}
	oldGraph := Graph{
		Root:  "root",
		Nodes: []Node{{ID: "root"}, {ID: "dep"}},
		Edges: edges,
	}
	newGraph := Graph{
		Root:  "root",
		Nodes: []Node{{ID: "root"}, {ID: "dep", Capabilities: []string{"postinstall"}}},
		Edges: edges,
	}

	report, err := Compare(oldGraph, newGraph)
	if err != nil {
		t.Fatal(err)
	}
	addition, ok := report.Find("postinstall")
	if !ok {
		t.Fatalf("postinstall not reported: %#v", report.Added)
	}
	if len(addition.Config) != 16 {
		t.Fatalf("witness has %d flags, want 16", len(addition.Config))
	}
	for _, name := range platforms {
		if !addition.Config[name] {
			t.Fatalf("witness %s does not set %q", addition.Config, name)
		}
	}
}

func TestGraphValidation(t *testing.T) {
	tests := []struct {
		name  string
		graph Graph
		want  string
	}{
		{
			name:  "missing root",
			graph: Graph{Root: "root", Nodes: []Node{{ID: "dep"}}},
			want:  "root",
		},
		{
			name:  "duplicate node",
			graph: Graph{Root: "root", Nodes: []Node{{ID: "root"}, {ID: "root"}}},
			want:  "duplicate",
		},
		{
			name: "unknown edge destination",
			graph: Graph{
				Root:  "root",
				Nodes: []Node{{ID: "root"}},
				Edges: []Edge{{From: "root", To: "missing"}},
			},
			want: "unknown destination",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.graph.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.want)
			}
		})
	}

	sixteen := Graph{
		Root:      "root",
		Platforms: []string{"p0", "p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8", "p9", "p10", "p11", "p12", "p13", "p14", "p15"},
		Nodes:     []Node{{ID: "root"}},
	}
	if err := sixteen.Validate(); err != nil {
		t.Fatalf("16 platforms should be accepted: %v", err)
	}
}

func TestShortestPath(t *testing.T) {
	graph := Graph{
		Root: "root",
		Nodes: []Node{
			{ID: "root"},
			{ID: "a"},
			{ID: "b"},
			{ID: "target"},
		},
		Edges: []Edge{
			{From: "root", To: "a", When: Flag("fast")},
			{From: "a", To: "target"},
			{From: "root", To: "b"},
			{From: "b", To: "target", When: Flag("slow")},
		},
	}

	path, active, err := graph.ShortestPath(Config{"fast": true, "slow": false}, "target")
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("target should be active")
	}
	if want := []string{"root", "a", "target"}; !reflect.DeepEqual(path, want) {
		t.Fatalf("path = %#v, want %#v", path, want)
	}

	path, active, err = graph.ShortestPath(Config{"fast": false, "slow": false}, "target")
	if err != nil {
		t.Fatal(err)
	}
	if active || path != nil {
		t.Fatalf("target should be inactive, got path %#v", path)
	}
}
