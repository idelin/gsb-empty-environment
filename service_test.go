package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceUsesPerViewUpstreamAndCachesLegitHit(t *testing.T) {
	cache := NewCache(ClockFunc(func() time.Time { return now }))
	if err := cache.SetPolicy(testPolicy("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetPolicy(testPolicy("beta")); err != nil {
		t.Fatal(err)
	}
	service := NewService(cache)
	qname := MustParseName("ghost.example.")
	alphaCalls, betaCalls := 0, 0
	if err := service.RegisterUpstream("alpha", UpstreamFunc(func(context.Context, Query) (*Response, error) {
		alphaCalls++
		resp := nsecNXResponse(t, "alpha", 3, 7)
		return &resp, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterUpstream("beta", UpstreamFunc(func(context.Context, Query) (*Response, error) {
		betaCalls++
		resp := nsecNODATAExactResponse(t, "beta", qname, 3, 7)
		return &resp, nil
	})); err != nil {
		t.Fatal(err)
	}

	query := Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN}
	alpha1, err := service.Query(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if alpha1.Result != ResultNXDOMAIN || alpha1.CacheHit {
		t.Fatalf("alpha first answer = %+v", alpha1)
	}
	query.View = "beta"
	beta1, err := service.Query(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if beta1.Result != ResultNODATA || beta1.CacheHit {
		t.Fatalf("beta first answer = %+v", beta1)
	}
	if alphaCalls != 1 || betaCalls != 1 {
		t.Fatalf("upstream calls alpha=%d beta=%d", alphaCalls, betaCalls)
	}

	query.View = "alpha"
	alpha2, err := service.Query(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if !alpha2.CacheHit || alpha2.Result != ResultNXDOMAIN {
		t.Fatalf("alpha cached answer = %+v", alpha2)
	}
	query.View = "beta"
	beta2, err := service.Query(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if !beta2.CacheHit || beta2.Result != ResultNODATA {
		t.Fatalf("beta cached answer = %+v", beta2)
	}
	if alphaCalls != 1 || betaCalls != 1 {
		t.Fatalf("cache hit called upstream: alpha=%d beta=%d", alphaCalls, betaCalls)
	}
}

func TestServiceRejectsWrongQuestionAndMissingUpstream(t *testing.T) {
	cache := NewCache(ClockFunc(func() time.Time { return now }))
	_ = cache.SetPolicy(testPolicy("alpha"))
	service := NewService(cache)
	qname := MustParseName("ghost.example.")
	query := Query{View: "alpha", Name: qname, Type: TypeA, Class: ClassIN}
	if _, err := service.Query(context.Background(), query); !errors.Is(err, ErrNoUpstream) {
		t.Fatalf("missing upstream: %v", err)
	}
	if err := service.RegisterUpstream("alpha", UpstreamFunc(func(context.Context, Query) (*Response, error) {
		resp := nsecNXResponse(t, "alpha", 3, 7)
		resp.Question.Name = MustParseName("other.example.")
		return &resp, nil
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Query(context.Background(), query); !errors.Is(err, ErrQuestionMismatch) {
		t.Fatalf("wrong upstream question: %v", err)
	}
	if cache.Len() != 0 {
		t.Fatal("mismatched proof was cached")
	}
}
