package netsec

import (
	"errors"
	"testing"
)

func labelSession(value string, transitive bool) *Session {
	s := NewSession(map[string]string{"project": value})
	if transitive {
		s.Transitive["project"] = true
		l := s.Labels["project"]
		l.Transitive = true
		s.Labels["project"] = l
	}
	return s
}

func TestMergeRejectsConflictingValues(t *testing.T) {
	left := labelSession("low", true)
	right := labelSession("high", true)
	if _, err := MergeSessions(left, right); !errors.Is(err, ErrLabelConflict) {
		t.Fatalf("err = %v, want label conflict", err)
	}
	if out, err := MergeAudited(left, right); err != nil {
		t.Fatalf("audited merge: %v", err)
	} else if out.Labels["project"].Value != "high" {
		t.Fatalf("audited merge value = %q", out.Labels["project"].Value)
	}
}

func TestMergeIntersectsTransitivity(t *testing.T) {
	left := labelSession("same", true)
	right := labelSession("same", false)
	out, err := MergeSessions(left, right)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if out.Transitive["project"] {
		t.Fatal("merge upgraded a non-transitive branch")
	}
	if len(out.Labels["project"].Origins) < 2 {
		t.Fatalf("merge lost provenance: %#v", out.Labels["project"].Origins)
	}
	if out.Labels["project"].Origins[len(out.Labels["project"].Origins)-1].Kind != OriginMerge {
		t.Fatalf("merge proof missing merge step: %#v", out.Labels["project"].Origins)
	}

	audited, err := MergeSessionsWithConfig(left, right, AuditConfig{Flaws: FlawMergeTransitiveUnion})
	if err != nil {
		t.Fatalf("audited merge: %v", err)
	}
	if !audited.Transitive["project"] {
		t.Fatal("audited union did not upgrade the label")
	}
}

func TestMergeFixedConflict(t *testing.T) {
	left := NewSession(map[string]string{"department": "eng"})
	l := left.Labels["department"]
	l.Fixed = true
	left.Labels["department"] = l
	right := NewSession(map[string]string{"department": "eng"})
	if _, err := MergeSessions(left, right); !errors.Is(err, ErrFixedConflict) {
		t.Fatalf("err = %v, want fixed conflict", err)
	}
	if out, err := MergeAudited(left, right); err != nil {
		t.Fatalf("audited merge: %v", err)
	} else if out.Values()["department"] != "eng" {
		t.Fatalf("audited merge value = %q", out.Values()["department"])
	}
}

func TestMergeTwoChainsAgreeing(t *testing.T) {
	left := labelSession("same", true)
	right := labelSession("same", true)
	out, err := MergeSessions(left, right)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !out.Transitive["project"] {
		t.Fatal("both transitive branches should remain transitive")
	}
	if len(out.Labels["project"].Origins) < 2 {
		t.Fatalf("origins = %#v", out.Labels["project"].Origins)
	}
}

func TestMergeKeepsNonTransitiveTaint(t *testing.T) {
	left := labelSession("same", true)
	right := labelSession("same", false)
	out, err := MergeSessions(left, right)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !out.NonTransitive["project"] {
		t.Fatal("merged non-transitive branch lost taint")
	}
	next, err := out.Advance(HopRequest{
		RequestedLabels:     map[string]string{"project": "fresh"},
		RequestedTransitive: []string{"project"},
		OverrideKeys:        []string{"project"},
		RoleID:              "role-c",
	})
	if !errors.Is(err, ErrTransitiveUp) {
		t.Fatalf("merged chain upgrade err = %v, want rejection (next=%v)", err, next)
	}
}
