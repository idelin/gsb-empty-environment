package netsec

import "testing"

const (
	actRead  Action = "read"
	actWrite Action = "write"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := NewEngine(
		[]Action{actRead, actWrite},
		NewIntervalSet(iv(0, 1000)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func basePolicies() (Policy, Policy) {
	identity := Policy{
		Name: "identity",
		Statements: []Statement{
			{
				Name:      "read-wide",
				Effect:    EffectAllow,
				Actions:   []Action{actRead},
				Resources: NewIntervalSet(iv(1, 100)),
			},
			{
				Name:      "block-sensitive",
				Effect:    EffectDeny,
				Actions:   []Action{actRead, actWrite},
				Resources: NewIntervalSet(iv(90, 95)),
			},
		},
	}
	resource := Policy{
		Name: "resource",
		Statements: []Statement{
			{
				Name:      "write-tail",
				Effect:    EffectAllow,
				Actions:   []Action{actWrite},
				Resources: NewIntervalSet(iv(50, 200)),
			},
		},
	}
	return identity, resource
}

func TestEvaluateAllowWithLayerIntersections(t *testing.T) {
	engine := testEngine(t)
	identity, resource := basePolicies()
	in := EvalInput{
		Action:         actRead,
		Resource:       60,
		IdentityPolicy: identity,
		ResourcePolicy: resource,
		Layers: []Layer{
			{NodeID: "root", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 80))},
			{NodeID: "ou-a", Effect: EffectAllow, Resources: NewIntervalSet(iv(10, 70))},
			{NodeID: "acct", Effect: EffectAllow, Resources: NewIntervalSet(iv(20, 60))},
		},
	}
	decision, err := engine.Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Fatalf("resource 60 should be allowed, got deny %+v", decision.EarliestDeny)
	}
	wantEffective := NewIntervalSet(iv(20, 60))
	if !decision.EffectiveAllow.Equal(wantEffective) {
		t.Fatalf("effective = %v, want %v", decision.EffectiveAllow.Ranges(), wantEffective.Ranges())
	}
	if len(decision.Layers) != 3 {
		t.Fatalf("got %d layer contributions, want 3", len(decision.Layers))
	}
	if !decision.Layers[0].AllowedAfter.Equal(NewIntervalSet(iv(1, 80))) {
		t.Errorf("root contribution = %v", decision.Layers[0].AllowedAfter.Ranges())
	}
	if !decision.Layers[1].AllowedAfter.Equal(NewIntervalSet(iv(10, 70))) {
		t.Errorf("ou-a contribution = %v", decision.Layers[1].AllowedAfter.Ranges())
	}
	if !decision.Layers[2].PointInside {
		t.Error("final layer should still contain resource 60")
	}
}

func TestEvaluateAncestorDenyOverridesDescendantAllow(t *testing.T) {
	engine := testEngine(t)
	identity, resource := basePolicies()
	in := EvalInput{
		Action:         actRead,
		Resource:       40,
		IdentityPolicy: identity,
		ResourcePolicy: resource,
		Layers: []Layer{
			{NodeID: "root", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1000))},
			{NodeID: "ou-a", Effect: EffectDeny, Resources: NewIntervalSet(iv(30, 50))},
			{NodeID: "acct", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1000))},
		},
	}
	decision, err := engine.Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed {
		t.Fatal("ancestor deny must win over descendant allow")
	}
	if decision.EarliestDeny == nil {
		t.Fatal("expected deny evidence")
	}
	if decision.EarliestDeny.Reason != ReasonBoundaryDeny ||
		decision.EarliestDeny.Source != "ou-a" ||
		decision.EarliestDeny.Depth != 1 {
		t.Fatalf("unexpected evidence %+v", decision.EarliestDeny)
	}
	if !decision.Layers[2].AllowedAfter.Equal(NewIntervalSet(iv(1, 29), iv(51, 89), iv(96, 100))) {
		t.Fatalf("deny should persist through descendant layer: %v",
			decision.Layers[2].AllowedAfter.Ranges())
	}
}

func TestEvaluateEmptyAllowLayerDeniesEverythingBelow(t *testing.T) {
	engine := testEngine(t)
	identity, resource := basePolicies()
	in := EvalInput{
		Action:         actRead,
		Resource:       40,
		IdentityPolicy: identity,
		ResourcePolicy: resource,
		Layers: []Layer{
			{NodeID: "root", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1000))},
			{NodeID: "ou-empty", Effect: EffectAllow, Resources: IntervalSet{}},
			{NodeID: "acct", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1000))},
		},
	}
	decision, err := engine.Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed {
		t.Fatal("empty allow layer must intersect the grant down to nothing")
	}
	if decision.EarliestDeny == nil ||
		decision.EarliestDeny.Reason != ReasonEmptyAllow ||
		decision.EarliestDeny.Source != "ou-empty" {
		t.Fatalf("unexpected evidence %+v", decision.EarliestDeny)
	}
	if !decision.Layers[1].AllowedAfter.Empty() || !decision.Layers[2].AllowedAfter.Empty() {
		t.Fatal("empty allow must stay empty for every descendant layer")
	}
}

func TestEvaluatePolicyDenyTakesPrecedenceAndIsEarliest(t *testing.T) {
	engine := testEngine(t)
	identity, resource := basePolicies()
	in := EvalInput{
		Action:         actRead,
		Resource:       92,
		IdentityPolicy: identity,
		ResourcePolicy: resource,
		Layers: []Layer{
			{NodeID: "root", Effect: EffectDeny, Resources: NewIntervalSet(iv(92, 92))},
		},
	}
	decision, err := engine.Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed {
		t.Fatal("denied resource must not be allowed")
	}
	if decision.EarliestDeny == nil ||
		decision.EarliestDeny.Reason != ReasonPolicyDeny ||
		decision.EarliestDeny.Source != "identity/block-sensitive" {
		t.Fatalf("policy deny must be reported before boundary deny: %+v", decision.EarliestDeny)
	}
}

func TestEvaluateIdentityAndResourcePolicyUnion(t *testing.T) {
	engine := testEngine(t)
	identity, resource := basePolicies()
	in := EvalInput{
		Action:         actWrite,
		Resource:       120,
		IdentityPolicy: identity,
		ResourcePolicy: resource,
		Layers: []Layer{
			{NodeID: "root", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1000))},
		},
	}
	decision, err := engine.Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Fatalf("resource-policy allow should union in, got %+v", decision.EarliestDeny)
	}
	found := false
	for _, contributor := range decision.AllowContributors {
		if contributor == "resource/write-tail" {
			found = true
		}
	}
	if !found {
		t.Fatalf("contributors missing resource policy: %v", decision.AllowContributors)
	}

	// Identity policy alone never allows write anywhere.
	identityOnly := in
	identityOnly.ResourcePolicy = Policy{Name: "resource"}
	denied, err := engine.Evaluate(identityOnly)
	if err != nil {
		t.Fatal(err)
	}
	if denied.Allowed || denied.EarliestDeny == nil ||
		denied.EarliestDeny.Reason != ReasonEmptyAllow {
		t.Fatalf("write without resource-policy allow must be denied: %+v", denied.EarliestDeny)
	}
}

func TestEvaluateNoLayersKeepsPolicyUnion(t *testing.T) {
	engine := testEngine(t)
	identity, resource := basePolicies()
	decision, err := engine.Evaluate(EvalInput{
		Action:         actRead,
		Resource:       20,
		IdentityPolicy: identity,
		ResourcePolicy: resource,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Fatalf("no boundary layers means the policy union stands: %+v", decision.EarliestDeny)
	}
	if len(decision.Layers) != 0 {
		t.Fatalf("expected no layer contributions, got %d", len(decision.Layers))
	}
}

func TestEvaluateRejectsUnknownActionAndOutOfUniverse(t *testing.T) {
	engine := testEngine(t)
	identity, resource := basePolicies()
	base := EvalInput{
		Action:         Action("delete"),
		Resource:       1,
		IdentityPolicy: identity,
		ResourcePolicy: resource,
	}
	if _, err := engine.Evaluate(base); err == nil {
		t.Fatal("unknown action must be rejected")
	}
	base.Action = actRead
	base.Resource = 5000
	if _, err := engine.Evaluate(base); err == nil {
		t.Fatal("resource outside the universe must be rejected")
	}
}

func TestEvaluateInvalidStatementAndLayer(t *testing.T) {
	engine := testEngine(t)
	if _, err := NewEngine(nil, NewIntervalSet(iv(0, 1))); err == nil {
		t.Fatal("empty action set must be rejected")
	}
	identity, resource := basePolicies()
	identity.Statements[0].Effect = "maybe"
	if _, err := engine.Evaluate(EvalInput{
		Action:         actRead,
		Resource:       1,
		IdentityPolicy: identity,
		ResourcePolicy: resource,
	}); err == nil {
		t.Fatal("invalid statement effect must be rejected")
	}

	identity, resource = basePolicies()
	if _, err := engine.Evaluate(EvalInput{
		Action:         actRead,
		Resource:       1,
		IdentityPolicy: identity,
		ResourcePolicy: resource,
		Layers: []Layer{
			{NodeID: "root", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1))},
			{NodeID: "root", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1))},
		},
	}); err == nil {
		t.Fatal("duplicate layer node ids must be rejected")
	}
}

func TestCheckMirrorsEvaluate(t *testing.T) {
	engine := testEngine(t)
	identity, resource := basePolicies()
	in := EvalInput{
		Action:         actRead,
		Resource:       60,
		IdentityPolicy: identity,
		ResourcePolicy: resource,
		Layers: []Layer{
			{NodeID: "root", Effect: EffectAllow, Resources: NewIntervalSet(iv(0, 1000))},
		},
	}
	allowed, err := engine.Check(in)
	if err != nil || !allowed {
		t.Fatalf("Check = %v, %v; want true", allowed, err)
	}
}
