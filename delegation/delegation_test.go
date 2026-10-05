package delegation

import (
	"reflect"
	"testing"
	"time"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(hours int) time.Time { return epoch.Add(time.Duration(hours) * time.Hour) }

func window(start, end int) (time.Time, time.Time) { return at(start), at(end) }

func mustIssue(t *testing.T, s *Store, g Grant, issueAt time.Time) int {
	t.Helper()
	gen, err := s.Issue(g, issueAt)
	if err != nil {
		t.Fatalf("Issue(%s) failed: %v", g.ID, err)
	}
	return gen
}

func mustRevoke(t *testing.T, s *Store, id string, gen int, at time.Time) {
	t.Helper()
	if err := s.Revoke(id, gen, at); err != nil {
		t.Fatalf("Revoke(%s#%d) failed: %v", id, gen, err)
	}
}

func assertResult(t *testing.T, got Result, valid bool, reason Reason, chain ...string) {
	t.Helper()
	if got.Valid != valid {
		t.Fatalf("Valid = %v, want %v (result %+v)", got.Valid, valid, got)
	}
	if valid {
		return
	}
	if got.Reason != reason {
		t.Fatalf("Reason = %q, want %q (chain %v)", got.Reason, reason, got.Chain)
	}
	if !reflect.DeepEqual(got.Chain, chain) {
		t.Fatalf("Chain = %v, want %v", got.Chain, chain)
	}
}

func rootGrant(id string, start, end int) Grant {
	nb, na := window(start, end)
	return Grant{
		ID:        id,
		Issuer:    "root-ca",
		Subject:   "team",
		Actions:   []string{"read", "write", "deploy"},
		Resources: []string{"repo/*", "cluster/prod"},
		NotBefore: nb,
		NotAfter:  na,
	}
}

func childGrant(id string, parentID string, start, end int) Grant {
	nb, na := window(start, end)
	return Grant{
		ID:        id,
		Issuer:    "team",
		Subject:   "service-a",
		Actions:   []string{"read"},
		Resources: []string{"repo/*"},
		NotBefore: nb,
		NotAfter:  na,
		Parent:    &Ref{ID: parentID},
	}
}

func TestRootGrantValid(t *testing.T) {
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 0, 100), at(0))
	assertResult(t, s.Validate("root", 0, at(50)), true, "")
}

func TestMultiLevelDelegationValid(t *testing.T) {
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 0, 100), at(0))
	mustIssue(t, s, childGrant("mid", "root", 10, 90), at(10))
	leaf := childGrant("leaf", "mid", 20, 80)
	mustIssue(t, s, leaf, at(20))
	assertResult(t, s.Validate("leaf", 0, at(50)), true, "")
}

func TestActionsExceedParent(t *testing.T) {
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 0, 100), at(0))
	child := childGrant("child", "root", 10, 90)
	child.Actions = []string{"read", "delete"}
	mustIssue(t, s, child, at(10))
	assertResult(t, s.Validate("child", 0, at(50)), false,
		ReasonActionsExceedParent, "child#1", "root#1")
}

func TestResourcesExceedParent(t *testing.T) {
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 0, 100), at(0))
	child := childGrant("child", "root", 10, 90)
	child.Resources = []string{"repo/*", "cluster/staging"}
	mustIssue(t, s, child, at(10))
	assertResult(t, s.Validate("child", 0, at(50)), false,
		ReasonResourcesExceedParent, "child#1", "root#1")
}

func TestValidityExceedsParent(t *testing.T) {
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 0, 100), at(0))

	tooLate := childGrant("late", "root", 10, 120)
	mustIssue(t, s, tooLate, at(10))
	assertResult(t, s.Validate("late", 0, at(50)), false,
		ReasonValidityExceedsParent, "late#1", "root#1")

	tooEarly := childGrant("early", "root", -10, 90)
	mustIssue(t, s, tooEarly, at(10))
	assertResult(t, s.Validate("early", 0, at(50)), false,
		ReasonValidityExceedsParent, "early#1", "root#1")
}

func TestRevokeUpstreamInvalidatesDescendantsImmediately(t *testing.T) {
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 0, 100), at(0))
	mustIssue(t, s, childGrant("mid", "root", 10, 90), at(10))
	mustIssue(t, s, childGrant("leaf", "mid", 20, 80), at(20))

	mustRevoke(t, s, "root", 0, at(60))

	// 撤销后：后代立即失效，原因链指向被撤销的祖先。
	assertResult(t, s.Validate("leaf", 0, at(61)), false,
		ReasonRevoked, "leaf#1", "mid#1", "root#1")
	// 历史时点：撤销之前查询仍然有效。
	assertResult(t, s.Validate("leaf", 0, at(59)), true, "")
}

func TestExpiryReportedAtNearestNode(t *testing.T) {
	// 子授权有效期必须被父授权包含，因此到期总是先在自身暴露：
	// 最短原因链停在子节点自身。
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 0, 60), at(0))
	mustIssue(t, s, childGrant("child", "root", 10, 60), at(10))
	assertResult(t, s.Validate("child", 0, at(60)), false,
		ReasonExpired, "child#1")
	assertResult(t, s.Validate("child", 0, at(59)), true, "")
}

func TestShortestReasonChainStopsAtNearestFailure(t *testing.T) {
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 0, 100), at(0))
	mustIssue(t, s, childGrant("mid", "root", 10, 90), at(10))
	mustIssue(t, s, childGrant("leaf", "mid", 20, 80), at(20))

	// 只撤销中间节点：叶子的最短原因链在 mid 处停止，不延伸到 root。
	mustRevoke(t, s, "mid", 1, at(30))
	assertResult(t, s.Validate("leaf", 0, at(31)), false,
		ReasonRevoked, "leaf#1", "mid#1")
}

func TestReissueSameNameGenerations(t *testing.T) {
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 0, 100), at(0))
	gen1 := mustIssue(t, s, childGrant("mid", "root", 10, 90), at(10))
	mustIssue(t, s, childGrant("leaf", "mid", 20, 80), at(20))

	// 同名重发 mid：产生第 2 代，并在 at(40) 取代第 1 代。
	gen2 := mustIssue(t, s, childGrant("mid", "root", 10, 95), at(40))
	if gen1 != 1 || gen2 != 2 {
		t.Fatalf("generations = %d, %d; want 1, 2", gen1, gen2)
	}

	// 取代之后：旧代次失效并给出 SUPERSEDED，新代次有效。
	assertResult(t, s.Validate("mid", 1, at(50)), false, ReasonSuperseded, "mid#1")
	assertResult(t, s.Validate("mid", 2, at(50)), true, "")
	// 不指定代次时解析到当前最新一代。
	assertResult(t, s.Validate("mid", 0, at(50)), true, "")

	// 历史时点：取代之前第 1 代仍然有效。
	assertResult(t, s.Validate("mid", 1, at(39)), true, "")

	// leaf 签发时依托的是 mid#1：mid#1 被取代后 leaf 随之失效，
	// 且失效原因链停在 mid#1（最短链）。
	assertResult(t, s.Validate("leaf", 0, at(50)), false,
		ReasonSuperseded, "leaf#1", "mid#1")
	// 历史时点：取代之前 leaf 有效。
	assertResult(t, s.Validate("leaf", 0, at(39)), true, "")
}

func TestReissuedChildBindsToNewGeneration(t *testing.T) {
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 0, 100), at(0))
	mustIssue(t, s, childGrant("mid", "root", 10, 90), at(10))
	mustIssue(t, s, childGrant("mid", "root", 10, 95), at(40))

	// 重发之后签发的子授权解析到最新一代 mid#2。
	mustIssue(t, s, childGrant("leaf2", "mid", 45, 80), at(45))
	assertResult(t, s.Validate("leaf2", 0, at(50)), true, "")
}

func TestMissingParentChain(t *testing.T) {
	s := NewStore()
	orphan := childGrant("orphan", "ghost", 10, 90)
	mustIssue(t, s, orphan, at(10))
	assertResult(t, s.Validate("orphan", 0, at(50)), false,
		ReasonParentMissing, "orphan#1", "ghost#0")

	// 指定了缺失的具体代次时，链尾应包含该代次。
	orphan2 := childGrant("orphan2", "ghost", 10, 90)
	orphan2.Parent = &Ref{ID: "ghost", Generation: 3}
	mustIssue(t, s, orphan2, at(10))
	assertResult(t, s.Validate("orphan2", 0, at(50)), false,
		ReasonParentMissing, "orphan2#1", "ghost#3")
}

func TestUnknownGrant(t *testing.T) {
	s := NewStore()
	assertResult(t, s.Validate("nope", 0, at(1)), false, ReasonNotFound, "nope#0")
}

func TestNotYetIssued(t *testing.T) {
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 0, 100), at(10))
	assertResult(t, s.Validate("root", 0, at(5)), false, ReasonNotYetIssued, "root#0")
}

func TestNotYetValidAndExpired(t *testing.T) {
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 10, 20), at(0))
	assertResult(t, s.Validate("root", 0, at(5)), false, ReasonNotYetValid, "root#1")
	assertResult(t, s.Validate("root", 0, at(20)), false, ReasonExpired, "root#1")
}

func TestCycleDetected(t *testing.T) {
	s := NewStore()
	a := childGrant("a", "b", 0, 100)
	b := childGrant("b", "a", 0, 100)
	mustIssue(t, s, a, at(0))
	mustIssue(t, s, b, at(0))
	res := s.Validate("a", 0, at(50))
	if res.Valid || res.Reason != ReasonCycle {
		t.Fatalf("want cycle detection, got %+v", res)
	}
}

func TestRevokeUnknownGrantFails(t *testing.T) {
	s := NewStore()
	if err := s.Revoke("ghost", 0, at(0)); err == nil {
		t.Fatal("expected error revoking unknown grant")
	}
}

func TestLookup(t *testing.T) {
	s := NewStore()
	mustIssue(t, s, rootGrant("root", 0, 100), at(10))
	if _, ok := s.Lookup("root", at(5)); ok {
		t.Fatal("Lookup before issue should miss")
	}
	ref, ok := s.Lookup("root", at(10))
	if !ok || ref != (Ref{ID: "root", Generation: 1}) {
		t.Fatalf("Lookup = %v, %v", ref, ok)
	}
}
