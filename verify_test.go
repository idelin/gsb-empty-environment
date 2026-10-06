package testsel

import (
	"testing"
)

func issuesOfKind(issues []Issue, kind IssueKind) []Issue {
	var out []Issue
	for _, i := range issues {
		if i.Kind == kind {
			out = append(out, i)
		}
	}
	return out
}

func TestVerifyDeletedFuncCovered(t *testing.T) {
	old := snap()
	old.Funcs["m.gone"] = "h1"
	old.Funcs["m.svc"] = "h2"
	old.Edges = []Edge{{"m.svc", "m.gone"}}
	old.Coverage["m.TestSvc"] = []FuncID{"m.svc"}

	new := snap()
	new.Funcs["m.svc"] = "h2"

	issues := Verify(old, new)
	del := issuesOfKind(issues, IssueDeletedFuncCovered)
	if len(del) != 1 || del[0].Func != "m.gone" {
		t.Fatalf("deleted-func issues: %v", del)
	}
}

func TestVerifyStaleCoverage(t *testing.T) {
	old := snap()
	old.Funcs["m.a"] = "h1"
	old.Coverage["m.TestA"] = []FuncID{"m.a", "m.phantom"}

	new := snap()
	new.Funcs["m.a"] = "h1"

	issues := Verify(old, new)
	stale := issuesOfKind(issues, IssueStaleCoverage)
	if len(stale) != 1 || stale[0].Func != "m.phantom" || stale[0].Test != "m.TestA" {
		t.Fatalf("stale issues: %v", stale)
	}
}

func TestVerifyDynamicEdgeChanged(t *testing.T) {
	old := snap()
	old.Funcs["m.svc"] = "h1"
	old.Funcs["m.(A).Run"] = "h2"
	old.DynCalls["m.svc"] = []string{"Run"}
	old.DynImpls["Run"] = []FuncID{"m.(A).Run"}

	new := snap()
	new.Funcs["m.svc"] = "h1"
	new.Funcs["m.(A).Run"] = "h2-changed"
	new.DynCalls["m.svc"] = []string{"Run"}
	new.DynImpls["Run"] = []FuncID{"m.(A).Run"}

	issues := Verify(old, new)
	dyn := issuesOfKind(issues, IssueDynamicEdgeChanged)
	if len(dyn) != 1 || dyn[0].Func != "m.svc" {
		t.Fatalf("dynamic issues: %v", dyn)
	}
}

func TestVerifyDanglingEdge(t *testing.T) {
	old := snap()
	new := snap()
	new.Funcs["m.a"] = "h1"
	new.Edges = []Edge{{"m.a", "m.missing"}}

	issues := Verify(old, new)
	if len(issuesOfKind(issues, IssueDanglingEdge)) != 1 {
		t.Fatalf("issues: %v", issues)
	}
}

func TestVerifyGhostEdgeChange(t *testing.T) {
	// Edge removed but the caller's body hash is unchanged: inconsistent,
	// must be flagged.
	old := snap()
	old.Funcs["m.a"] = "h1"
	old.Funcs["m.b"] = "h2"
	old.Edges = []Edge{{"m.a", "m.b"}}

	new := snap()
	new.Funcs["m.a"] = "h1"
	new.Funcs["m.b"] = "h2"

	issues := Verify(old, new)
	ghost := issuesOfKind(issues, IssueGhostEdgeChange)
	if len(ghost) != 1 || ghost[0].Func != "m.a" {
		t.Fatalf("ghost issues: %v", ghost)
	}
}

func TestVerifyClean(t *testing.T) {
	old := snap()
	old.Funcs["m.a"] = "h1"
	old.Funcs["m.b"] = "h2"
	old.Edges = []Edge{{"m.a", "m.b"}}
	old.Coverage["m.TestA"] = []FuncID{"m.a", "m.b"}

	new := snap()
	new.Funcs["m.a"] = "h1"
	new.Funcs["m.b"] = "h2"
	new.Edges = old.Edges

	if issues := Verify(old, new); len(issues) != 0 {
		t.Fatalf("expected no issues, got %v", issues)
	}
}
