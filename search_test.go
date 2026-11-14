package netsec

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
)

const (
	testTenant  = "tenant-a"
	otherTenant = "tenant-b"
)

type fixture struct {
	store   *MemoryStore
	engine  *Engine
	p1Scope Scope
	p2Scope Scope
	all     Scope
}

func newFixture() fixture {
	org1 := NewResource(testTenant, "Organization", "org1", "")
	org1.Shared = true
	org1.AddAttribute("name", "Org One")
	org1.AddReference("partOf", "Organization/org2")

	org2 := NewResource(testTenant, "Organization", "org2", "")
	org2.Shared = true
	org2.AddAttribute("name", "Parent Org")

	// Same logical ID in another tenant. It must never be reachable from a
	// tenant-a search.
	orgOtherTenant := NewResource(otherTenant, "Organization", "org2", "")
	orgOtherTenant.Shared = true
	orgOtherTenant.AddAttribute("name", "Foreign Parent")

	p1 := NewResource(testTenant, "Patient", "p1", "")
	p1.AddAttribute("name", "Alice")
	p1.AddReference("managingOrganization", "Organization/org1")

	p2 := NewResource(testTenant, "Patient", "p2", "")
	p2.AddAttribute("name", "Bob")

	p3 := NewResource(testTenant, "Patient", "p3", "")
	p3.AddAttribute("name", "Carol")

	// Patient links are relationships, not ownership. pl1 remains readable,
	// but following its link must not expose pl2.
	pl1 := NewResource(testTenant, "Patient", "pl1", "")
	pl1.AddAttribute("name", "Linked One")
	pl1.AddReference("link", "Patient/pl2")

	pl2 := NewResource(testTenant, "Patient", "pl2", "")
	pl2.AddAttribute("name", "Linked Two")
	pl2.AddReference("link", "Patient/pl1")

	// A same-ID patient in another tenant.
	p1OtherTenant := NewResource(otherTenant, "Patient", "p1", "")
	p1OtherTenant.AddAttribute("name", "Alice Foreign")

	o1 := NewResource(testTenant, "Observation", "o1", "p1")
	o1.AddAttribute("code", "A")
	o1.AddReference("subject", "Patient/p1")
	o1.AddReference("has-member", "Observation/o10")

	o2 := NewResource(testTenant, "Observation", "o2", "p2")
	o2.AddAttribute("code", "A")
	o2.AddReference("subject", "Patient/p2")

	// The owning field says p1 but the subject points at p2. Fail closed.
	o3 := NewResource(testTenant, "Observation", "o3", "p1")
	o3.AddAttribute("code", "A")
	o3.AddReference("subject", "Patient/p2")

	// No owner. It must not become visible merely because it is in the tenant.
	o4 := NewResource(testTenant, "Observation", "o4", "")
	o4.AddAttribute("code", "A")

	observations := []*Resource{o1, o2, o3, o4}
	for _, id := range []string{"o5", "o6", "o7", "o8", "o9"} {
		observation := NewResource(testTenant, "Observation", id, "p1")
		observation.AddAttribute("code", "A")
		observation.AddReference("subject", "Patient/p1")
		observations = append(observations, observation)
	}

	o10 := NewResource(testTenant, "Observation", "o10", "p1")
	o10.AddAttribute("code", "A")
	o10.AddReference("subject", "Patient/p1")
	observations = append(observations, o10)

	o12 := NewResource(testTenant, "Observation", "o12", "p1")
	o12.AddAttribute("code", "C")
	o12.AddReference("subject", "Patient/p1")
	o12.AddReference("derived-from", "Observation/o1")
	observations = append(observations, o12)

	// A victim-owned observation that references an authorized patient. A
	// reverse include must still reject it.
	o20 := NewResource(testTenant, "Observation", "o20", "p2")
	o20.AddAttribute("code", "A")
	o20.AddReference("subject", "Patient/p1")
	observations = append(observations, o20)

	t2o := NewResource(otherTenant, "Observation", "t2o", "p1")
	t2o.AddAttribute("code", "A")
	t2o.AddReference("subject", "Patient/p1")
	observations = append(observations, t2o)

	e1 := NewResource(testTenant, "Encounter", "e1", "p1")
	e1.AddAttribute("status", "finished")
	e1.AddReference("subject", "Patient/p1")
	e1.AddReference("service-provider", "Organization/org1")
	// e1 points at an observation owned by p2. Include and chain traversal
	// must stop at the unauthorized hop.
	e1.AddReference("reason-reference", "Observation/o2")

	e2 := NewResource(testTenant, "Encounter", "e2", "p2")
	e2.AddAttribute("status", "finished")
	e2.AddReference("subject", "Patient/p2")

	e3 := NewResource(testTenant, "Encounter", "e3", "p1")
	e3.AddAttribute("status", "finished")
	e3.AddReference("subject", "Patient/p1")

	e4 := NewResource(testTenant, "Encounter", "e4", "p2")
	e4.AddAttribute("status", "finished")
	e4.AddReference("subject", "Patient/p2")

	e5 := NewResource(testTenant, "Encounter", "e5", "p1")
	e5.AddAttribute("status", "finished")
	e5.AddReference("subject", "Patient/p1")

	// Legitimate chain target owned by p1.
	e6 := NewResource(testTenant, "Encounter", "e6", "p1")
	e6.AddAttribute("status", "finished")
	e6.AddReference("subject", "Patient/p1")
	e6.AddReference("reason-reference", "Observation/o1")

	// A p2-owned encounter that references p1. A reverse include must reject
	// it even though the target resource is authorized.
	e20 := NewResource(testTenant, "Encounter", "e20", "p2")
	e20.AddAttribute("status", "finished")
	e20.AddReference("subject", "Patient/p1")

	resources := append([]*Resource{
		org1, org2, orgOtherTenant,
		p1, p2, p3, pl1, pl2, p1OtherTenant,
		e1, e2, e3, e4, e5, e6, e20,
	}, observations...)

	store := NewMemoryStore(resources...)
	return fixture{
		store:   store,
		engine:  NewEngine(store),
		p1Scope: NewScope(testTenant, "p1"),
		p2Scope: NewScope(testTenant, "p2"),
		all:     NewScope(testTenant, "p1", "p2"),
	}
}

func search(t *testing.T, engine *Engine, scope Scope, resourceType, rawQuery string) *SearchResult {
	t.Helper()
	result, err := engine.SearchQuery(context.Background(), scope, resourceType, rawQuery)
	if err != nil {
		t.Fatalf("search %s?%s: %v", resourceType, rawQuery, err)
	}
	return result
}

func resourceKeys(resources []*Resource) []string {
	keys := make([]string, 0, len(resources))
	for _, resource := range resources {
		keys = append(keys, resource.Key())
	}
	sort.Strings(keys)
	return keys
}

func assertKeys(t *testing.T, got []*Resource, want ...string) {
	t.Helper()
	sort.Strings(want)
	gotKeys := resourceKeys(got)
	if strings.Join(gotKeys, ",") != strings.Join(want, ",") {
		t.Fatalf("resource keys = %v, want %v", gotKeys, want)
	}
}

func assertOnlyAuthorized(t *testing.T, resources []*Resource, allowed ...string) {
	t.Helper()
	allowedSet := make(map[string]bool, len(allowed))
	for _, patient := range allowed {
		allowedSet[patient] = true
	}
	for _, resource := range resources {
		for _, patient := range resource.PatientIDs() {
			if !allowedSet[patient] {
				t.Fatalf("%s exposes patient %s; allowed %v", resource.Key(), patient, allowed)
			}
		}
	}
}

func TestSearchReturnsOnlyAuthorizedPatients(t *testing.T) {
	f := newFixture()

	result := search(t, f.engine, f.p1Scope, "Observation", "code=A")
	if result.Total != 7 {
		t.Fatalf("total = %d, want 7", result.Total)
	}
	assertKeys(t, result.Resources,
		"Observation/o1", "Observation/o5", "Observation/o6", "Observation/o7",
		"Observation/o8", "Observation/o9", "Observation/o10")
	assertOnlyAuthorized(t, result.Resources, "p1")

	// Explicitly asking for another patient's data must not bypass ownership.
	result = search(t, f.engine, f.p1Scope, "Observation", "patient=Patient/p2")
	if result.Total != 0 || len(result.Resources) != 0 {
		t.Fatalf("cross-patient filter leaked: total=%d resources=%v", result.Total, resourceKeys(result.Resources))
	}

	// The same logical ID exists in tenant-b and must not be returned.
	result = search(t, f.engine, f.p1Scope, "Observation", "code=A")
	for _, resource := range result.Resources {
		if resource.TenantID != testTenant {
			t.Fatalf("cross-tenant resource %s returned", resource.Key())
		}
	}
}

func TestCrossPatientChainIsRejectedAtIntermediateHop(t *testing.T) {
	f := newFixture()

	// Without per-hop authorization this chain reaches Bob: e1 ->
	// Observation/o2 -> Patient/p2.
	result := search(t, f.engine, f.p1Scope, "Encounter",
		"reason-reference:Observation.subject:Patient.name=Bob")
	if result.Total != 0 {
		t.Fatalf("cross-patient chain returned %v", resourceKeys(result.Resources))
	}

	// The same chain with an enforceable terminal value still stops at the
	// unauthorized observation.
	result = search(t, f.engine, f.p1Scope, "Encounter",
		"_id=e1&reason-reference:Observation.code=A")
	if result.Total != 0 {
		t.Fatalf("chain traversed unauthorized observation: %v", resourceKeys(result.Resources))
	}

	// A legitimate chain to an authorized observation still works.
	result = search(t, f.engine, f.p1Scope, "Encounter",
		"reason-reference:Observation.code=A")
	assertKeys(t, result.Resources, "Encounter/e6")

	// A multi-hop chain through Patient and Organization stays authorized.
	result = search(t, f.engine, f.p1Scope, "Encounter",
		"subject:Patient.managingOrganization:Organization.partOf:Organization.name=Parent Org")
	assertKeys(t, result.Resources, "Encounter/e1", "Encounter/e3", "Encounter/e5", "Encounter/e6")
}

func TestIncludeDoesNotExpandScope(t *testing.T) {
	f := newFixture()

	result := search(t, f.engine, f.p1Scope, "Encounter",
		"_id=e1,e6&_include=Encounter:reason-reference")
	assertKeys(t, result.Resources, "Encounter/e1", "Encounter/e6")
	// e6's target o1 is authorized; e1's target o2 belongs to p2 and must be
	// dropped rather than failing or widening the response.
	assertKeys(t, result.Included, "Observation/o1")
	assertOnlyAuthorized(t, result.Included, "p1")

	result = search(t, f.engine, f.p1Scope, "Encounter",
		"_id=e1&_include=Encounter:subject&_include=Encounter:service-provider")
	assertKeys(t, result.Resources, "Encounter/e1")
	assertKeys(t, result.Included, "Organization/org1", "Patient/p1")
	assertOnlyAuthorized(t, result.Included, "p1")

	// Target-type qualifiers are enforced. The legitimate target here is an
	// Observation, so requesting Patient must not include it.
	result = search(t, f.engine, f.p1Scope, "Encounter",
		"_id=e6&_include=Encounter:reason-reference:Observation")
	assertKeys(t, result.Included, "Observation/o1")
	result = search(t, f.engine, f.p1Scope, "Encounter",
		"_id=e6&_include=Encounter:reason-reference:Patient")
	assertKeys(t, result.Included)

	// A wildcard include must apply the same filter to every reference.
	result = search(t, f.engine, f.p1Scope, "Encounter", "_id=e1&_include=*:*")
	assertKeys(t, result.Included, "Organization/org1", "Patient/p1")
	assertOnlyAuthorized(t, result.Included, "p1")
}

func TestRevincludeDoesNotExpandScope(t *testing.T) {
	f := newFixture()

	result := search(t, f.engine, f.p1Scope, "Patient",
		"_id=p1&_revinclude=Observation:subject")
	assertKeys(t, result.Resources, "Patient/p1")
	assertKeys(t, result.Included,
		"Observation/o1", "Observation/o5", "Observation/o6", "Observation/o7",
		"Observation/o8", "Observation/o9", "Observation/o10", "Observation/o12")
	assertOnlyAuthorized(t, result.Included, "p1")

	// e20 references p1 but belongs to p2 and must not be reverse-included.
	result = search(t, f.engine, f.p1Scope, "Patient",
		"_id=p1&_revinclude=Encounter:subject")
	assertKeys(t, result.Included, "Encounter/e1", "Encounter/e3", "Encounter/e5", "Encounter/e6")
	assertOnlyAuthorized(t, result.Included, "p1")

	result = search(t, f.engine, f.p1Scope, "Patient",
		"_id=p1&_revinclude=Observation:subject:Patient")
	assertKeys(t, result.Included,
		"Observation/o1", "Observation/o5", "Observation/o6", "Observation/o7",
		"Observation/o8", "Observation/o9", "Observation/o10", "Observation/o12")
	// The current target is Patient/p1, not an Encounter, so this directive
	// must not return anything.
	result = search(t, f.engine, f.p1Scope, "Patient",
		"_id=p1&_revinclude=Observation:subject:Encounter")
	assertKeys(t, result.Included)
}

func TestPaginationAndCountUseTheSameAuthorizedFilter(t *testing.T) {
	f := newFixture()
	params := mustValues(t, "code=A")

	count, err := f.engine.Count(context.Background(), f.p1Scope, "Observation", params)
	if err != nil {
		t.Fatal(err)
	}
	if count != 7 {
		t.Fatalf("count = %d, want 7", count)
	}

	var collected []string
	for offset := 0; offset <= 8; offset += 2 {
		query := fmt.Sprintf("code=A&_count=2&_offset=%d", offset)
		result := search(t, f.engine, f.p1Scope, "Observation", query)
		if result.Total != 7 {
			t.Fatalf("offset %d total = %d, want 7", offset, result.Total)
		}
		if result.Offset != offset {
			t.Fatalf("offset = %d, want %d", result.Offset, offset)
		}
		if offset == 0 && len(result.Resources) != 2 {
			t.Fatalf("first page size = %d, want 2", len(result.Resources))
		}
		if offset == 8 && len(result.Resources) != 0 {
			t.Fatalf("past-end page returned %v", resourceKeys(result.Resources))
		}
		for _, resource := range result.Resources {
			collected = append(collected, resource.Key())
		}
		assertOnlyAuthorized(t, result.Resources, "p1")
	}
	sort.Strings(collected)
	want := []string{
		"Observation/o1", "Observation/o10", "Observation/o5", "Observation/o6",
		"Observation/o7", "Observation/o8", "Observation/o9",
	}
	if strings.Join(collected, ",") != strings.Join(want, ",") {
		t.Fatalf("paged union = %v, want %v", collected, want)
	}

	countResult := search(t, f.engine, f.p1Scope, "Observation", "code=A&_summary=count")
	if countResult.Total != 7 || len(countResult.Resources) != 0 {
		t.Fatalf("summary count = total %d, resources %v", countResult.Total, resourceKeys(countResult.Resources))
	}
	zeroCount := search(t, f.engine, f.p1Scope, "Observation", "code=A&_count=0")
	if zeroCount.Total != 7 || len(zeroCount.Resources) != 0 {
		t.Fatalf("_count=0 = total %d, resources %v", zeroCount.Total, resourceKeys(zeroCount.Resources))
	}
}

func TestScopeChangesAreAppliedWithoutCaching(t *testing.T) {
	f := newFixture()

	p1 := search(t, f.engine, f.p1Scope, "Observation", "code=A")
	p2 := search(t, f.engine, f.p2Scope, "Observation", "code=A")
	all := search(t, f.engine, f.all, "Observation", "code=A")

	assertKeys(t, p1.Resources,
		"Observation/o1", "Observation/o5", "Observation/o6", "Observation/o7",
		"Observation/o8", "Observation/o9", "Observation/o10")
	// o20 is owned by p2 but also references p1. Because p1 is outside the
	// p2-only scope, the resource fails closed.
	assertKeys(t, p2.Resources, "Observation/o2")
	if all.Total != 10 {
		t.Fatalf("combined scope total = %d, want 10", all.Total)
	}
	assertOnlyAuthorized(t, all.Resources, "p1", "p2")

	// Switching back must not retain p2 results from a previous call.
	p1Again := search(t, f.engine, f.p1Scope, "Observation", "code=A")
	assertKeys(t, p1Again.Resources,
		"Observation/o1", "Observation/o5", "Observation/o6", "Observation/o7",
		"Observation/o8", "Observation/o9", "Observation/o10")

	empty := search(t, f.engine, NewScope(testTenant), "Observation", "code=A")
	if empty.Total != 0 {
		t.Fatalf("empty patient scope returned %v", resourceKeys(empty.Resources))
	}
}

func TestIterativeIncludesRespectDepthAndScope(t *testing.T) {
	f := newFixture()

	result := search(t, f.engine, f.p1Scope, "Encounter",
		"_id=e1&_include:iterate=Encounter:subject&_include:iterate=Patient:managingOrganization&_include:iterate=Organization:partOf")
	assertKeys(t, result.Included, "Organization/org1", "Organization/org2", "Patient/p1")
	assertOnlyAuthorized(t, result.Included, "p1")

	// The unauthorized o2 cannot seed the next iteration, so neither o2 nor
	// p2 can appear.
	result = search(t, f.engine, f.p1Scope, "Encounter",
		"_id=e1&_include:iterate=Encounter:reason-reference&_include:iterate=Observation:subject")
	assertKeys(t, result.Included)

	// A low expansion depth still keeps the authorized first hop but stops
	// before deeper shared resources.
	shallow := NewEngine(f.store, WithMaxIncludeDepth(1))
	result = search(t, shallow, f.p1Scope, "Encounter",
		"_id=e1&_include:iterate=Encounter:subject&_include:iterate=Patient:managingOrganization")
	assertKeys(t, result.Included, "Patient/p1")

	// Iterative reverse include: p1 -> observations referencing p1 -> o12,
	// which references o1.
	result = search(t, f.engine, f.p1Scope, "Patient",
		"_id=p1&_revinclude:iterate=Observation:subject&_revinclude:iterate=Observation:derived-from")
	assertKeys(t, result.Included,
		"Observation/o1", "Observation/o5", "Observation/o6", "Observation/o7",
		"Observation/o8", "Observation/o9", "Observation/o10", "Observation/o12")
	assertOnlyAuthorized(t, result.Included, "p1")
}

func TestResolutionBudgetFailsClosed(t *testing.T) {
	f := newFixture()
	engine := NewEngine(f.store, WithMaxResolutions(2))
	_, err := engine.SearchQuery(context.Background(), f.p1Scope, "Encounter",
		"_id=e1&_include=Encounter:*")
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("error = %v, want ErrBudgetExceeded", err)
	}
}

func TestTenantAndResourceTypeScope(t *testing.T) {
	f := newFixture()

	tenantB := NewScope(otherTenant, "p1")
	result := search(t, f.engine, tenantB, "Observation", "code=A")
	assertKeys(t, result.Resources, "Observation/t2o")

	// Organization/org2 exists in both tenants; a tenant-a include must use
	// the tenant-a instance.
	result = search(t, f.engine, f.p1Scope, "Encounter",
		"_id=e1&_include:iterate=Encounter:subject&_include:iterate=Patient:managingOrganization&_include:iterate=Organization:partOf")
	for _, resource := range result.Included {
		if resource.Key() == "Organization/org2" && resource.TenantID != testTenant {
			t.Fatalf("cross-tenant include returned tenant %s", resource.TenantID)
		}
	}

	observationsOnly := f.p1Scope.WithResourceTypes("Observation")
	result = search(t, f.engine, observationsOnly, "Encounter", "status=finished")
	if result.Total != 0 {
		t.Fatalf("resource-type allowlist returned %v", resourceKeys(result.Resources))
	}
	result = search(t, f.engine, observationsOnly, "Observation", "_id=o1&_include=Observation:subject")
	assertKeys(t, result.Resources, "Observation/o1")
	assertKeys(t, result.Included)
}

func TestPatientLinksDoNotAuthorizeTheLinkedPatient(t *testing.T) {
	f := newFixture()
	scope := NewScope(testTenant, "pl1")

	result := search(t, f.engine, scope, "Patient", "_id=pl1")
	assertKeys(t, result.Resources, "Patient/pl1")

	result = search(t, f.engine, scope, "Patient", "_id=pl1&_include=Patient:link")
	assertKeys(t, result.Included)

	result = search(t, f.engine, scope, "Patient", "_id=pl1&link:Patient.name=Linked Two")
	if result.Total != 0 {
		t.Fatalf("link chain crossed patient scope: %v", resourceKeys(result.Resources))
	}

	result = search(t, f.engine, scope, "Patient", "_id=pl1&_revinclude=Patient:link")
	assertKeys(t, result.Included)
}

func TestMalformedAndUnsupportedQueriesFailClosed(t *testing.T) {
	f := newFixture()
	tests := []struct {
		name string
		typ  string
		raw  string
		err  error
	}{
		{name: "has", typ: "Observation", raw: "_has:Encounter:subject:status=finished", err: ErrUnsupported},
		{name: "filter", typ: "Observation", raw: "_filter=code eq A", err: ErrUnsupported},
		{name: "negative count", typ: "Observation", raw: "_count=-1", err: ErrInvalidQuery},
		{name: "bad chain type", typ: "Encounter", raw: "subject:patient.name=Alice", err: ErrInvalidQuery},
		{name: "bad include modifier", typ: "Observation", raw: "_include:every=Observation:subject", err: ErrUnsupported},
		{name: "wildcard revinclude", typ: "Patient", raw: "_revinclude=*:*", err: ErrUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := f.engine.SearchQuery(context.Background(), f.p1Scope, test.typ, test.raw)
			if !errors.Is(err, test.err) {
				t.Fatalf("error = %v, want %v", err, test.err)
			}
		})
	}

	if _, err := f.engine.SearchQuery(context.Background(), Scope{}, "Observation", ""); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("empty tenant error = %v, want ErrInvalidScope", err)
	}
}

func TestDeepChainIsBounded(t *testing.T) {
	f := newFixture()
	steps := []string{"subject:Patient"}
	for i := 0; i < 16; i++ {
		steps = append(steps, "link:Patient")
	}
	query := strings.Join(steps, ".") + ".name=Alice"
	_, err := f.engine.SearchQuery(context.Background(), f.p1Scope, "Encounter", query)
	if !errors.Is(err, ErrTooDeep) {
		t.Fatalf("deep chain error = %v, want ErrTooDeep", err)
	}

	// A configured lower bound is also enforced.
	engine := NewEngine(f.store, WithMaxChainHops(1))
	_, err = engine.SearchQuery(context.Background(), f.p1Scope, "Encounter",
		"subject:Patient.managingOrganization:Organization.name=Org One")
	if !errors.Is(err, ErrTooDeep) {
		t.Fatalf("configured depth error = %v, want ErrTooDeep", err)
	}
}

func TestParseReference(t *testing.T) {
	tests := []struct {
		raw  string
		want Reference
		ok   bool
	}{
		{raw: "Patient/p1", want: Reference{Type: "Patient", ID: "p1"}, ok: true},
		{raw: "/fhir/Patient/p1/_history/7", want: Reference{Type: "Patient", ID: "p1"}, ok: true},
		{raw: "https://example.test/fhir/Observation/o1", want: Reference{Type: "Observation", ID: "o1"}, ok: true},
		{raw: "p1", want: Reference{ID: "p1"}, ok: true},
		{raw: "Patient/..%2Fp2", ok: false},
		{raw: "Patient/p1?x=1", ok: false},
		{raw: "urn:uuid:1234", ok: false},
	}
	for _, test := range tests {
		got, err := ParseReference(test.raw)
		if test.ok {
			if err != nil || got != test.want {
				t.Fatalf("ParseReference(%q) = %+v, %v; want %+v", test.raw, got, err, test.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("ParseReference(%q) = %+v, want error", test.raw, got)
		}
	}
}

func TestConcurrentSearchesKeepScopesIsolated(t *testing.T) {
	f := newFixture()
	var wg sync.WaitGroup
	errs := make(chan error, 40)

	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			result, err := f.engine.SearchQuery(context.Background(), f.p1Scope, "Observation", "code=A")
			if err != nil {
				errs <- err
				return
			}
			for _, resource := range result.Resources {
				if resource.PatientID != "p1" {
					errs <- fmt.Errorf("p1 search returned %s owned by %s", resource.Key(), resource.PatientID)
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			result, err := f.engine.SearchQuery(context.Background(), f.p2Scope, "Observation", "code=A")
			if err != nil {
				errs <- err
				return
			}
			for _, resource := range result.Resources {
				if resource.PatientID != "p2" {
					errs <- fmt.Errorf("p2 search returned %s owned by %s", resource.Key(), resource.PatientID)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func mustValues(t *testing.T, raw string) url.Values {
	t.Helper()
	values, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return values
}
