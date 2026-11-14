package netsec

import (
	"fmt"
	"testing"
)

func TestPodsReadDoesNotGrantExec(t *testing.T) {
	rules := []Rule{{
		Namespace:    "prod",
		Resource:     ResourcePods,
		Verb:         VerbGet,
		ResourceName: "web-0",
	}}

	read := Request{
		Namespace:    "prod",
		Resource:     ResourcePods,
		Verb:         VerbGet,
		ResourceName: "web-0",
	}
	if !New(rules).Authorize(read) {
		t.Fatal("expected the explicitly declared pod read to be allowed")
	}

	exec := Request{
		Namespace:    "prod",
		Resource:     ResourcePods,
		Subresource:  SubresourceExec,
		Verb:         VerbCreate,
		ResourceName: "web-0",
	}
	if New(rules).Authorize(exec) {
		t.Fatal("a pods read rule must not authorize pods/exec")
	}
}

func TestSubresourcesAreModeledSeparately(t *testing.T) {
	base := Request{
		Namespace:    "prod",
		Resource:     ResourcePods,
		Verb:         VerbGet,
		ResourceName: "web-0",
	}
	execRule := Rule{
		Namespace:    "prod",
		Resource:     ResourcePods,
		Subresource:  SubresourceExec,
		Verb:         VerbCreate,
		ResourceName: "web-0",
	}

	requests := []struct {
		name string
		req  Request
	}{
		{"exec", Request{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceExec, Verb: VerbCreate, ResourceName: "web-0"}},
		{"attach", Request{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceAttach, Verb: VerbCreate, ResourceName: "web-0"}},
		{"portforward", Request{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourcePortForward, Verb: VerbCreate, ResourceName: "web-0"}},
		{"log", Request{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceLog, Verb: VerbGet, ResourceName: "web-0"}},
	}

	auth := New([]Rule{execRule})
	for _, tc := range requests {
		want := tc.name == "exec"
		if got := auth.Authorize(tc.req); got != want {
			t.Errorf("%s: got allowed=%v, want %v", tc.name, got, want)
		}
	}
	if auth.Authorize(base) {
		t.Fatal("an exec rule must not authorize the base pods resource")
	}

	// Each sibling must be granted explicitly; an exec grant never covers it.
	for _, tc := range requests {
		if tc.name == "exec" {
			continue
		}
		rule := Rule{
			Namespace:    "prod",
			Resource:     ResourcePods,
			Subresource:  tc.req.Subresource,
			Verb:         tc.req.Verb,
			ResourceName: tc.req.ResourceName,
		}
		if !New([]Rule{rule}).Authorize(tc.req) {
			t.Errorf("%s: expected an explicit rule to grant access", tc.name)
		}
	}
}

func TestWildcardsRequireExplicitDeclaration(t *testing.T) {
	req := Request{
		Namespace: "prod",
		Resource:  ResourcePods,
		Verb:      VerbGet,
	}

	// A broad resource/verb wildcard does not declare the subresource
	// dimension, so it remains a base-resource rule.
	baseWildcard := Rule{Namespace: "prod", Resource: Wildcard, Verb: Wildcard}
	if !New([]Rule{baseWildcard}).Authorize(req) {
		t.Fatal("explicit resource and verb wildcards should grant a base-resource read")
	}
	exec := Request{
		Namespace:   "prod",
		Resource:    ResourcePods,
		Subresource: SubresourceExec,
		Verb:        VerbCreate,
	}
	if New([]Rule{baseWildcard}).Authorize(exec) {
		t.Fatal("an absent subresource must not be widened by resource/verb wildcards")
	}

	// Only an explicitly written subresource wildcard covers subresources.
	subresourceWildcard := Rule{
		Namespace:   "prod",
		Resource:    Wildcard,
		Subresource: Wildcard,
		Verb:        Wildcard,
	}
	if !New([]Rule{subresourceWildcard}).Authorize(exec) {
		t.Fatal("an explicit subresource wildcard should cover pods/exec")
	}
	// The explicit subresource wildcard describes subresources, not the base
	// resource, which has its own empty-subresource dimension.
	if New([]Rule{subresourceWildcard}).Authorize(req) {
		t.Fatal("a subresource wildcard must not grant the base resource")
	}

	// APIGroup, namespace, and resource name wildcards also require an
	// explicit token.
	explicitDimensions := Rule{
		APIGroup:     Wildcard,
		Namespace:    Wildcard,
		Resource:     Wildcard,
		Subresource:  Wildcard,
		Verb:         Wildcard,
		ResourceName: Wildcard,
	}
	if !New([]Rule{explicitDimensions}).Authorize(exec) {
		t.Fatal("explicit wildcards in every dimension should grant access")
	}
}

func TestEmptyFieldsAreNotWildcards(t *testing.T) {
	rule := Rule{
		Resource: ResourcePods,
		Verb:     VerbGet,
	}

	cases := []struct {
		name string
		req  Request
	}{
		{"named request", Request{Resource: ResourcePods, Verb: VerbGet, ResourceName: "web-0"}},
		{"namespace request", Request{Namespace: "prod", Resource: ResourcePods, Verb: VerbGet}},
		{"api group request", Request{APIGroup: "apps", Resource: ResourcePods, Verb: VerbGet}},
		{"subresource request", Request{Resource: ResourcePods, Subresource: SubresourceExec, Verb: VerbGet}},
	}

	auth := New([]Rule{rule})
	for _, tc := range cases {
		if auth.Authorize(tc.req) {
			t.Errorf("%s: empty rule fields unexpectedly behaved as wildcards", tc.name)
		}
	}

	empty := Request{Resource: ResourcePods, Verb: VerbGet}
	if !auth.Authorize(empty) {
		t.Fatal("empty dimensions should match exactly when both sides are empty")
	}
}

func TestCrossNamespaceAccessIsNotBorrowed(t *testing.T) {
	rule := Rule{
		Namespace:    "on-call",
		Resource:     ResourcePods,
		Verb:         VerbGet,
		ResourceName: "web-0",
	}
	auth := New([]Rule{rule})

	if !auth.Authorize(Request{Namespace: "on-call", Resource: ResourcePods, Verb: VerbGet, ResourceName: "web-0"}) {
		t.Fatal("the rule's own namespace should be allowed")
	}
	for _, ns := range []string{"prod", "", "staging"} {
		if auth.Authorize(Request{Namespace: ns, Resource: ResourcePods, Verb: VerbGet, ResourceName: "web-0"}) {
			t.Errorf("namespace %q borrowed authority from namespace on-call", ns)
		}
	}

	// An explicit namespace wildcard is the only way to cover all namespaces.
	allNamespaces := Rule{
		Namespace:    Wildcard,
		Resource:     ResourcePods,
		Verb:         VerbGet,
		ResourceName: "web-0",
	}
	if !New([]Rule{allNamespaces}).Authorize(Request{Namespace: "prod", Resource: ResourcePods, Verb: VerbGet, ResourceName: "web-0"}) {
		t.Fatal("an explicit namespace wildcard should cover another namespace")
	}
}

func TestCrossAPIGroupAccessIsNotBorrowed(t *testing.T) {
	rule := Rule{
		APIGroup:     "apps",
		Namespace:    "prod",
		Resource:     "deployments",
		Verb:         VerbGet,
		ResourceName: "web",
	}
	auth := New([]Rule{rule})

	if !auth.Authorize(Request{APIGroup: "apps", Namespace: "prod", Resource: "deployments", Verb: VerbGet, ResourceName: "web"}) {
		t.Fatal("the rule's own API group should be allowed")
	}
	for _, group := range []string{"", "batch", "example.org"} {
		if auth.Authorize(Request{APIGroup: group, Namespace: "prod", Resource: "deployments", Verb: VerbGet, ResourceName: "web"}) {
			t.Errorf("API group %q borrowed authority from API group apps", group)
		}
	}

	allGroups := Rule{
		APIGroup:     Wildcard,
		Namespace:    "prod",
		Resource:     "deployments",
		Verb:         VerbGet,
		ResourceName: "web",
	}
	if !New([]Rule{allGroups}).Authorize(Request{APIGroup: "batch", Namespace: "prod", Resource: "deployments", Verb: VerbGet, ResourceName: "web"}) {
		t.Fatal("an explicit API group wildcard should cover another API group")
	}
}

func TestExplicitExecAuthorization(t *testing.T) {
	rule := Rule{
		Namespace:    "prod",
		Resource:     ResourcePods,
		Subresource:  SubresourceExec,
		Verb:         VerbCreate,
		ResourceName: "web-0",
	}
	auth := New([]Rule{rule})

	allowed := Request{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceExec, Verb: VerbCreate, ResourceName: "web-0"}
	if !auth.Authorize(allowed) {
		t.Fatal("explicit pods/exec authorization should be honored")
	}

	denied := []struct {
		name string
		req  Request
	}{
		{"different verb", Request{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceExec, Verb: VerbGet, ResourceName: "web-0"}},
		{"different name", Request{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceExec, Verb: VerbCreate, ResourceName: "web-1"}},
		{"different namespace", Request{Namespace: "staging", Resource: ResourcePods, Subresource: SubresourceExec, Verb: VerbCreate, ResourceName: "web-0"}},
		{"base resource", Request{Namespace: "prod", Resource: ResourcePods, Verb: VerbCreate, ResourceName: "web-0"}},
		{"attach sibling", Request{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceAttach, Verb: VerbCreate, ResourceName: "web-0"}},
	}
	for _, tc := range denied {
		if auth.Authorize(tc.req) {
			t.Errorf("%s: exec authorization unexpectedly covered another capability", tc.name)
		}
	}
}

func TestResourceNamesAreExact(t *testing.T) {
	rule := Rule{
		Namespace:    "prod",
		Resource:     ResourcePods,
		Verb:         VerbGet,
		ResourceName: "web-0",
	}
	auth := New([]Rule{rule})

	if !auth.Authorize(Request{Namespace: "prod", Resource: ResourcePods, Verb: VerbGet, ResourceName: "web-0"}) {
		t.Fatal("the declared resource name should be allowed")
	}
	if auth.Authorize(Request{Namespace: "prod", Resource: ResourcePods, Verb: VerbGet, ResourceName: "web-1"}) {
		t.Fatal("a different resource name must be denied")
	}
	if auth.Authorize(Request{Namespace: "prod", Resource: ResourcePods, Verb: VerbGet}) {
		t.Fatal("an empty resource name must not match a named rule")
	}
}

func TestCombinedResourcePathIsAnExplicitSubresource(t *testing.T) {
	rule := Rule{
		Namespace:    "prod",
		Resource:     "pods/exec",
		Verb:         VerbCreate,
		ResourceName: "web-0",
	}
	auth := New([]Rule{rule})

	req := Request{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceExec, Verb: VerbCreate, ResourceName: "web-0"}
	if !auth.Authorize(req) {
		t.Fatal("pods/exec should match the equivalent split rule dimensions")
	}
	if auth.Authorize(Request{Namespace: "prod", Resource: ResourcePods, Verb: VerbCreate, ResourceName: "web-0"}) {
		t.Fatal("pods/exec must not match the base pods resource")
	}
}

func TestDefaultDenyAndMalformedRequests(t *testing.T) {
	if New(nil).Authorize(Request{Resource: ResourcePods, Verb: VerbGet}) {
		t.Fatal("an empty policy must deny")
	}
	var nilAuth *Authorizer
	if nilAuth.Authorize(Request{Resource: ResourcePods, Verb: VerbGet}) {
		t.Fatal("a nil authorizer must deny")
	}

	badRules := []Rule{
		{Resource: "", Verb: VerbGet},
		{Resource: ResourcePods, Verb: ""},
		{Resource: "pods/exec", Subresource: SubresourceExec, Verb: VerbCreate},
	}
	for i, rule := range badRules {
		if err := rule.Validate(); err == nil {
			t.Errorf("bad rule %d was accepted", i)
		}
		if rule.Matches(Request{Resource: ResourcePods, Subresource: SubresourceExec, Verb: VerbCreate}) {
			t.Errorf("bad rule %d unexpectedly matched", i)
		}
	}
	if _, err := Compile(badRules); err == nil {
		t.Fatal("Compile should report invalid rules")
	}
}

func TestCheckReportsMatchingRule(t *testing.T) {
	rules := []Rule{
		{Namespace: "prod", Resource: ResourcePods, Verb: VerbList},
		{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceExec, Verb: VerbCreate, ResourceName: "web-0"},
	}
	auth := New(rules)

	decision := auth.Check(Request{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceExec, Verb: VerbCreate, ResourceName: "web-0"})
	if !decision.Allowed || decision.MatchedRule != 1 {
		t.Fatalf("got decision %+v, want allowed by rule 1", decision)
	}

	decision = auth.Check(Request{Namespace: "staging", Resource: ResourcePods, Verb: VerbList})
	if decision.Allowed || decision.MatchedRule != -1 {
		t.Fatalf("got decision %+v, want default deny", decision)
	}
}

func ExampleAuthorizer() {
	policy := New([]Rule{
		{Namespace: "prod", Resource: ResourcePods, Verb: VerbGet, ResourceName: "web-0"},
		{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceExec, Verb: VerbCreate, ResourceName: "web-0"},
	})

	fmt.Println(policy.Authorize(Request{Namespace: "prod", Resource: ResourcePods, Verb: VerbGet, ResourceName: "web-0"}))
	fmt.Println(policy.Authorize(Request{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceExec, Verb: VerbCreate, ResourceName: "web-0"}))
	fmt.Println(policy.Authorize(Request{Namespace: "prod", Resource: ResourcePods, Subresource: SubresourceAttach, Verb: VerbCreate, ResourceName: "web-0"}))

	// Output:
	// true
	// true
	// false
}
