package spectest

import (
	"errors"
	"testing"

	. "prescreen.local/ps-c7499fbc67"
)

func TestAuditIgnoresAggregatorClaims(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attr := mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10))
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attr)},
		Witnesses:  []Witness{w},
		Now:        tt(9),
	}
	b := mustAggregate(t, req)

	b.ClaimedDomains = 999
	b.ClaimedWitnessCount = 0
	b.ClaimedTotalCost = -1
	b.ClaimedPublicIP = false
	b.CommonObservation = TimeSpan{}
	b.ClaimedDomainsList = []string{"not-the-real-domain"}
	// The denormalized evidence fields are not used for policy decisions.
	b.Evidence[0].Domain = "attacker-domain"
	b.Evidence[0].RouteOrigin = "AS9999"
	b.Evidence[0].Attribution.Domain = "attacker-domain"

	a := Audit(req, b)
	if !a.Valid {
		t.Fatalf("Audit trusted bad claims: %v", a.Err())
	}
	if len(a.Domains) != 1 || a.Domains[0] != "alpha" {
		t.Fatalf("audit domains = %v", a.Domains)
	}
	if len(a.Origins) != 1 || a.Origins[0] != "AS1" {
		t.Fatalf("audit origins = %v", a.Origins)
	}
}

func TestAuditRecomputesDomainCount(t *testing.T) {
	order := testOrder(t, 2, 0, 10)
	obs := mkObserved(t, [2]int{1, 9})
	attrs := []Attribution{
		mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10)),
		mkAttr("w2", "p2", "alpha", "AS2", tt(0), tt(10)),
	}
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attrs...)},
		Witnesses: []Witness{
			mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1", observed: obs, cost: 1}, order),
			mkTestWitness(witnessSpec{id: "w2", probe: "p2", version: "v1", attrID: "w2", observed: obs, cost: 1}, order),
		},
		Now: tt(9),
	}
	if _, err := Aggregate(req); !errors.Is(err, ErrNoValidEvidence) {
		t.Fatalf("Aggregate error = %v", err)
	}
	bundle := Bundle{
		OrderDigest:     order.Digest(),
		IssuedAt:        tt(9),
		ClaimedDomains:  2,
		ClaimedPublicIP: true,
		Evidence: []Evidence{
			{Witness: req.Witnesses[0], AttributionID: "w1", Attribution: attrs[0], Domain: "alpha", RouteOrigin: "AS1"},
			{Witness: req.Witnesses[1], AttributionID: "w2", Attribution: attrs[1], Domain: "alpha", RouteOrigin: "AS2"},
		},
		CommonObservation: obs,
	}
	a := Audit(req, bundle)
	if a.Valid || !errors.Is(a.Err(), ErrInsufficientDomains) {
		t.Fatalf("Audit = %+v, err = %v; want insufficient domains", a, a.Err())
	}
}

func TestAuditRecomputesGlobalTimeIntersection(t *testing.T) {
	order := testOrder(t, 2, 0, 10)
	attrs := []Attribution{
		mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10)),
		mkAttr("w2", "p2", "beta", "AS2", tt(0), tt(10)),
	}
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attrs...)},
		Witnesses: []Witness{
			mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
				observed: mkObserved(t, [2]int{0, 3}), cost: 1}, order),
			mkTestWitness(witnessSpec{id: "w2", probe: "p2", version: "v1", attrID: "w2",
				observed: mkObserved(t, [2]int{4, 7}), cost: 1}, order),
		},
		Now: tt(9),
	}
	if _, err := Aggregate(req); !errors.Is(err, ErrNoValidEvidence) {
		t.Fatalf("Aggregate error = %v", err)
	}
	bundle := Bundle{
		OrderDigest:       order.Digest(),
		IssuedAt:          tt(9),
		ClaimedDomains:    2,
		ClaimedPublicIP:   true,
		CommonObservation: MustNewTimeSpan(Interval{Start: tt(0), End: tt(10)}),
		Evidence: []Evidence{
			{Witness: req.Witnesses[0], AttributionID: "w1", Attribution: attrs[0]},
			{Witness: req.Witnesses[1], AttributionID: "w2", Attribution: attrs[1]},
		},
	}
	a := Audit(req, bundle)
	if a.Valid || !errors.Is(a.Err(), ErrNoCommonInstant) {
		t.Fatalf("Audit = %+v, err = %v; want no common instant", a, a.Err())
	}
}

func TestAuditRejectsTamperedWitnessAgainstOriginals(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attr := mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10))
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attr)},
		Witnesses:  []Witness{w},
		Now:        tt(9),
	}
	b := mustAggregate(t, req)
	b.Evidence[0].Witness.Cost = 0
	if err := Accept(req, b); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("Accept tampered bundle = %v, want ErrInvalidBundle", err)
	}
}

func TestAuditDeduplicatesExactEvidence(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attr := mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10))
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attr)},
		Witnesses:  []Witness{w},
		Now:        tt(9),
	}
	b := mustAggregate(t, req)
	b.Evidence = append(b.Evidence, b.Evidence[0])
	a := Audit(req, b)
	if !a.Valid {
		t.Fatalf("Audit duplicate evidence: %v", a.Err())
	}
	if a.Duplicates != 1 {
		t.Fatalf("audit duplicates = %d", a.Duplicates)
	}
}

func TestAuditRejectsForkInsideBundle(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attr := mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10))
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attr)},
		Witnesses:  []Witness{w},
		Now:        tt(9),
	}
	b := mustAggregate(t, req)
	forked := b.Evidence[0]
	forked.Witness.ProofDigest = DigestBytes([]byte("other"))
	forked.AttributionID = "w1"
	b.Evidence = append(b.Evidence, forked)
	a := Audit(req, b)
	if a.Valid || !errors.Is(a.Err(), ErrForkedReceipt) {
		t.Fatalf("Audit = %+v, err = %v; want fork", a, a.Err())
	}
}

func TestAuditRequiresAuthenticatedIdentityByDefault(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attr := mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10))
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	bundle := Bundle{
		OrderDigest: order.Digest(),
		IssuedAt:    tt(9),
		Evidence: []Evidence{{
			Witness:       w,
			AttributionID: "w1",
			Attribution:   attr,
			Domain:        "alpha",
			RouteOrigin:   "AS1",
		}},
	}
	if err := Accept(Request{Order: order, Now: tt(9)}, bundle); !errors.Is(err, ErrMissingIdentity) {
		t.Fatalf("Accept without identity = %v, want ErrMissingIdentity", err)
	}
	// The explicit escape hatch lets a caller assert that the inline
	// attribution is part of signed evidence.
	if err := Accept(Request{Order: order, Now: tt(9), TrustInlineAttribution: true}, bundle); err != nil {
		t.Fatalf("Accept with inline trust: %v", err)
	}
}
