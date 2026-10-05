package delegation

import (
	"testing"
	"time"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(hours int) time.Time { return epoch.Add(time.Duration(hours) * time.Hour) }

func issue(t *testing.T, s *Store, g Grant) *Grant {
	t.Helper()
	stored, err := s.Issue(g)
	if err != nil {
		t.Fatalf("issue %s: %v", g.ID, err)
	}
	return stored
}

func rootGrant(id string, actions, resources []string, from, to time.Time) Grant {
	return Grant{
		ID: id, Issuer: "root", Subject: "alice",
		Actions: actions, Resources: resources,
		NotBefore: from, NotAfter: to,
	}
}

func childGrant(id string, parent *Grant, actions, resources []string, from, to time.Time) Grant {
	return Grant{
		ID: id, Issuer: parent.Subject, Subject: "bob",
		Actions: actions, Resources: resources,
		NotBefore: from, NotAfter: to,
		Parents: []GrantRef{parent.Ref()},
	}
}

func requireValid(t *testing.T, v Verdict) {
	t.Helper()
	if !v.Valid {
		t.Fatalf("expected valid, got %s", v)
	}
}

func requireInvalid(t *testing.T, v Verdict, reason ReasonCode, chainLen int) {
	t.Helper()
	if v.Valid {
		t.Fatalf("expected invalid (%s), got valid", reason)
	}
	if v.Reason != reason {
		t.Fatalf("expected reason %s, got %s (%s)", reason, v.Reason, v.Detail)
	}
	if len(v.Chain) != chainLen {
		t.Fatalf("expected chain length %d, got %d (%v)", chainLen, len(v.Chain), v.Chain)
	}
}

func TestValidDelegationChain(t *testing.T) {
	s := NewStore()
	root := issue(t, s, rootGrant("root", []string{"read", "write"}, []string{"fs:/data/*"}, at(0), at(100)))
	mid := issue(t, s, childGrant("mid", root, []string{"read", "write"}, []string{"fs:/data/*"}, at(10), at(90)))
	leaf := issue(t, s, childGrant("leaf", mid, []string{"read"}, []string{"fs:/data/*"}, at(20), at(80)))

	requireValid(t, s.Validate(root.Ref(), at(50)))
	requireValid(t, s.Validate(mid.Ref(), at(50)))
	requireValid(t, s.Validate(leaf.Ref(), at(50)))
}

func TestChildExceedsParent(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(g *Grant)
		reason ReasonCode
	}{
		{"action", func(g *Grant) { g.Actions = []string{"read", "delete"} }, ReasonActionExceedsParent},
		{"resource", func(g *Grant) { g.Resources = []string{"fs:/secret"} }, ReasonResourceExceedsParent},
		{"starts-earlier", func(g *Grant) { g.NotBefore = at(5) }, ReasonValidityExceedsParent},
		{"ends-later", func(g *Grant) { g.NotAfter = at(95) }, ReasonValidityExceedsParent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStore()
			root := issue(t, s, rootGrant("root", []string{"read"}, []string{"fs:/data/*"}, at(10), at(90)))
			child := childGrant("child", root, []string{"read"}, []string{"fs:/data/*"}, at(20), at(80))
			tc.mutate(&child)
			issued := issue(t, s, child)
			v := s.Validate(issued.Ref(), at(50))
			requireInvalid(t, v, tc.reason, 1)
			if v.Chain[0] != issued.Ref() {
				t.Fatalf("chain should start at the offending grant, got %v", v.Chain)
			}
		})
	}
}

func TestRevocationCascadesImmediately(t *testing.T) {
	s := NewStore()
	root := issue(t, s, rootGrant("root", []string{"*"}, []string{"*"}, at(0), at(100)))
	mid := issue(t, s, childGrant("mid", root, []string{"read"}, []string{"fs:/a"}, at(0), at(100)))
	leaf := issue(t, s, childGrant("leaf", mid, []string{"read"}, []string{"fs:/a"}, at(0), at(100)))

	if err := s.Revoke(root.Ref(), at(50)); err != nil {
		t.Fatal(err)
	}

	// Descendants are invalid at the revocation instant.
	requireInvalid(t, s.Validate(leaf.Ref(), at(50)), ReasonRevoked, 3)
	requireInvalid(t, s.Validate(mid.Ref(), at(50)), ReasonRevoked, 2)
	requireInvalid(t, s.Validate(root.Ref(), at(50)), ReasonRevoked, 1)

	// Historical queries before the revocation still pass.
	requireValid(t, s.Validate(leaf.Ref(), at(49)))
	requireValid(t, s.Validate(root.Ref(), at(49)))
}

func TestShortestReasonChain(t *testing.T) {
	// leaf delegates from two parents: a long healthy chain and a short
	// broken one. The reported reason chain must be the shortest.
	s := NewStore()
	deep := issue(t, s, rootGrant("deep", []string{"*"}, []string{"*"}, at(0), at(100)))
	mid := issue(t, s, childGrant("mid", deep, []string{"read"}, []string{"fs:/a"}, at(0), at(100)))
	shallow := issue(t, s, rootGrant("shallow", []string{"*"}, []string{"*"}, at(0), at(100)))

	leaf := issue(t, s, Grant{
		ID: "leaf", Issuer: "alice", Subject: "bob",
		Actions: []string{"read"}, Resources: []string{"fs:/a"},
		NotBefore: at(0), NotAfter: at(100),
		Parents: []GrantRef{mid.Ref(), shallow.Ref()},
	})

	if err := s.Revoke(shallow.Ref(), at(10)); err != nil {
		t.Fatal(err)
	}
	v := s.Validate(leaf.Ref(), at(20))
	requireInvalid(t, v, ReasonRevoked, 2)
	if v.Chain[1] != shallow.Ref() {
		t.Fatalf("expected shortest chain through shallow, got %v", v.Chain)
	}
}

func TestSameNameReissueGenerations(t *testing.T) {
	s := NewStore()
	gen1 := issue(t, s, rootGrant("cap", []string{"read"}, []string{"fs:/a"}, at(0), at(100)))
	child := issue(t, s, childGrant("child", gen1, []string{"read"}, []string{"fs:/a"}, at(0), at(100)))

	if gen1.Generation != 1 {
		t.Fatalf("expected generation 1, got %d", gen1.Generation)
	}
	if err := s.Revoke(gen1.Ref(), at(40)); err != nil {
		t.Fatal(err)
	}

	// Re-issue under the same name: a new generation, not a resurrection.
	gen2 := issue(t, s, rootGrant("cap", []string{"read"}, []string{"fs:/a"}, at(40), at(100)))
	if gen2.Generation != 2 {
		t.Fatalf("expected generation 2, got %d", gen2.Generation)
	}

	// The old child pins generation 1 and stays invalid after re-issue.
	requireInvalid(t, s.Validate(child.Ref(), at(50)), ReasonRevoked, 2)
	// The new generation is independently valid.
	requireValid(t, s.Validate(gen2.Ref(), at(50)))
	// A new child pinned to generation 2 is valid.
	child2 := issue(t, s, childGrant("child2", gen2, []string{"read"}, []string{"fs:/a"}, at(40), at(100)))
	requireValid(t, s.Validate(child2.Ref(), at(50)))
	// History: generation 1 was still good before its revocation.
	requireValid(t, s.Validate(child.Ref(), at(30)))
}

func TestMissingParentChain(t *testing.T) {
	s := NewStore()
	ghost := GrantRef{ID: "ghost", Generation: 1}
	mid := issue(t, s, Grant{
		ID: "mid", Issuer: "alice", Subject: "bob",
		Actions: []string{"read"}, Resources: []string{"fs:/a"},
		NotBefore: at(0), NotAfter: at(100),
		Parents: []GrantRef{ghost},
	})
	leaf := issue(t, s, childGrant("leaf", mid, []string{"read"}, []string{"fs:/a"}, at(0), at(100)))

	v := s.Validate(leaf.Ref(), at(50))
	requireInvalid(t, v, ReasonParentMissing, 3)
	if v.Chain[2] != ghost {
		t.Fatalf("chain should end at the missing parent, got %v", v.Chain)
	}

	// Querying a grant that was never issued at all.
	v = s.Validate(GrantRef{ID: "nope", Generation: 1}, at(50))
	requireInvalid(t, v, ReasonGrantNotFound, 1)
}

func TestHistoricalQueryAroundExpiry(t *testing.T) {
	s := NewStore()
	root := issue(t, s, rootGrant("root", []string{"read"}, []string{"fs:/a"}, at(10), at(20)))

	requireInvalid(t, s.Validate(root.Ref(), at(5)), ReasonNotYetValid, 1)
	requireValid(t, s.Validate(root.Ref(), at(15)))
	requireInvalid(t, s.Validate(root.Ref(), at(25)), ReasonExpired, 1)
}

func TestCyclicDelegation(t *testing.T) {
	s := NewStore()
	a := issue(t, s, rootGrant("a", []string{"read"}, []string{"fs:/a"}, at(0), at(100)))
	b := issue(t, s, childGrant("b", a, []string{"read"}, []string{"fs:/a"}, at(0), at(100)))
	// Rewire a to delegate from b, creating a cycle.
	a.Parents = []GrantRef{b.Ref()}

	v := s.Validate(a.Ref(), at(50))
	requireInvalid(t, v, ReasonCyclicDelegation, 3)
}
