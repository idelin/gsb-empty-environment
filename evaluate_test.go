package netsec

import (
	"errors"
	"testing"
	"time"
)

func TestNSECProvesNXDOMAINAndCachesAcrossTypes(t *testing.T) {
	apex := testApex(t)
	policy := testPolicy("alpha")
	qname := MustParseName("ghost.example.")
	resp := testResponse("alpha", apex, qname, TypeA, ParamsNSEC(), 3, 7)
	resp.NSEC = []NSECRecord{
		testNSEC(t, apex, "g.example.", "h.example.", MustTypeBitmap(TypeA, TypeRRSIG, TypeNSEC), 3),
		testNSEC(t, apex, "example.", "a.example.", MustTypeBitmap(TypeSOA, TypeNS, TypeRRSIG, TypeNSEC), 3),
	}
	answer, err := EvaluateResponse(&resp, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Result != ResultNXDOMAIN {
		t.Fatalf("result = %s", answer.Result)
	}
	roles := map[WitnessRole]bool{}
	for _, witness := range answer.Witnesses {
		roles[witness.Role] = true
	}
	if !roles[WitnessNextCloserCover] || !roles[WitnessWildcardCover] {
		t.Fatalf("missing witnesses: %+v", answer.Witnesses)
	}

	cache := NewCache(ClockFunc(func() time.Time { return now }))
	if err := cache.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Ingest(resp); err != nil {
		t.Fatal(err)
	}
	for _, qtype := range []uint16{TypeA, TypeAAAA} {
		hit, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: qtype, Class: ClassIN})
		if !ok || !hit.CacheHit || hit.Result != ResultNXDOMAIN {
			t.Fatalf("cache hit for type %d: %+v, %v", qtype, hit, ok)
		}
	}
}

func TestNSECNODATAUsesExactBitmap(t *testing.T) {
	apex := testApex(t)
	policy := testPolicy("alpha")
	qname := MustParseName("a.example.")
	resp := testResponse("alpha", apex, qname, TypeAAAA, ParamsNSEC(), 3, 7)
	resp.NSEC = []NSECRecord{
		testNSEC(t, apex, "a.example.", "b.example.", MustTypeBitmap(TypeA, TypeRRSIG, TypeNSEC), 3),
	}
	answer, err := EvaluateResponse(&resp, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Result != ResultNODATA {
		t.Fatalf("result = %s", answer.Result)
	}
	if len(answer.Witnesses) != 1 || answer.Witnesses[0].Role != WitnessExactMatch {
		t.Fatalf("witnesses = %+v", answer.Witnesses)
	}

	resp.Question.Type = TypeA
	if _, err := EvaluateResponse(&resp, policy, now); !errors.Is(err, ErrPositive) {
		t.Fatalf("A query should be positive, got %v", err)
	}

	cache := NewCache(ClockFunc(func() time.Time { return now }))
	_ = cache.SetPolicy(policy)
	resp.Question.Type = TypeAAAA
	if _, err := cache.Ingest(resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeAAAA, Class: ClassIN}); !ok {
		t.Fatal("missing NODATA cache hit")
	}
	if _, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN}); ok {
		t.Fatal("cached NODATA incorrectly answered a present type")
	}
}

func TestNSECEmptyNonTerminal(t *testing.T) {
	apex := testApex(t)
	policy := testPolicy("alpha")
	qname := MustParseName("b.example.")
	resp := testResponse("alpha", apex, qname, TypeAAAA, ParamsNSEC(), 3, 7)
	resp.NSEC = []NSECRecord{
		testNSEC(t, apex, "x.b.example.", "y.b.example.", MustTypeBitmap(TypeA, TypeRRSIG, TypeNSEC), 3),
	}
	answer, err := EvaluateResponse(&resp, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Result != ResultNODATA || !hasRole(answer.Witnesses, WitnessEmptyNonTerminal) {
		t.Fatalf("ENT result = %s, witnesses=%+v", answer.Result, answer.Witnesses)
	}
}

func TestNSECWildcardExistsPreventsNXDOMAIN(t *testing.T) {
	apex := testApex(t)
	policy := testPolicy("alpha")
	qname := MustParseName("ghost.example.")
	resp := testResponse("alpha", apex, qname, TypeA, ParamsNSEC(), 3, 7)
	resp.NSEC = []NSECRecord{
		testNSEC(t, apex, "*.example.", "a.example.", MustTypeBitmap(TypeA, TypeRRSIG, TypeNSEC), 3),
		testNSEC(t, apex, "g.example.", "h.example.", MustTypeBitmap(TypeA, TypeRRSIG, TypeNSEC), 3),
	}
	if _, err := EvaluateResponse(&resp, policy, now); !errors.Is(err, ErrPositive) {
		t.Fatalf("wildcard existing A query: %v", err)
	}

	resp.Question.Type = TypeAAAA
	answer, err := EvaluateResponse(&resp, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Result != ResultNODATA || !hasRole(answer.Witnesses, WitnessWildcardMatch) {
		t.Fatalf("wildcard NODATA = %s, %+v", answer.Result, answer.Witnesses)
	}
}

func TestNSECDelegationIsNotNXDOMAIN(t *testing.T) {
	apex := testApex(t)
	policy := testPolicy("alpha")
	qname := MustParseName("child.example.")
	resp := testResponse("alpha", apex, qname, TypeA, ParamsNSEC(), 3, 7)
	resp.NSEC = []NSECRecord{
		testNSEC(t, apex, "child.example.", "d.example.", MustTypeBitmap(TypeNS, TypeRRSIG, TypeNSEC), 3),
	}
	if _, err := EvaluateResponse(&resp, policy, now); !errors.Is(err, ErrDelegation) {
		t.Fatalf("non-DS at delegation: %v", err)
	}
	resp.Question.Type = TypeDS
	answer, err := EvaluateResponse(&resp, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Result != ResultNODATA {
		t.Fatalf("DS NODATA = %s", answer.Result)
	}
}

func TestNSEC3ProvesNXDOMAIN(t *testing.T) {
	apex := testApex(t)
	policy := testPolicy("alpha")
	params := ParamsNSEC3(2, []byte{0xaa, 0xbb})
	qname := MustParseName("ghost.example.")
	ceHash := mustHash(t, params, apex)
	nextHash := mustHash(t, params, MustParseName("ghost.example."))
	nextOwner, nextNext := coverHashes(t, nextHash)
	wildcard := MustParseName("*.example.")
	wildHash := mustHash(t, params, wildcard)
	wildOwner, wildNext := coverHashes(t, wildHash)
	resp := testResponse("alpha", apex, qname, TypeA, params, 3, 7)
	resp.NSEC3 = []NSEC3Record{
		testNSEC3Hash(t, apex, params, ceHash, ceHash, MustTypeBitmap(TypeSOA, TypeNS), false, 3),
		testNSEC3Hash(t, apex, params, nextOwner, nextNext, MustTypeBitmap(), false, 3),
		testNSEC3Hash(t, apex, params, wildOwner, wildNext, MustTypeBitmap(), false, 3),
	}
	answer, err := EvaluateResponse(&resp, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Result != ResultNXDOMAIN {
		t.Fatalf("result = %s", answer.Result)
	}
	for _, role := range []WitnessRole{WitnessClosestEncloser, WitnessNextCloserCover, WitnessWildcardCover} {
		if !hasRole(answer.Witnesses, role) {
			t.Fatalf("missing role %s in %+v", role, answer.Witnesses)
		}
	}
}

func TestNSEC3OptOutUnsignedDelegation(t *testing.T) {
	apex := testApex(t)
	policy := testPolicy("alpha")
	params := ParamsNSEC3(2, []byte{0xaa, 0xbb})
	qname := MustParseName("child.example.")
	ceHash := mustHash(t, params, apex)
	childHash := mustHash(t, params, qname)
	coverOwner, coverNext := coverHashes(t, childHash)
	resp := testResponse("alpha", apex, qname, TypeDS, params, 3, 7)
	resp.Delegations = []Delegation{{Name: qname, Signed: false}}
	resp.NSEC3 = []NSEC3Record{
		testNSEC3Hash(t, apex, params, ceHash, ceHash, MustTypeBitmap(TypeSOA, TypeNS), false, 3),
		testNSEC3Hash(t, apex, params, coverOwner, coverNext, MustTypeBitmap(), true, 3),
	}
	answer, err := EvaluateResponse(&resp, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Result != ResultNODATA || !hasRole(answer.Witnesses, WitnessOptOutCover) {
		t.Fatalf("DS opt-out = %s, %+v", answer.Result, answer.Witnesses)
	}

	resp.Question.Type = TypeA
	if _, err := EvaluateResponse(&resp, policy, now); !errors.Is(err, ErrDelegation) {
		t.Fatalf("A at unsigned delegation: %v", err)
	}

	// Even without explicit delegation metadata, an opt-out interval covering
	// an ordinary name must not produce NXDOMAIN.
	noMeta := resp
	noMeta.Delegations = nil
	noMeta.Question.Type = TypeA
	if _, err := EvaluateResponse(&noMeta, policy, now); !errors.Is(err, ErrOptOut) {
		t.Fatalf("opt-out coverage for A: %v", err)
	}

	// A DS question at the covered name is conservatively NODATA, not NXDOMAIN.
	noMeta.Question.Type = TypeDS
	dsAnswer, err := EvaluateResponse(&noMeta, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if dsAnswer.Result != ResultNODATA || !hasRole(dsAnswer.Witnesses, WitnessOptOutCover) {
		t.Fatalf("meta-free DS opt-out = %s, %+v", dsAnswer.Result, dsAnswer.Witnesses)
	}
}

func TestNSEC3WildcardExists(t *testing.T) {
	apex := testApex(t)
	policy := testPolicy("alpha")
	params := ParamsNSEC3(2, []byte{0xaa, 0xbb})
	qname := MustParseName("ghost.example.")
	ceHash := mustHash(t, params, apex)
	nextHash := mustHash(t, params, qname)
	nextOwner, nextNext := coverHashes(t, nextHash)
	wildcard := MustParseName("*.example.")
	wildHash := mustHash(t, params, wildcard)
	resp := testResponse("alpha", apex, qname, TypeA, params, 3, 7)
	resp.NSEC3 = []NSEC3Record{
		testNSEC3Hash(t, apex, params, ceHash, ceHash, MustTypeBitmap(TypeSOA, TypeNS), false, 3),
		testNSEC3Hash(t, apex, params, nextOwner, nextNext, MustTypeBitmap(), false, 3),
		testNSEC3Hash(t, apex, params, wildHash, wildHash, MustTypeBitmap(TypeA), false, 3),
	}
	if _, err := EvaluateResponse(&resp, policy, now); !errors.Is(err, ErrPositive) {
		t.Fatalf("wildcard A query: %v", err)
	}
	resp.Question.Type = TypeAAAA
	answer, err := EvaluateResponse(&resp, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Result != ResultNODATA || !hasRole(answer.Witnesses, WitnessWildcardMatch) {
		t.Fatalf("wildcard AAAA = %s, %+v", answer.Result, answer.Witnesses)
	}
}

func TestNSEC3EmptyNonTerminal(t *testing.T) {
	apex := testApex(t)
	policy := testPolicy("alpha")
	params := ParamsNSEC3(2, []byte{0xaa, 0xbb})
	qname := MustParseName("b.example.")
	h := mustHash(t, params, qname)
	resp := testResponse("alpha", apex, qname, TypeA, params, 3, 7)
	resp.NSEC3 = []NSEC3Record{
		testNSEC3Hash(t, apex, params, h, h, MustTypeBitmap(), false, 3),
	}
	answer, err := EvaluateResponse(&resp, policy, now)
	if err != nil {
		t.Fatal(err)
	}
	if answer.Result != ResultNODATA || !answer.Witnesses[0].NSEC3.Types.Empty() {
		t.Fatalf("ENT = %s, %+v", answer.Result, answer.Witnesses)
	}
}

func hasRole(witnesses []Witness, role WitnessRole) bool {
	for _, witness := range witnesses {
		if witness.Role == role {
			return true
		}
	}
	return false
}
