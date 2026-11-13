package netsec

import (
	"fmt"
	"testing"
)

const (
	arnAdmin   = "arn:aws:iam::123456789012:role/Admin"
	arnOps     = "arn:aws:iam::123456789012:role/Ops"
	arnAudit   = "arn:aws:iam::123456789012:role/Audit"
	arnSecret  = "arn:aws:iam::123456789012:role/Secret"
	arnDeep    = "arn:aws:iam::123456789012:role/Deep"
	arnFourth  = "arn:aws:iam::123456789012:role/Fourth"
	arnNoTrust = "arn:aws:iam::123456789012:role/NoLambdaTrust"
)

func lambdaTrust() TrustPolicy {
	return TrustPolicy{Statements: []TrustStatement{{
		Effect:   EffectAllow,
		Services: []string{ServiceLambda},
	}}}
}

func ec2Trust() TrustPolicy {
	return TrustPolicy{Statements: []TrustStatement{{
		Effect:   EffectAllow,
		Services: []string{"ec2.amazonaws.com"},
	}}}
}

func passAllow(resources ...string) Policy {
	return Policy{Statements: []Statement{{
		Effect:    EffectAllow,
		Actions:   []string{ActionPassRole},
		Resources: resources,
	}}}
}

func passDeny(resources ...string) Policy {
	return Policy{Statements: []Statement{{
		Effect:    EffectDeny,
		Actions:   []string{ActionPassRole},
		Resources: resources,
	}}}
}

func lambdaAdminPolicy() Policy {
	return Policy{Statements: []Statement{{
		Effect:    EffectAllow,
		Actions:   []string{"lambda:*"},
		Resources: []string{"*"},
	}}}
}

func role(arn string, trust TrustPolicy, policies ...Policy) Role {
	return Role{ARN: arn, Trust: trust, Policies: policies}
}

func assertRoleSet(t *testing.T, got []Reachability, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("reachable count = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i, arn := range want {
		if got[i].RoleARN != arn {
			t.Fatalf("reachable[%d] = %q, want %q (full: %v)", i, got[i].RoleARN, arn, got)
		}
	}
}

func TestReachableRolesWildcardResourceAndTwoLevelPass(t *testing.T) {
	entry := []Policy{
		lambdaAdminPolicy(),
		passAllow("arn:aws:iam::123456789012:role/Adm*"),
	}
	roles := []Role{
		role(arnAdmin, lambdaTrust(), lambdaAdminPolicy(), passAllow("*")),
		role(arnOps, lambdaTrust()),
	}

	got := ReachableRoles(entry, roles)
	assertRoleSet(t, got, []string{arnAdmin, arnOps})

	if got[0].Depth != 1 {
		t.Fatalf("Admin depth = %d, want 1", got[0].Depth)
	}
	if got[1].Depth != 2 || len(got[1].Path) != 2 || got[1].Path[1] != arnOps {
		t.Fatalf("Ops reachability = %+v, want depth 2 through Admin", got[1])
	}
}

func TestExplicitDenyOverridesAllow(t *testing.T) {
	entry := []Policy{
		lambdaAdminPolicy(),
		passAllow("*"),
		passDeny(arnSecret),
	}
	roles := []Role{
		role(arnSecret, lambdaTrust()),
		role(arnAudit, lambdaTrust()),
	}

	got := ReachableRoles(entry, roles)
	assertRoleSet(t, got, []string{arnAudit})

	if CanPassRole(entry, arnSecret, ServiceLambda) {
		t.Fatal("Deny on Secret must win over wildcard Allow")
	}
}

func TestTrustWithoutLambdaIsNotReachable(t *testing.T) {
	entry := []Policy{lambdaAdminPolicy(), passAllow("*")}
	roles := []Role{
		role(arnNoTrust, ec2Trust()),
		role(arnAudit, lambdaTrust()),
	}

	got := ReachableRoles(entry, roles)
	assertRoleSet(t, got, []string{arnAudit})

	if roles[0].TrustsService(ServiceLambda) {
		t.Fatal("EC2-only trust must not be assumed by Lambda")
	}
}

func TestChainStopsAtThreePasses(t *testing.T) {
	entry := []Policy{lambdaAdminPolicy(), passAllow(arnAdmin)}
	roles := []Role{
		role(arnAdmin, lambdaTrust(), lambdaAdminPolicy(), passAllow(arnOps)),
		role(arnOps, lambdaTrust(), lambdaAdminPolicy(), passAllow(arnDeep)),
		role(arnDeep, lambdaTrust(), lambdaAdminPolicy(), passAllow(arnFourth)),
		role(arnFourth, lambdaTrust()),
	}

	got := ReachableRoles(entry, roles)
	assertRoleSet(t, got, []string{arnAdmin, arnDeep, arnOps})

	depthByARN := map[string]int{}
	for _, reachable := range got {
		depthByARN[reachable.RoleARN] = reachable.Depth
	}
	if depthByARN[arnAdmin] != 1 || depthByARN[arnOps] != 2 || depthByARN[arnDeep] != 3 {
		t.Fatalf("unexpected depths: %v", depthByARN)
	}
}

func TestPassedToServiceConditionScopedToService(t *testing.T) {
	lambdaScopedPass := Policy{Statements: []Statement{{
		Effect:    EffectAllow,
		Actions:   []string{ActionPassRole},
		Resources: []string{"*"},
		Conditions: Conditions{
			PassedToService: []string{ServiceLambda},
		}},
	}}
	entry := []Policy{lambdaAdminPolicy(), lambdaScopedPass}
	roles := []Role{role(arnAdmin, lambdaTrust())}

	if !CanPassRole(entry, arnAdmin, ServiceLambda) {
		t.Fatal("lambda-scoped condition must permit passing to Lambda")
	}
	if CanPassRole(entry, arnAdmin, "ec2.amazonaws.com") {
		t.Fatal("lambda-scoped condition must not permit passing to EC2")
	}
	assertRoleSet(t, ReachableRoles(entry, roles), []string{arnAdmin})

	ec2ScopedDeny := Policy{Statements: []Statement{{
		Effect:    EffectDeny,
		Actions:   []string{ActionPassRole},
		Resources: []string{arnAdmin},
		Conditions: Conditions{
			PassedToService: []string{"ec2.amazonaws.com"},
		}},
	}}
	withScopedDeny := append(entry, ec2ScopedDeny)
	if !CanPassRole(withScopedDeny, arnAdmin, ServiceLambda) {
		t.Fatal("EC2-scoped Deny must not block a Lambda pass")
	}
	if CanPassRole(withScopedDeny, arnAdmin, "ec2.amazonaws.com") {
		t.Fatal("EC2-scoped Deny must block an EC2 pass")
	}

	globScoped := Policy{Statements: []Statement{{
		Effect:     EffectAllow,
		Actions:    []string{ActionPassRole},
		Resources:  []string{"*"},
		Conditions: Conditions{PassedToService: []string{"*.amazonaws.com"}},
	}}}
	if !CanPassRole([]Policy{globScoped}, arnAdmin, ServiceLambda) {
		t.Fatal("wildcard service condition must match Lambda")
	}
}

func TestLambdaCreateAndInvokeBothRequired(t *testing.T) {
	createOnly := Policy{Statements: []Statement{{
		Effect:    EffectAllow,
		Actions:   []string{ActionCreateFunction},
		Resources: []string{"*"},
	}}}
	entry := []Policy{createOnly, passAllow("*")}
	roles := []Role{role(arnAdmin, lambdaTrust())}

	if CanCreateAndInvokeLambda(entry) {
		t.Fatal("CreateFunction without InvokeFunction is insufficient")
	}
	assertRoleSet(t, ReachableRoles(entry, roles), nil)

	invokeDeny := Policy{Statements: []Statement{{
		Effect:    EffectDeny,
		Actions:   []string{ActionInvokeFunction},
		Resources: []string{"*"},
	}}}
	blocked := []Policy{lambdaAdminPolicy(), invokeDeny, passAllow("*")}
	if CanCreateAndInvokeLambda(blocked) {
		t.Fatal("explicit Deny on InvokeFunction must win")
	}
	assertRoleSet(t, ReachableRoles(blocked, roles), nil)
}

func TestActionAndResourceWildcards(t *testing.T) {
	entry := []Policy{
		lambdaAdminPolicy(),
		passAllow("arn:aws:iam::123456789012:role/A*"),
	}
	roles := []Role{
		role(arnAudit, lambdaTrust()),
		role(arnOps, lambdaTrust()),
	}

	assertRoleSet(t, ReachableRoles(entry, roles), []string{arnAudit})

	wildcardAction := []Policy{{Statements: []Statement{{
		Effect:    EffectAllow,
		Actions:   []string{"iam:*"},
		Resources: []string{"*"},
	}}}}
	if !CanPassRole(wildcardAction, arnOps, ServiceLambda) {
		t.Fatal("iam:* action wildcard must cover iam:PassRole")
	}
}

func TestCyclesVisitEachRoleOnce(t *testing.T) {
	entry := []Policy{lambdaAdminPolicy(), passAllow("*")}
	roles := []Role{
		role(arnAdmin, lambdaTrust(), lambdaAdminPolicy(), passAllow("*")),
		role(arnOps, lambdaTrust(), lambdaAdminPolicy(), passAllow("*")),
	}

	got := ReachableRoles(entry, roles)
	assertRoleSet(t, got, []string{arnAdmin, arnOps})
	for _, reachable := range got {
		if reachable.Depth < 1 || reachable.Depth > MaxPassDepth {
			t.Fatalf("role %q reported with invalid depth %d", reachable.RoleARN, reachable.Depth)
		}
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern string
		value   string
		want    bool
	}{
		{"*", "arn:aws:iam::1234:role/Admin", true},
		{"arn:*:role/Admin", "arn:aws:iam::1234:role/Admin", true},
		{"role/Admin?", "role/Admins", true},
		{"role/Admin?", "role/Admin", false},
		{"role/A*", "role/Audit", true},
		{"role/A*", "role/Ops", false},
		{"lambda.amazonaws.com", "lambda.amazonaws.com", true},
		{"lambda.amazonaws.com", "ec2.amazonaws.com", false},
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.value); got != tc.want {
			t.Fatalf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.value, got, tc.want)
		}
	}
}

func ExampleReachableRoles() {
	entryPolicies := []Policy{
		lambdaAdminPolicy(),
		passAllow(arnAdmin),
	}
	roles := []Role{
		role(arnAdmin, lambdaTrust(), lambdaAdminPolicy(), passAllow(arnOps)),
		role(arnOps, lambdaTrust()),
	}

	for _, reachable := range ReachableRoles(entryPolicies, roles) {
		fmt.Printf("%s depth=%d\n", reachable.RoleARN, reachable.Depth)
	}
	// Output:
	// arn:aws:iam::123456789012:role/Admin depth=1
	// arn:aws:iam::123456789012:role/Ops depth=2
}
