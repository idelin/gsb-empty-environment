package netsec

import (
	"errors"
	"testing"
)

func httpInput(path string) Input {
	return Input{
		Kind: "http-request",
		Fields: map[string]Input{
			"method": {Value: "GET"},
			"path":   {Value: path},
			"query": {Fields: map[string]Input{
				"q": {Value: "1' OR '1'='1"},
			}},
		},
	}
}

func mustAdd(t *testing.T, tracker *Tracker, counter Counterexample) {
	t.Helper()
	if err := tracker.Add(counter); err != nil {
		t.Fatalf("Add(%s/%s): %v", counter.Version, counter.AssertionID, err)
	}
}

func mapAll(t *testing.T, migration *NodeMigration, entries ...[2]string) {
	t.Helper()
	for _, entry := range entries {
		if err := migration.Map(entry[0], entry[1]); err != nil {
			t.Fatalf("Map(%q -> %q): %v", entry[0], entry[1], err)
		}
	}
}

// 函数拆分：旧函数在新版本里拆成两个函数，节点编号同时变化。
// 调用方先给出双候选映射，证据必须保留歧义；收窄到正确落点后才能判定关联。
func TestAssociate_FunctionSplitKeepsAmbiguity(t *testing.T) {
	oldCounter := Counterexample{
		AssertionID: "SEC-SQLI-001",
		Version:     "v1",
		Input:       httpInput("/search"),
		Origin: Graph{
			Nodes: []Node{
				{ID: "f1", Kind: NodeFunction, Name: "buildQuery"},
				{ID: "o7", Kind: NodeObject, Name: "tainted-sql"},
				{ID: "a1", Kind: NodeAssertion, Name: "SEC-SQLI-001"},
			},
			Edges: []Edge{
				{From: "o7", To: "f1", Relation: "produced-by"},
				{From: "a1", To: "o7", Relation: "checks"},
			},
		},
	}
	newCounter := Counterexample{
		AssertionID: "SEC-SQLI-001",
		Version:     "v2",
		Input:       httpInput("/search"),
		Origin: Graph{
			Nodes: []Node{
				{ID: "f1a", Kind: NodeFunction, Name: "buildQueryCore"},
				{ID: "f1b", Kind: NodeFunction, Name: "buildWhere"},
				{ID: "o209", Kind: NodeObject, Name: "tainted-sql"},
				{ID: "a2", Kind: NodeAssertion, Name: "SEC-SQLI-001"},
			},
			Edges: []Edge{
				{From: "o209", To: "f1a", Relation: "produced-by"},
				{From: "o209", To: "f1b", Relation: "produced-by"},
				{From: "a2", To: "o209", Relation: "checks"},
			},
		},
	}

	ambiguousMigration := NewNodeMigration("v1", "v2")
	mapAll(t, ambiguousMigration,
		[2]string{"f1", "f1a"}, [2]string{"f1", "f1b"},
		[2]string{"o7", "o209"}, [2]string{"a1", "a2"},
	)

	evidence, err := Associate(oldCounter, newCounter, ambiguousMigration)
	if err != nil {
		t.Fatalf("Associate with ambiguous mapping: %v", err)
	}
	if evidence.Verdict != VerdictAmbiguous {
		t.Fatalf("verdict = %q, want %q", evidence.Verdict, VerdictAmbiguous)
	}
	if !evidence.InputEqual {
		t.Fatal("inputs should be structurally equal")
	}
	if len(evidence.AmbiguousNodes) != 1 || evidence.AmbiguousNodes[0] != "f1" {
		t.Fatalf("ambiguous nodes = %v, want [f1]", evidence.AmbiguousNodes)
	}
	if len(evidence.Violations) != 0 {
		t.Fatalf("unexpected violations: %+v", evidence.Violations)
	}

	narrowed := NewNodeMigration("v1", "v2")
	mapAll(t, narrowed,
		[2]string{"f1", "f1a"}, [2]string{"o7", "o209"}, [2]string{"a1", "a2"},
	)
	evidence, err = Associate(oldCounter, newCounter, narrowed)
	if err != nil {
		t.Fatalf("Associate with narrowed mapping: %v", err)
	}
	if evidence.Verdict != VerdictAssociated {
		t.Fatalf("verdict = %q, want %q, reasons: %v", evidence.Verdict, VerdictAssociated, evidence.Reasons)
	}
	for _, edgeEvidence := range evidence.EdgeEvidence {
		if len(edgeEvidence.MatchedEdges) == 0 {
			t.Fatalf("edge %v lost its dependency evidence", edgeEvidence.OldEdge)
		}
	}
}

// 对象编号变化：函数重命名、对象从 obj-17 变成 obj-402，只要显式映射保持
// 依赖边就应关联；新版本改掉依赖关系时必须判 diverged。
func TestAssociate_ObjectRenumbering(t *testing.T) {
	oldCounter := Counterexample{
		AssertionID: "SEC-CMDI-007",
		Version:     "v1",
		Input:       httpInput("/ping"),
		Origin: Graph{
			Nodes: []Node{
				{ID: "fn9", Kind: NodeFunction, Name: "parseInput"},
				{ID: "obj17", Kind: NodeObject, Name: "header-map"},
			},
			Edges: []Edge{{From: "obj17", To: "fn9", Relation: "flows-into"}},
		},
	}
	newCounter := Counterexample{
		AssertionID: "SEC-CMDI-007",
		Version:     "v2",
		Input:       httpInput("/ping"),
		Origin: Graph{
			Nodes: []Node{
				{ID: "fn21", Kind: NodeFunction, Name: "parseInputV2"},
				{ID: "obj402", Kind: NodeObject, Name: "header-map"},
			},
			Edges: []Edge{{From: "obj402", To: "fn21", Relation: "flows-into"}},
		},
	}

	migration := NewNodeMigration("v1", "v2")
	mapAll(t, migration, [2]string{"obj17", "obj402"}, [2]string{"fn9", "fn21"})
	evidence, err := Associate(oldCounter, newCounter, migration)
	if err != nil {
		t.Fatalf("Associate after renumbering: %v", err)
	}
	if evidence.Verdict != VerdictAssociated {
		t.Fatalf("verdict = %q, want %q: %v", evidence.Verdict, VerdictAssociated, evidence.Reasons)
	}

	broken := newCounter
	broken.Origin = Graph{
		Nodes: newCounter.Origin.Nodes,
		Edges: []Edge{{From: "obj402", To: "fn21", Relation: "calls"}},
	}
	evidence, err = Associate(oldCounter, broken, migration)
	if err != nil {
		t.Fatalf("Associate with broken dependency: %v", err)
	}
	if evidence.Verdict != VerdictDiverged {
		t.Fatalf("verdict = %q, want %q", evidence.Verdict, VerdictDiverged)
	}
	if len(evidence.Violations) != 1 || evidence.Violations[0].Code != ViolationDependencyBroken {
		t.Fatalf("violations = %+v, want one %q", evidence.Violations, ViolationDependencyBroken)
	}
}

// 两个相近缺陷：SQLi 与 XSS 反例位置相邻、输入相似，但断言 ID 不同，
// 关联必须拒绝合并；两者各自跨版本追踪时仍然独立成立。
func TestAssociate_NearbyDefectsAreNeverMerged(t *testing.T) {
	tracker := NewTracker()

	sqliV1 := Counterexample{
		AssertionID: "SEC-SQLI-001",
		Version:     "v1",
		Input:       httpInput("/search"),
		Origin: Graph{
			Nodes: []Node{{ID: "sq1", Kind: NodeAssertion, Name: "sql injection near line 40"}},
		},
	}
	xssV1 := Counterexample{
		AssertionID: "SEC-XSS-002",
		Version:     "v1",
		Input:       httpInput("/search"),
		Origin: Graph{
			Nodes: []Node{{ID: "xs1", Kind: NodeAssertion, Name: "xss injection near line 42"}},
		},
	}
	xssV2 := Counterexample{
		AssertionID: "SEC-XSS-002",
		Version:     "v2",
		Input:       httpInput("/search"),
		Origin: Graph{
			Nodes: []Node{{ID: "xs9", Kind: NodeAssertion, Name: "xss injection near line 57"}},
		},
	}
	mustAdd(t, tracker, sqliV1)
	mustAdd(t, tracker, xssV1)
	mustAdd(t, tracker, xssV2)

	crossDefectMigration := NewNodeMigration("v1", "v2")
	if err := crossDefectMigration.Map("sq1", "xs9"); err != nil {
		t.Fatal(err)
	}
	evidence, err := Associate(sqliV1, xssV2, crossDefectMigration)
	if err != nil {
		t.Fatalf("Associate across distinct defects: %v", err)
	}
	if evidence.Verdict != VerdictDiverged {
		t.Fatalf("verdict = %q, want %q", evidence.Verdict, VerdictDiverged)
	}

	xssMigration := NewNodeMigration("v1", "v2")
	if err := xssMigration.Map("xs1", "xs9"); err != nil {
		t.Fatal(err)
	}
	xssEvidence, err := tracker.AssociatePair("v1", "v2", "SEC-XSS-002", xssMigration)
	if err != nil {
		t.Fatalf("AssociatePair for XSS: %v", err)
	}
	if xssEvidence.Verdict != VerdictAssociated {
		t.Fatalf("XSS verdict = %q, want %q", xssEvidence.Verdict, VerdictAssociated)
	}

	if _, err := tracker.Lookup("v1", "SEC-SQLI-001"); err != nil {
		t.Fatalf("SQLi counterexample lost from tracker: %v", err)
	}
	if _, err := tracker.Lookup("v1", "SEC-XSS-002"); err != nil {
		t.Fatalf("XSS counterexample lost from tracker: %v", err)
	}
}

func TestAssociate_RejectsUnmappedNodeAndUnknownTarget(t *testing.T) {
	oldCounter := Counterexample{
		AssertionID: "SEC-A-1",
		Version:     "v1",
		Input:       httpInput("/a"),
		Origin: Graph{
			Nodes: []Node{
				{ID: "n1", Kind: NodeObject},
				{ID: "n2", Kind: NodeFunction},
			},
			Edges: []Edge{{From: "n1", To: "n2", Relation: "dep"}},
		},
	}
	newCounter := Counterexample{
		AssertionID: "SEC-A-1",
		Version:     "v2",
		Input:       httpInput("/a"),
		Origin: Graph{
			Nodes: []Node{{ID: "m1", Kind: NodeObject}},
		},
	}

	incomplete := NewNodeMigration("v1", "v2")
	if err := incomplete.Map("n1", "m1"); err != nil {
		t.Fatal(err)
	}
	if err := incomplete.Map("n1", "ghost-node"); err != nil {
		t.Fatal(err)
	}
	evidence, err := Associate(oldCounter, newCounter, incomplete)
	if err != nil {
		t.Fatalf("Associate: %v", err)
	}
	if evidence.Verdict != VerdictDiverged {
		t.Fatalf("verdict = %q, want %q", evidence.Verdict, VerdictDiverged)
	}
	var sawUnmapped, sawUnknownTarget, sawBroken bool
	for _, violation := range evidence.Violations {
		switch violation.Code {
		case ViolationUnmappedNode:
			sawUnmapped = true
		case ViolationUnknownTarget:
			sawUnknownTarget = true
		case ViolationDependencyBroken:
			sawBroken = true
		}
	}
	if !sawUnmapped || !sawUnknownTarget || !sawBroken {
		t.Fatalf("violations = %+v, want unmapped-node, unknown-target and dependency-broken", evidence.Violations)
	}

	if err := NewNodeMigration("v1", "v2").Map("", "m1"); err == nil {
		t.Fatal("Map with empty old node id must fail")
	}
	if _, err := Associate(oldCounter, newCounter, nil); err == nil {
		t.Fatal("Associate without an explicit migration must fail")
	}
	wrongVersions := NewNodeMigration("v9", "v2")
	if err := wrongVersions.Map("n1", "m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Associate(oldCounter, newCounter, wrongVersions); err == nil {
		t.Fatal("Associate with mismatched migration versions must fail")
	}
}

func TestInputEqual_Structural(t *testing.T) {
	base := httpInput("/search")
	reordered := Input{
		Kind: "http-request",
		Fields: map[string]Input{
			"query": {Fields: map[string]Input{
				"q": {Value: "1' OR '1'='1"},
			}},
			"path":   {Value: "/search"},
			"method": {Value: "GET"},
		},
	}
	if !base.Equal(reordered) {
		t.Fatal("map field ordering must not affect structural equality")
	}

	sequenceA := Input{Kind: "args", Seq: []Input{{Value: "a"}, {Value: "b"}}}
	sequenceB := Input{Kind: "args", Seq: []Input{{Value: "b"}, {Value: "a"}}}
	if sequenceA.Equal(sequenceB) {
		t.Fatal("sequence ordering must be significant")
	}

	if (Input{Value: 1}).Equal(Input{Value: "1"}) {
		t.Fatal("leaf values of different types must not be equal")
	}

	differentPath := httpInput("/login")
	if base.Equal(differentPath) {
		t.Fatal("different leaf values must not be equal")
	}

	oldCounter := Counterexample{
		AssertionID: "SEC-SQLI-001",
		Version:     "v1",
		Input:       base,
		Origin:      Graph{Nodes: []Node{{ID: "a1", Kind: NodeAssertion}}},
	}
	newCounter := Counterexample{
		AssertionID: "SEC-SQLI-001",
		Version:     "v2",
		Input:       differentPath,
		Origin:      Graph{Nodes: []Node{{ID: "a2", Kind: NodeAssertion}}},
	}
	migration := NewNodeMigration("v1", "v2")
	if err := migration.Map("a1", "a2"); err != nil {
		t.Fatal(err)
	}
	evidence, err := Associate(oldCounter, newCounter, migration)
	if err != nil {
		t.Fatalf("Associate with changed input: %v", err)
	}
	if evidence.Verdict != VerdictDiverged || evidence.InputEqual {
		t.Fatalf("changed input must diverge, got verdict=%q inputEqual=%v", evidence.Verdict, evidence.InputEqual)
	}
}

func TestTracker_DuplicateAndLookupErrors(t *testing.T) {
	tracker := NewTracker()
	counter := Counterexample{
		AssertionID: "SEC-A-1",
		Version:     "v1",
		Input:       httpInput("/a"),
		Origin:      Graph{Nodes: []Node{{ID: "n", Kind: NodeAssertion}}},
	}
	mustAdd(t, tracker, counter)
	if err := tracker.Add(counter); err == nil {
		t.Fatal("duplicate assertion in the same version must be rejected")
	}
	if got := tracker.Versions(); len(got) != 1 || got[0] != "v1" {
		t.Fatalf("Versions() = %v, want [v1]", got)
	}
	if _, err := tracker.Lookup("v9", "SEC-A-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown version error = %v, want ErrNotFound", err)
	}
	if _, err := tracker.Lookup("v1", "SEC-NOPE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown assertion error = %v, want ErrNotFound", err)
	}

	invalid := counter
	invalid.AssertionID = ""
	if err := tracker.Add(invalid); err == nil {
		t.Fatal("invalid counterexample must be rejected")
	}
}
