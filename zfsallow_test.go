package netsec

import (
	"strings"
	"testing"
)

func testTree() []Dataset {
	names := []string{
		"tank",
		"tank/home",
		"tank/home/alice",
		"tank/home/alice/data",
		"tank/home/alice/a",
		"tank/home/bob",
		"tank/home/bob/docs",
		"tank/scratch",
	}
	ds := make([]Dataset, 0, len(names))
	for _, name := range names {
		ds = append(ds, Dataset{Name: name})
	}
	return ds
}

func user(name string, groups ...string) Subject {
	return Subject{Type: SubjectUser, Name: name, Groups: groups}
}

func testGrants() []Grant {
	return []Grant{
		// Local-only grants never reach descendants.
		{Subject: SubjectUser, Name: "alice", Permission: PermCreate, Dataset: "tank/home", Scope: ScopeLocal},
		{Subject: SubjectUser, Name: "alice", Permission: PermMount, Dataset: "tank/home", Scope: ScopeLocal},
		// Descendant-only grants never apply to the carrying dataset itself.
		{Subject: SubjectUser, Name: "alice", Permission: PermSnapshot, Dataset: "tank/home", Scope: ScopeDescendants},
		// Both-direction grants stack everywhere below the carrier.
		{Subject: SubjectUser, Name: "alice", Permission: PermSend, Dataset: "tank", Scope: ScopeAll},
		// Group and everyone grants used by the later subtests.
		{Subject: SubjectGroup, Name: "storage", Permission: PermRename, Dataset: "tank/home", Scope: ScopeAll},
		{Subject: SubjectEveryone, Name: "everyone", Permission: PermMount, Dataset: "tank/home", Scope: ScopeAll},
		// Attribute delegations on alice's subtree.
		{Subject: SubjectUser, Name: "alice", Permission: "sharenfs", Dataset: "tank/home/alice", Scope: ScopeAll},
		{Subject: SubjectUser, Name: "alice", Permission: "canmount", Dataset: "tank/home/alice", Scope: ScopeAll},
		{Subject: SubjectUser, Name: "alice", Permission: PermSetuid, Dataset: "tank/home/alice", Scope: ScopeAll},
		// bob can create under his own subtree.
		{Subject: SubjectUser, Name: "bob", Permission: PermCreate, Dataset: "tank/home/bob", Scope: ScopeAll},
		{Subject: SubjectUser, Name: "bob", Permission: PermMount, Dataset: "tank/home/bob", Scope: ScopeAll},
		{Subject: SubjectUser, Name: "bob", Permission: PermSetuid, Dataset: "tank/home/bob", Scope: ScopeAll},
	}
}

func newTestAuthorizer(t *testing.T) *Authorizer {
	t.Helper()
	a, err := NewAuthorizer(testTree(), testGrants())
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}
	return a
}

func TestLocalOnlyGrantDoesNotReachDescendants(t *testing.T) {
	a := newTestAuthorizer(t)

	// The local create+mount grants work on tank/home itself.
	createOnCarrier := a.Authorize(Request{
		Subject:   user("alice"),
		Operation: OpCreate,
		Dataset:   "tank/home/newset",
	})
	if !createOnCarrier.Allowed {
		t.Fatalf("create on grant carrier denied: %v", createOnCarrier.Reasons)
	}

	// The same local-only grant must not authorize creation one level down.
	createUnderChild := a.Authorize(Request{
		Subject:   user("alice"),
		Operation: OpCreate,
		Dataset:   "tank/home/alice/newset",
	})
	if createUnderChild.Allowed {
		t.Fatalf("local-only create grant reached descendant")
	}

	// Snapshot is descendant-only: denied on the carrier, allowed below it.
	snapCarrier := a.Authorize(Request{
		Subject:   user("alice"),
		Operation: OpSnapshot,
		Dataset:   "tank/home",
	})
	if snapCarrier.Allowed {
		t.Fatalf("descendant-only snapshot grant applied to carrier")
	}
	snapChild := a.Authorize(Request{
		Subject:   user("alice"),
		Operation: OpSnapshot,
		Dataset:   "tank/home/alice",
	})
	if !snapChild.Allowed {
		t.Fatalf("snapshot on descendant denied: %v", snapChild.Reasons)
	}

	// The all-scoped send grant on tank stacks onto every descendant.
	eff, err := a.EffectivePermissions(user("alice"), "tank/home/bob/docs")
	if err != nil {
		t.Fatalf("EffectivePermissions: %v", err)
	}
	if !eff[PermSend] {
		t.Fatalf("ancestor all-scoped send grant did not stack onto deep descendant")
	}
	if eff[PermCreate] {
		t.Fatalf("local-only create grant leaked into descendant permission set")
	}
}

func TestCrossParentRenameRequiresRenameAndMountOnBothParents(t *testing.T) {
	a := newTestAuthorizer(t)

	// alice holds rename (via group) + mount (everyone) on tank/home, but the
	// destination parent tank/scratch has neither delegation: denied.
	escape := a.Authorize(Request{
		Subject:     user("alice", "storage"),
		Operation:   OpRename,
		Source:      "tank/home/alice/data",
		Destination: "tank/scratch/data",
	})
	if escape.Allowed {
		t.Fatalf("rename across parents without grants on destination parent allowed")
	}

	// Same-prefix move inside tank/home satisfies both parents: allowed.
	inside := a.Authorize(Request{
		Subject:     user("alice", "storage"),
		Operation:   OpRename,
		Source:      "tank/home/alice/data",
		Destination: "tank/home/bob/data",
	})
	if !inside.Allowed {
		t.Fatalf("rename with rename+mount on both parents denied: %v", inside.Reasons)
	}

	// A user outside the storage group lacks rename even though mount is
	// delegated to everyone.
	noRename := a.Authorize(Request{
		Subject:     user("mallory"),
		Operation:   OpRename,
		Source:      "tank/home/alice/data",
		Destination: "tank/home/bob/data",
	})
	if noRename.Allowed {
		t.Fatalf("rename without rename permission allowed")
	}
	if !contains(noRename.Reasons, PermRename) {
		t.Fatalf("expected a missing rename reason, got %v", noRename.Reasons)
	}

	// Pool hops are rejected outright.
	crossPool := a.Authorize(Request{
		Subject:     user("alice", "storage"),
		Operation:   OpRename,
		Source:      "tank/home/alice/data",
		Destination: "other/home/alice/data",
	})
	if crossPool.Allowed || !contains(crossPool.Reasons, "pool") {
		t.Fatalf("cross-pool rename not rejected, got allowed=%v reasons=%v", crossPool.Allowed, crossPool.Reasons)
	}
}

func TestSetuidAndMountGuardedProperties(t *testing.T) {
	a := newTestAuthorizer(t)

	// bob has create, mount and setuid on the bob subtree: creating a child
	// with setuid=on is permitted.
	allowedCreate := a.Authorize(Request{
		Subject:   user("bob"),
		Operation: OpCreate,
		Dataset:   "tank/home/bob/safe",
		Properties: []Property{
			{Name: PermSetuid, Value: "on"},
		},
	})
	if !allowedCreate.Allowed {
		t.Fatalf("setuid=on create with setuid delegation denied: %v", allowedCreate.Reasons)
	}

	// alice can create under tank/home (local grants) but the new child only
	// sees ancestor grants, and no setuid delegation reaches it: the explicit
	// create-time setuid rule must deny the request.
	escalation := a.Authorize(Request{
		Subject:   user("alice"),
		Operation: OpCreate,
		Dataset:   "tank/home/evil",
		Properties: []Property{
			{Name: PermSetuid, Value: "on"},
		},
	})
	if escalation.Allowed {
		t.Fatalf("create with setuid=on without setuid delegation allowed")
	}
	if !contains(escalation.Reasons, "setuid") {
		t.Fatalf("expected a setuid reason, got %v", escalation.Reasons)
	}

	// setuid=off does not trigger the special on-rule but still needs the
	// setuid attribute permission.
	setuidOff := a.Authorize(Request{
		Subject:   user("alice"),
		Operation: OpCreate,
		Dataset:   "tank/home/quiet",
		Properties: []Property{
			{Name: PermSetuid, Value: "off"},
		},
	})
	if setuidOff.Allowed {
		t.Fatalf("setuid=off create should still need the setuid attribute permission")
	}

	// sharenfs on alice's dataset requires both sharenfs and mount; alice has
	// both on that dataset.
	share := a.Authorize(Request{
		Subject:   user("alice"),
		Operation: OpSet,
		Dataset:   "tank/home/alice",
		Properties: []Property{
			{Name: "sharenfs", Value: "on"},
		},
	})
	if !share.Allowed {
		t.Fatalf("sharenfs set with sharenfs+mount denied: %v", share.Reasons)
	}

	// bob holds create+mount under tank/home/bob but no sharenfs delegation:
	// the sharenfs escape is blocked even though mount is present.
	shareEscape := a.Authorize(Request{
		Subject:   user("bob"),
		Operation: OpSet,
		Dataset:   "tank/home/bob/docs",
		Properties: []Property{
			{Name: "sharenfs", Value: "on"},
		},
	})
	if shareEscape.Allowed || !contains(shareEscape.Reasons, "sharenfs") {
		t.Fatalf("sharenfs change without sharenfs permission not rejected, got %v", shareEscape.Reasons)
	}

	// canmount without the mount permission must fail on both requirements.
	noMount := a.Authorize(Request{
		Subject:   user("mallory"),
		Operation: OpSet,
		Dataset:   "tank/home/alice",
		Properties: []Property{
			{Name: "canmount", Value: "off"},
		},
	})
	if noMount.Allowed || !contains(noMount.Reasons, PermMount) {
		t.Fatalf("canmount change without mount permission not rejected, got %v", noMount.Reasons)
	}
}

func TestCompliantOperationsAllowed(t *testing.T) {
	a := newTestAuthorizer(t)

	cases := []struct {
		name string
		req  Request
	}{
		{
			name: "plain create on parent",
			req: Request{
				Subject:   user("alice"),
				Operation: OpCreate,
				Dataset:   "tank/home/newset",
			},
		},
		{
			name: "snapshot on delegated descendant",
			req: Request{
				Subject:   user("alice"),
				Operation: OpSnapshot,
				Dataset:   "tank/home/alice",
			},
		},
		{
			name: "send stacked from tank grant",
			req: Request{
				Subject:   user("alice"),
				Operation: OpSend,
				Dataset:   "tank/home/bob/docs",
			},
		},
		{
			name: "bob creates child with setuid on",
			req: Request{
				Subject:   user("bob"),
				Operation: OpCreate,
				Dataset:   "tank/home/bob/work",
				Properties: []Property{
					{Name: PermSetuid, Value: "on"},
				},
			},
		},
		{
			name: "rename within delegated parent",
			req: Request{
				Subject:     user("alice", "storage"),
				Operation:   OpRename,
				Source:      "tank/home/alice/a",
				Destination: "tank/home/alice/b",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := a.Authorize(tc.req)
			if !decision.Allowed {
				t.Fatalf("compliant operation denied: %v", decision.Reasons)
			}
		})
	}
}

func TestSubjectMatchingAndValidation(t *testing.T) {
	a := newTestAuthorizer(t)

	// everyone grant matches any user.
	eff, err := a.EffectivePermissions(user("unknown"), "tank/home/alice")
	if err != nil {
		t.Fatalf("EffectivePermissions: %v", err)
	}
	if !eff[PermMount] {
		t.Fatalf("everyone mount grant did not match arbitrary user")
	}

	// Group grant matches via group membership.
	effGroup, err := a.EffectivePermissions(user("carol", "storage"), "tank/home/alice")
	if err != nil {
		t.Fatalf("EffectivePermissions: %v", err)
	}
	if !effGroup[PermRename] {
		t.Fatalf("group rename grant did not match group member")
	}

	if _, err := NewAuthorizer(testTree(), []Grant{
		{Subject: SubjectUser, Name: "x", Permission: PermMount, Dataset: "tank/missing", Scope: ScopeAll},
	}); err == nil {
		t.Fatalf("grant on unknown dataset accepted")
	}
	if _, err := NewAuthorizer([]Dataset{{Name: "tank/orphan/child"}}, nil); err == nil {
		t.Fatalf("broken tree accepted")
	}
	if _, err := NewAuthorizer([]Dataset{{Name: "tank"}}, []Grant{
		{Subject: SubjectUser, Name: "x", Permission: PermMount, Dataset: "tank", Scope: Scope("weird")},
	}); err == nil {
		t.Fatalf("grant with unknown scope accepted")
	}

	unknownOp := a.Authorize(Request{Subject: user("alice"), Operation: Operation("frob"), Dataset: "tank/home"})
	if unknownOp.Allowed {
		t.Fatalf("unknown operation allowed")
	}
}

func contains(reasons []string, fragment string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, fragment) {
			return true
		}
	}
	return false
}

func TestEdgeCasesAndConvenienceWrappers(t *testing.T) {
	// Package-level wrapper returns a decision and an error independently.
	decision, err := Authorize(testTree(), testGrants(), Request{
		Subject:   user("alice"),
		Operation: OpSnapshot,
		Dataset:   "tank/home/alice",
	})
	if err != nil || !decision.Allowed {
		t.Fatalf("package Authorize failed: err=%v decision=%+v", err, decision)
	}
	eff, err := EffectivePermissions(testTree(), testGrants(), user("alice"), "tank/home")
	if err != nil || !eff[PermCreate] {
		t.Fatalf("package EffectivePermissions failed: err=%v eff=%v", err, eff)
	}

	a := newTestAuthorizer(t)

	// Invalid subjects and requests targeting missing datasets are denied.
	invalidSubject := a.Authorize(Request{
		Subject:   Subject{Type: SubjectType("robot"), Name: "r2"},
		Operation: OpSend,
		Dataset:   "tank/home",
	})
	if invalidSubject.Allowed {
		t.Fatalf("request with invalid subject allowed")
	}
	missingTarget := a.Authorize(Request{
		Subject:   user("alice"),
		Operation: OpSnapshot,
		Dataset:   "tank/home/ghost",
	})
	if missingTarget.Allowed {
		t.Fatalf("request on missing dataset allowed")
	}

	// Create without a target and create of a top-level dataset are rejected.
	emptyCreate := a.Authorize(Request{Subject: user("alice"), Operation: OpCreate})
	if emptyCreate.Allowed {
		t.Fatalf("create without target allowed")
	}
	topCreate := a.Authorize(Request{
		Subject:   user("alice"),
		Operation: OpCreate,
		Dataset:   "newpool",
	})
	if topCreate.Allowed {
		t.Fatalf("top-level create allowed")
	}

	// Rename without endpoints and rename of a pool-level dataset are rejected.
	badRename := a.Authorize(Request{Subject: user("alice"), Operation: OpRename})
	if badRename.Allowed {
		t.Fatalf("rename without endpoints allowed")
	}
	poolRename := a.Authorize(Request{
		Subject:     user("alice"),
		Operation:   OpRename,
		Source:      "tank",
		Destination: "tank2",
	})
	if poolRename.Allowed {
		t.Fatalf("pool-level rename allowed")
	}

	// Set on a missing dataset is rejected before attribute evaluation.
	missingSet := a.Authorize(Request{
		Subject:   user("alice"),
		Operation: OpSet,
		Dataset:   "tank/home/ghost",
		Properties: []Property{
			{Name: "sharenfs", Value: "on"},
		},
	})
	if missingSet.Allowed {
		t.Fatalf("set on missing dataset allowed")
	}

	// Repeated calls exercise the memoized permission cache and stay stable.
	for i := 0; i < 3; i++ {
		d := a.Authorize(Request{
			Subject:   user("alice"),
			Operation: OpMount,
			Dataset:   "tank/home",
		})
		if !d.Allowed {
			t.Fatalf("cached mount decision unstable on iteration %d", i)
		}
	}

	// A group-typed subject matches group grants directly.
	groupEff, err := a.EffectivePermissions(Subject{Type: SubjectGroup, Name: "storage"}, "tank/home/bob")
	if err != nil {
		t.Fatalf("EffectivePermissions: %v", err)
	}
	if !groupEff[PermRename] {
		t.Fatalf("group subject did not receive group-scoped rename")
	}

	// Constructor rejects malformed names, duplicate datasets and bad grants.
	if _, err := NewAuthorizer([]Dataset{{Name: "/tank/bad"}}, nil); err == nil {
		t.Fatalf("malformed dataset name accepted")
	}
	if _, err := NewAuthorizer(
		[]Dataset{{Name: "tank"}, {Name: "tank"}}, nil,
	); err == nil {
		t.Fatalf("duplicate dataset accepted")
	}
	if _, err := NewAuthorizer([]Dataset{{Name: "tank"}}, []Grant{
		{Subject: SubjectType("nobody"), Name: "x", Permission: PermMount, Dataset: "tank", Scope: ScopeAll},
	}); err == nil {
		t.Fatalf("grant with bad subject type accepted")
	}
	if _, err := NewAuthorizer([]Dataset{{Name: "tank"}}, []Grant{
		{Subject: SubjectUser, Name: "x", Permission: "", Dataset: "tank", Scope: ScopeAll},
	}); err == nil {
		t.Fatalf("grant with empty permission accepted")
	}
}
