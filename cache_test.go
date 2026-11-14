package netsec

import (
	"errors"
	"testing"
	"time"
)

func TestCacheIsolatesSameNameAcrossViews(t *testing.T) {
	cache := NewCache(ClockFunc(func() time.Time { return now }))
	if err := cache.SetPolicy(testPolicy("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetPolicy(testPolicy("beta")); err != nil {
		t.Fatal(err)
	}
	qname := MustParseName("ghost.example.")
	alpha := nsecNXResponse(t, "alpha", 3, 7)
	if _, err := cache.Ingest(alpha); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Lookup(Query{View: "beta", Name: qname, Type: TypeA, Class: ClassIN}); ok {
		t.Fatal("beta view reused alpha's NSEC interval")
	}

	beta := nsecNODATAExactResponse(t, "beta", qname, 3, 7)
	if _, err := cache.Ingest(beta); err != nil {
		t.Fatal(err)
	}
	alphaAnswer, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN})
	if !ok || alphaAnswer.Result != ResultNXDOMAIN {
		t.Fatalf("alpha answer = %+v, %v", alphaAnswer, ok)
	}
	betaAnswer, ok := cache.Lookup(Query{View: "beta", Name: qname, Type: TypeA, Class: ClassIN})
	if !ok || betaAnswer.Result != ResultNODATA {
		t.Fatalf("beta answer = %+v, %v", betaAnswer, ok)
	}
}

func TestCacheDoesNotMixNSEC3ViewIntervals(t *testing.T) {
	cache := NewCache(ClockFunc(func() time.Time { return now }))
	if err := cache.SetPolicy(testPolicy("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetPolicy(testPolicy("beta")); err != nil {
		t.Fatal(err)
	}
	params := ParamsNSEC3(2, []byte{0xaa, 0xbb})
	qname := MustParseName("hidden.example.")
	resp := nsec3NXResponse(t, "alpha", qname, params, 3, 7)
	if _, err := cache.Ingest(resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Lookup(Query{View: "beta", Name: qname, Type: TypeA, Class: ClassIN}); ok {
		t.Fatal("beta view reused alpha's NSEC3 interval")
	}
}

func TestCacheKeyBindsNSEC3Parameters(t *testing.T) {
	cache := NewCache(ClockFunc(func() time.Time { return now }))
	if err := cache.SetPolicy(testPolicy("alpha")); err != nil {
		t.Fatal(err)
	}
	qname := MustParseName("ghost.example.")
	p1 := ParamsNSEC3(0, []byte{0x01})
	p2 := ParamsNSEC3(9, []byte{0x02, 0x03})
	if _, err := cache.Ingest(nsec3NXResponse(t, "alpha", qname, p1, 3, 7)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Ingest(nsec3NXResponse(t, "alpha", qname, p2, 3, 7)); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN}); ok {
		t.Fatal("ambiguous entries with different NSEC3 parameters produced a hit")
	}
	if hit, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN, Parameters: &p1}); !ok || !hit.Parameters.Equal(p1) {
		t.Fatalf("p1 hit = %+v, %v", hit, ok)
	}
	if hit, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN, Parameters: &p2}); !ok || !hit.Parameters.Equal(p2) {
		t.Fatalf("p2 hit = %+v, %v", hit, ok)
	}
}

func TestKeyRotationInvalidatesOldProofs(t *testing.T) {
	cache := NewCache(ClockFunc(func() time.Time { return now }))
	if err := cache.SetPolicy(testPolicy("alpha")); err != nil {
		t.Fatal(err)
	}
	qname := MustParseName("ghost.example.")
	oldProof := nsecNXResponse(t, "alpha", 3, 7)
	if _, err := cache.Ingest(oldProof); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN}); !ok {
		t.Fatal("expected initial hit")
	}
	if err := cache.RevokeKeys("alpha", 4); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN}); ok {
		t.Fatal("old key epoch survived rotation")
	}
	if _, err := cache.Ingest(oldProof); !errors.Is(err, ErrKeyGeneration) {
		t.Fatalf("old proof after rotation: %v", err)
	}
	newProof := nsecNXResponse(t, "alpha", 4, 7)
	if _, err := cache.Ingest(newProof); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN}); !ok {
		t.Fatal("new key epoch proof did not cache")
	}
}

func TestPolicyChangeInvalidatesEntries(t *testing.T) {
	cache := NewCache(ClockFunc(func() time.Time { return now }))
	if err := cache.SetPolicy(testPolicy("alpha")); err != nil {
		t.Fatal(err)
	}
	qname := MustParseName("ghost.example.")
	oldProof := nsecNXResponse(t, "alpha", 3, 7)
	if _, err := cache.Ingest(oldProof); err != nil {
		t.Fatal(err)
	}
	next := testPolicy("alpha")
	next.Generation = 8
	if err := cache.SetPolicy(next); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN}); ok {
		t.Fatal("entry survived view policy generation change")
	}
	if _, err := cache.Ingest(oldProof); !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("old policy proof: %v", err)
	}
}

func TestCacheClockRollbackAndExpiry(t *testing.T) {
	clock := &fakeClock{when: now}
	cache := NewCache(clock)
	if err := cache.SetPolicy(testPolicy("alpha")); err != nil {
		t.Fatal(err)
	}
	qname := MustParseName("ghost.example.")
	if _, err := cache.Ingest(nsecNXResponse(t, "alpha", 3, 7)); err != nil {
		t.Fatal(err)
	}
	clock.when = now.Add(100 * time.Second)
	if _, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN}); !ok {
		t.Fatal("expected fresh hit")
	}
	clock.when = now.Add(-time.Second)
	if _, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN}); ok {
		t.Fatal("entry survived a clock rollback")
	}
	if cache.Rollbacks() != 1 {
		t.Fatalf("rollbacks = %d", cache.Rollbacks())
	}

	clock.when = now
	if _, err := cache.Ingest(nsecNXResponse(t, "alpha", 3, 7)); err != nil {
		t.Fatal(err)
	}
	clock.when = now.Add(301 * time.Second)
	if _, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN}); ok {
		t.Fatal("entry survived its negative TTL")
	}
}

func TestExpiredSignatureIsRejected(t *testing.T) {
	policy := testPolicy("alpha")
	resp := nsecNXResponse(t, "alpha", 3, 7)
	if _, err := EvaluateResponse(&resp, policy, now.Add(3*time.Hour)); !errors.Is(err, ErrProofExpired) {
		t.Fatalf("expired proof: %v", err)
	}
}

func TestCheckClockReportsRollback(t *testing.T) {
	clock := &fakeClock{when: now}
	cache := NewCache(clock)
	_ = cache.SetPolicy(testPolicy("alpha"))
	qname := MustParseName("ghost.example.")
	if _, err := cache.Ingest(nsecNXResponse(t, "alpha", 3, 7)); err != nil {
		t.Fatal(err)
	}
	clock.when = now.Add(-time.Second)
	if err := cache.CheckClock(); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("CheckClock = %v", err)
	}
	if _, ok := cache.Lookup(Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN}); ok {
		t.Fatal("rollback left a cached entry")
	}
}
