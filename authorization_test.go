package netsec

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func testIdentity() Identity {
	return Identity{TenantID: "tenant-a", SubjectID: "alice"}
}

func mustAuthorize(t *testing.T, query string, variables map[string]any, identity Identity, policy Policy) (*Plan, error) {
	t.Helper()
	return NewEngine(policy).Authorize(context.Background(), Request{
		Query:     query,
		Variables: variables,
		Identity:  identity,
	})
}

func TestDuplicateAliasDeniedOccurrenceRejectsWholePath(t *testing.T) {
	requests := 0
	policy := PolicyFunc(func(_ context.Context, request FieldRequest) (Decision, error) {
		requests++
		if request.Occurrence == 1 {
			return Deny("second alias denied"), nil
		}
		return Allow("secret:read"), nil
	})
	plan, err := mustAuthorize(t, `{ secret secret }`, nil, testIdentity(), policy)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if requests != 2 {
		t.Fatalf("expected each alias occurrence to be checked independently, got %d checks", requests)
	}
	if len(plan.DeniedPaths) != 1 || plan.DeniedPaths[0].String() != "$.secret" {
		t.Fatalf("expected whole $.secret path denied, got %#v", plan.DeniedPaths)
	}
	if len(plan.Fields) != 0 {
		t.Fatalf("denied path must not be returned as an authorized response field")
	}
}

func TestDuplicateAliasesMergeOnlyWhenIdentityAndDecisionMatch(t *testing.T) {
	plan, err := mustAuthorize(t, `{ secret secret }`, nil, testIdentity(), AllowAllPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Fields) != 1 {
		t.Fatalf("expected one merged response path, got %d", len(plan.Fields))
	}
	field := plan.Fields[0]
	if !field.Merged || field.Occurrences != 2 || len(field.Nodes) != 2 {
		t.Fatalf("expected both independently checked occurrences merged: %#v", field)
	}
}

func TestConflictingDecisionsAtSamePathAreRejected(t *testing.T) {
	policy := PolicyFunc(func(_ context.Context, request FieldRequest) (Decision, error) {
		if request.Occurrence == 0 {
			return Allow("scope-a"), nil
		}
		return Allow("scope-b"), nil
	})
	plan, err := mustAuthorize(t, `{ secret secret }`, nil, testIdentity(), policy)
	if !errors.Is(err, ErrResponseConflict) {
		t.Fatalf("expected ErrResponseConflict, got %v", err)
	}
	if len(plan.Fields) != 0 {
		t.Fatalf("conflicting grants must not be merged")
	}
}

func TestPrototypeStyleAliasesRemainIndependentPathSegments(t *testing.T) {
	policy := PolicyFunc(func(_ context.Context, request FieldRequest) (Decision, error) {
		if request.ResponseKey == "__proto__" {
			return Deny("prototype alias denied"), nil
		}
		return Allow(), nil
	})
	plan, err := mustAuthorize(t, `{ __proto__: secret constructor: secret }`, nil, testIdentity(), policy)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized prototype-style alias, got %v", err)
	}
	if len(plan.DeniedPaths) != 1 || plan.DeniedPaths[0].String() != `$["__proto__"]` {
		t.Fatalf("unexpected denied path: %#v", plan.DeniedPaths)
	}
	if _, ok := plan.FieldAt("__proto__"); ok {
		t.Fatalf("denied alias must not be returned")
	}
	if _, ok := plan.FieldAt("constructor"); !ok {
		t.Fatalf("independently authorized sibling path should remain available")
	}
}

func TestPrototypeStyleAliasesDoNotCollideWhenAuthorized(t *testing.T) {
	plan, err := mustAuthorize(t, `{ __proto__: secret constructor: secret prototype: secret }`, nil, testIdentity(), AllowAllPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Fields) != 3 {
		t.Fatalf("expected three literal response paths, got %d", len(plan.Fields))
	}
	for _, field := range plan.Fields {
		if field.Path.Key() == "" {
			t.Fatalf("empty path key")
		}
	}
}

func TestDuplicatePrototypeAliasStillRequiresBothAuthorizations(t *testing.T) {
	policy := PolicyFunc(func(_ context.Context, request FieldRequest) (Decision, error) {
		if request.Occurrence == 1 {
			return Deny("duplicate prototype alias denied"), nil
		}
		return Allow(), nil
	})
	plan, err := mustAuthorize(t, `{ __proto__: secret __proto__: secret }`, nil, testIdentity(), policy)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected duplicate prototype alias to reject the path, got %v", err)
	}
	if len(plan.DeniedPaths) != 1 || plan.DeniedPaths[0].String() != `$["__proto__"]` {
		t.Fatalf("unexpected denied path: %#v", plan.DeniedPaths)
	}
	if _, ok := plan.FieldAt("__proto__"); ok {
		t.Fatalf("denied prototype path must not survive")
	}
}

func TestFragmentReuseCreatesIndependentAuthorizationNodes(t *testing.T) {
	query := `
		fragment Shared on User { secret }
		query Q {
			first: user { ...Shared }
			second: user { ...Shared }
		}
	`
	policy := PolicyFunc(func(_ context.Context, request FieldRequest) (Decision, error) {
		if request.ResponsePath.String() == "$.second.secret" {
			return Deny("second fragment use denied"), nil
		}
		return Allow(), nil
	})
	plan, err := mustAuthorize(t, query, nil, testIdentity(), policy)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized fragment reuse, got %v", err)
	}
	var secretNodes []*AuthNode
	for _, node := range plan.Nodes {
		if node.FieldName == "secret" {
			secretNodes = append(secretNodes, node)
		}
	}
	if len(secretNodes) != 2 {
		t.Fatalf("expected two independent secret nodes, got %d", len(secretNodes))
	}
	if secretNodes[0].ID == secretNodes[1].ID {
		t.Fatalf("fragment reuse must not reuse authorization node identity")
	}
	if _, ok := plan.FieldAt("first", "secret"); !ok {
		t.Fatalf("authorized fragment use should remain in the safe plan")
	}
	if _, ok := plan.FieldAt("second", "secret"); ok {
		t.Fatalf("unauthorized fragment use must not be returned")
	}
}

func TestSkipAndIncludeAreEvaluatedPerOccurrenceThenReauthorized(t *testing.T) {
	query := `
		query Q($hide: Boolean!, $show: Boolean!) {
			secret @skip(if: $hide)
			secret @include(if: $show)
		}
	`
	var seen []FieldRequest
	policy := PolicyFunc(func(_ context.Context, request FieldRequest) (Decision, error) {
		seen = append(seen, request)
		return Allow(), nil
	})
	vars := map[string]any{"hide": false, "show": true}
	plan, err := mustAuthorize(t, query, vars, testIdentity(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("both active conditional occurrences must be re-authorized, got %d", len(seen))
	}
	if len(plan.Fields) != 1 || !plan.Fields[0].Merged {
		t.Fatalf("two identically authorized occurrences should merge safely: %#v", plan.Fields)
	}

	seen = nil
	vars = map[string]any{"hide": true, "show": false}
	plan, err = mustAuthorize(t, query, vars, testIdentity(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Fatalf("inactive skipped fields must not be authorized as reachable fields, got %d", len(seen))
	}
	if len(plan.Fields) != 0 {
		t.Fatalf("all fields were skipped, expected no active response fields")
	}
}

func TestSkipCannotHideAnUnauthorizedDuplicateWhenItRuns(t *testing.T) {
	query := `
		query Q($skipFirst: Boolean!) {
			secret @skip(if: $skipFirst)
			secret
		}
	`
	policy := PolicyFunc(func(_ context.Context, request FieldRequest) (Decision, error) {
		if request.Occurrence == 0 {
			return Deny("skippable duplicate denied"), nil
		}
		return Allow(), nil
	})
	plan, err := mustAuthorize(t, query, map[string]any{"skipFirst": false}, testIdentity(), policy)
	if !errors.Is(err, ErrUnauthorized) || len(plan.DeniedPaths) != 1 {
		t.Fatalf("expected both active aliases to reject the path, got %v", err)
	}

	plan, err = mustAuthorize(t, query, map[string]any{"skipFirst": true}, testIdentity(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Fields) != 1 || plan.Fields[0].Occurrences != 1 {
		t.Fatalf("with the denied occurrence skipped only one authorized occurrence remains")
	}
}

func TestSkipOnFragmentSpreadAndInlineFragmentGatesDescendants(t *testing.T) {
	query := `
		fragment Shared on Query { a: secret b: secret }
		query Q($skipShared: Boolean!, $showInline: Boolean!) {
			...Shared @skip(if: $skipShared)
			... on Query @include(if: $showInline) { c: secret }
		}
	`
	policy := PolicyFunc(func(_ context.Context, request FieldRequest) (Decision, error) {
		if request.ResponseKey == "c" {
			return Deny("inline branch denied"), nil
		}
		return Allow(), nil
	})
	plan, err := mustAuthorize(t, query, map[string]any{"skipShared": false, "showInline": true}, testIdentity(), policy)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("active inline branch should be denied, got %v", err)
	}
	if _, ok := plan.FieldAt("c"); ok {
		t.Fatalf("denied inline branch must not be returned")
	}
	if _, ok := plan.FieldAt("a"); !ok {
		t.Fatalf("authorized fragment branch should remain")
	}

	plan, err = mustAuthorize(t, query, map[string]any{"skipShared": true, "showInline": false}, testIdentity(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Fields) != 0 {
		t.Fatalf("both conditional branches were skipped: %#v", plan.Fields)
	}
}

func TestPaginationCapIsEvaluatedPerAlias(t *testing.T) {
	policy := PolicyFunc(func(_ context.Context, request FieldRequest) (Decision, error) {
		decision := Allow("items:read")
		decision.MaxPageSize = 5
		return decision, nil
	})
	plan, err := mustAuthorize(t, `{ small: items(first: 1) large: items(first: 10) }`, nil, testIdentity(), policy)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("large alias must be denied even when small alias is allowed, got %v", err)
	}
	if _, ok := plan.FieldAt("small"); !ok {
		t.Fatalf("small alias should remain authorized")
	}
	if _, ok := plan.FieldAt("large"); ok {
		t.Fatalf("large alias must not survive")
	}
}

func TestVariableDefaultCannotWidenPagination(t *testing.T) {
	query := `query Q($size: Int = 100) { items(first: $size) }`
	policy := PolicyFunc(func(_ context.Context, request FieldRequest) (Decision, error) {
		decision := Allow()
		decision.MaxPageSize = 5
		return decision, nil
	})
	_, err := mustAuthorize(t, query, nil, testIdentity(), policy)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("default page size must be constrained by the grant, got %v", err)
	}
	plan, err := mustAuthorize(t, query, map[string]any{"size": 3}, testIdentity(), policy)
	if err != nil {
		t.Fatalf("non-default in-scope page size should pass: %v", err)
	}
	if field, ok := plan.FieldAt("items"); !ok || !field.Nodes[0].Pagination.HasFirst {
		t.Fatalf("resolved pagination must be available to authorization")
	}
}

func TestDifferentAliasesWithDifferentPaginationAreNotMerged(t *testing.T) {
	plan, err := mustAuthorize(t, `{ small: items(first: 1) large: items(first: 2) }`, nil, testIdentity(), AllowAllPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Fields) != 2 {
		t.Fatalf("different aliases bind different response paths, got %#v", plan.Fields)
	}
	for _, field := range plan.Fields {
		if field.Merged {
			t.Fatalf("separate aliases must not be merged")
		}
	}
}

func TestCrossTenantNodeIDIsDeniedByDefaultGuard(t *testing.T) {
	query := `query Q($id: ID!) { node(id: $id) { secret } }`
	plan, err := mustAuthorize(t, query, map[string]any{"id": "tenant-b:User:1"}, testIdentity(), AllowAllPolicy{})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected cross-tenant denial, got %v", err)
	}
	if len(plan.DeniedPaths) < 1 || plan.DeniedPaths[0].String() != "$.node" {
		t.Fatalf("expected $.node path denied first, got %#v", plan.DeniedPaths)
	}
	if len(plan.Fields) != 0 {
		t.Fatalf("descendants of a denied node must not be returned: %#v", plan.Fields)
	}

	_, err = mustAuthorize(t, query, map[string]any{"id": "tenant-a:User:1"}, testIdentity(), AllowAllPolicy{})
	if err != nil {
		t.Fatalf("same-tenant node ID should pass: %v", err)
	}
}

func TestDeniedParentPropagatesToDescendantPaths(t *testing.T) {
	policy := PolicyFunc(func(_ context.Context, request FieldRequest) (Decision, error) {
		if request.ResponsePath.String() == "$.parent" {
			return Deny("parent denied"), nil
		}
		return Allow(), nil
	})
	plan, err := mustAuthorize(t, `{ parent { child { leaf } } }`, nil, testIdentity(), policy)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected parent denial, got %v", err)
	}
	if len(plan.Fields) != 0 {
		t.Fatalf("no descendant of a denied parent may remain: %#v", plan.Fields)
	}
	rejected := make(map[string]bool)
	for _, path := range plan.RejectedPaths() {
		rejected[path.String()] = true
	}
	for _, path := range []string{"$.parent", "$.parent.child", "$.parent.child.leaf"} {
		if !rejected[path] {
			t.Fatalf("expected %s rejected, got %#v", path, plan.RejectedPaths())
		}
	}
}

func TestCrossTenantTenantArgumentAndOpaqueID(t *testing.T) {
	query := `query Q { node(id: "dGVuYW50LWI6VXNlcjox") { secret } }`
	_, err := mustAuthorize(t, query, nil, testIdentity(), AllowAllPolicy{})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("base64-encoded foreign tenant ID must be denied, got %v", err)
	}
	sameTenant := `query Q { node(id: "dGVuYW50LWE6VXNlcjox") { secret } }`
	if _, err := mustAuthorize(t, sameTenant, nil, testIdentity(), AllowAllPolicy{}); err != nil {
		t.Fatalf("base64-encoded same-tenant ID should pass: %v", err)
	}

	query = `query Q { node(tenantId: "tenant-b", id: "1") { secret } }`
	_, err = mustAuthorize(t, query, nil, testIdentity(), AllowAllPolicy{})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("foreign tenantId argument must be denied, got %v", err)
	}
}

func TestTenantNodePolicyComposesWithFieldPolicy(t *testing.T) {
	inner := PolicyFunc(func(_ context.Context, request FieldRequest) (Decision, error) {
		if request.FieldName == "admin" {
			return Deny("admin denied"), nil
		}
		return Allow(), nil
	})
	_, err := mustAuthorize(t, `{ node(id: "tenant-b:User:1") { secret } }`, nil, testIdentity(), NewTenantNodePolicy(inner))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("tenant node policy should deny foreign ID, got %v", err)
	}
}

func TestBatchAndPersistedQueriesSharePlanDigest(t *testing.T) {
	engine := NewEngine(AllowAllPolicy{})
	query := `query Q($size: Int = 2) { a: items(first: $size) }`
	if err := engine.RegisterPersistedQuery("pq-1", query); err != nil {
		t.Fatal(err)
	}
	direct, err := engine.Authorize(context.Background(), Request{
		Query:     query,
		Variables: map[string]any{"size": 1},
		Identity:  testIdentity(),
	})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := engine.AuthorizeBatch(context.Background(), []Request{{
		Query:            query,
		Variables:        map[string]any{"size": 7},
		Identity:         testIdentity(),
		BatchID:          "batch-item",
		PersistedQueryID: "pq-1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := engine.AuthorizePersisted(context.Background(), "pq-1", map[string]any{"size": 12}, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	static, err := engine.Compile(query)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := engine.AuthorizeStatic(context.Background(), static, map[string]any{"size": 2}, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if direct.Digest != batch.Plans[0].Digest || direct.Digest != persisted.Digest || direct.Digest != reused.Digest {
		t.Fatalf("batch/persisted/static/direct digests differ: %s %s %s %s", direct.Digest, batch.Plans[0].Digest, persisted.Digest, reused.Digest)
	}
	if direct.Digest == "" {
		t.Fatalf("empty plan digest")
	}
	if direct.AuthorizationDigest == batch.Plans[0].AuthorizationDigest {
		t.Fatalf("different runtime variables should be visible in the authorization digest")
	}
}

func TestPlanDigestIgnoresFormattingAndBatchMetadata(t *testing.T) {
	first, err := PlanDigest(`query Q { a: item b: item }`)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanDigest(`
		# comment
		query Q {
			a: item,
			b: item
		}
	`)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("formatting changed plan digest: %s != %s", first, second)
	}
	third, err := PlanDigest(`query Q { a: item a: item }`)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatalf("duplicate alias occurrences must change the plan digest")
	}
}

func TestMultipleOperationsAreSelectedByPlanDigest(t *testing.T) {
	query := `query A { a: secret } query B { b: secret }`
	digestA, err := PlanDigest(query, "A")
	if err != nil {
		t.Fatal(err)
	}
	digestB, err := PlanDigest(query, "B")
	if err != nil {
		t.Fatal(err)
	}
	if digestA == digestB {
		t.Fatalf("different selected operations must have different digests")
	}
	plan, err := AuthorizeOperation(context.Background(), query, "B", nil, testIdentity(), AllowAllPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plan.FieldAt("b"); !ok {
		t.Fatalf("expected operation B path")
	}
	if _, ok := plan.FieldAt("a"); ok {
		t.Fatalf("operation A must not be planned")
	}
	if _, err := Compile(query); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("multi-operation compile without name should fail, got %v", err)
	}
}

func TestDuplicatePathWithDifferentArgumentsRejectsPaginationWidening(t *testing.T) {
	plan, err := mustAuthorize(t, `{ items(first: 1) items(first: 10) }`, nil, testIdentity(), AllowAllPolicy{})
	if !errors.Is(err, ErrResponseConflict) {
		t.Fatalf("conflicting pagination on the same response key must fail closed, got %v", err)
	}
	if len(plan.Fields) != 0 {
		t.Fatalf("conflicting response key must not be returned")
	}
}

func TestInvalidPaginationIsPathLevelFailure(t *testing.T) {
	plan, err := mustAuthorize(t, `{ items(first: -1) }`, nil, testIdentity(), AllowAllPolicy{})
	if !errors.Is(err, ErrInvalidVariables) || !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected invalid-variable authorization failure, got %v", err)
	}
	if len(plan.DeniedPaths) != 1 || plan.DeniedPaths[0].String() != "$.items" {
		t.Fatalf("invalid pagination should deny the bound path: %#v", plan.DeniedPaths)
	}
}

func TestPolicyFailureFailsClosed(t *testing.T) {
	policy := PolicyFunc(func(_ context.Context, _ FieldRequest) (Decision, error) {
		return Decision{}, errors.New("policy backend unavailable")
	})
	plan, err := mustAuthorize(t, `{ secret }`, nil, testIdentity(), policy)
	if !errors.Is(err, ErrPolicy) || !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("policy errors must fail closed, got %v", err)
	}
	if plan.Allowed() {
		t.Fatalf("rejected plan must not report allowed")
	}
	if len(plan.Failures) != 1 {
		t.Fatalf("expected one failure, got %#v", plan.Failures)
	}
}

func TestInactiveNodesRetainBindingsButDoNotAuthorize(t *testing.T) {
	query := `query Q($skip: Boolean!) { visible: secret @skip(if: $skip) }`
	plan, err := mustAuthorize(t, query, map[string]any{"skip": true}, testIdentity(), AllowAllPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 1 || plan.Nodes[0].Active {
		t.Fatalf("expected one inactive structurally bound node: %#v", plan.Nodes)
	}
	if len(plan.Nodes[0].Directives) == 0 || !plan.Nodes[0].Directives[0].Evaluated {
		t.Fatalf("runtime directive state should be retained")
	}
}

func TestFragmentCycleAndUndefinedVariableAreRejected(t *testing.T) {
	_, err := Compile(`fragment A on Query { ...B } fragment B on Query { ...A } query Q { ...A }`)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected fragment cycle error, got %v", err)
	}
	_, err = Compile(`query Q { item(id: $missing) }`)
	if err == nil || !errors.Is(err, ErrInvalidVariables) {
		t.Fatalf("expected undefined variable error, got %v", err)
	}
}
