package checkpoint

import (
	"strings"
	"testing"
)

func buildRepo(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	a := put(t, s, "a")
	b := put(t, s, "b")
	c := put(t, s, "c")

	mustCommit(t, s, "base", nil, []Operation{addOp(Ref{"a", a})})
	mustCommit(t, s, "left", []string{"base"}, []Operation{addOp(Ref{"b", b})})
	mustCommit(t, s, "right", []string{"base"}, []Operation{addOp(Ref{"c", c})})
	if _, err := s.Merge("m", "left", "right", nil); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVerifyCleanRepo(t *testing.T) {
	s := buildRepo(t)
	if issues := s.Verify(); len(issues) != 0 {
		t.Fatalf("issues: %v", issues)
	}
}

func TestVerifyDetectsDigestTampering(t *testing.T) {
	s := buildRepo(t)
	s.snapshots["left"].Ops = append(s.snapshots["left"].Ops, removeOp("a"))

	issues := s.Verify()
	if !hasIssue(issues, "digest mismatch") {
		t.Fatalf("expected digest mismatch, got %v", issues)
	}
}

func TestVerifyDetectsMissingParent(t *testing.T) {
	s := buildRepo(t)
	s.snapshots["left"].Parents = []string{"ghost"}

	issues := s.Verify()
	if !hasIssue(issues, "missing parent") {
		t.Fatalf("expected missing parent, got %v", issues)
	}
}

func TestVerifyDetectsCycle(t *testing.T) {
	s := buildRepo(t)
	// Forge a cycle: base <-> left.
	s.snapshots["base"].Parents = []string{"left"}

	issues := s.Verify()
	if !hasIssue(issues, "cycle") {
		t.Fatalf("expected cycle report, got %v", issues)
	}
}

func TestVerifyDetectsDuplicateRef(t *testing.T) {
	s := buildRepo(t)
	dup := put(t, s, "dup")
	s.snapshots["left"].Ops = []Operation{addOp(Ref{"p", dup}, Ref{"p", dup})}

	issues := s.Verify()
	if !hasIssue(issues, "duplicate reference") {
		t.Fatalf("expected duplicate reference report, got %v", issues)
	}
}

func TestVerifyDetectsUnknownBlock(t *testing.T) {
	s := buildRepo(t)
	s.snapshots["left"].Ops = []Operation{addOp(Ref{"p", "sha256:missing"})}

	issues := s.Verify()
	if !hasIssue(issues, "unknown block") {
		t.Fatalf("expected unknown block report, got %v", issues)
	}
}

func TestVerifyDetectsMergeTampering(t *testing.T) {
	s := buildRepo(t)
	// Corrupt the auto-resolution of the merge without fixing the digest.
	s.snapshots["m"].AutoOps = []Operation{removeOp("a")}

	issues := s.Verify()
	if !hasIssue(issues, "digest mismatch") && !hasIssue(issues, "merge") {
		t.Fatalf("expected merge/digest inconsistency, got %v", issues)
	}
}

func hasIssue(issues []string, substr string) bool {
	for _, i := range issues {
		if strings.Contains(i, substr) {
			return true
		}
	}
	return false
}
