package spectest

import (
	"errors"
	"strings"
	"testing"

	. "prescreen.local/ps-c7499fbc67"
)

func TestMissingIdentityCanBeSupplemented(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attr := mkAttr("attr1", "p1", "alpha", "AS1", tt(0), tt(10))
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "attr1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	req := Request{
		Order:     order,
		Witnesses: []Witness{w},
		Now:       tt(9),
	}
	if _, err := Aggregate(req); !errors.Is(err, ErrNoValidEvidence) {
		t.Fatalf("Aggregate without identity error = %v", err)
	}

	req.Supplements = []IdentitySupplement{{
		WitnessID:   "w1",
		Version:     "v1",
		Attribution: attr,
	}}
	b := mustAggregate(t, req)
	if len(b.Evidence) != 1 || b.Evidence[0].AttributionID != "attr1" {
		t.Fatalf("evidence = %+v", b.Evidence)
	}
	requireAccept(t, req, b)

	// The bundle carries the supplement as evidence, so a historical verifier
	// that only retains the order and bundle can still revalidate it.
	onlyBundle := Request{Order: order, Now: tt(9)}
	if a := Audit(onlyBundle, b); !a.Valid {
		t.Fatalf("bundle-only audit failed: %v", a.Err())
	}
}

func TestSupplementDoesNotLeakToOtherWitnesses(t *testing.T) {
	order := testOrder(t, 2, 0, 10)
	obs := mkObserved(t, [2]int{1, 9})
	a1 := mkAttr("a1", "p1", "alpha", "AS1", tt(0), tt(10))
	w1 := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "a1", observed: obs, cost: 1}, order)
	w2 := mkTestWitness(witnessSpec{id: "w2", probe: "p2", version: "v1", attrID: "a2", observed: obs, cost: 1}, order)
	req := Request{
		Order:     order,
		Witnesses: []Witness{w1, w2},
		Supplements: []IdentitySupplement{{
			WitnessID:   "w1",
			Version:     "v1",
			Attribution: a1,
		}},
		Now: tt(9),
	}
	b, err := Aggregate(req)
	if !errors.Is(err, ErrNoValidEvidence) {
		t.Fatalf("Aggregate error = %v, want no valid evidence", err)
	}
	foundW2Missing := false
	for _, r := range b.Rejections {
		if r.WitnessID == "w2" && strings.Contains(r.Reason, ErrMissingIdentity.Error()) {
			foundW2Missing = true
		}
	}
	if !foundW2Missing {
		t.Fatalf("w2 should still lack identity; rejections = %+v", b.Rejections)
	}
}

func TestSelectionOnlySupplementPinsOneAttribution(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attrs := []Attribution{
		mkAttr("a", "p1", "alpha", "AS1", tt(0), tt(10)),
		mkAttr("b", "p1", "beta", "AS2", tt(0), tt(10)),
	}
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attrs...)},
		Witnesses:  []Witness{w},
		Supplements: []IdentitySupplement{{
			WitnessID:           "w1",
			Version:             "v1",
			SelectAttributionID: "b",
		}},
		Now: tt(9),
	}
	b := mustAggregate(t, req)
	if len(b.Evidence) != 1 || b.Evidence[0].AttributionID != "b" {
		t.Fatalf("evidence = %+v", b.Evidence)
	}
	requireAccept(t, req, b)
}

func TestGlobalSupplementAugmentsCatalogAndEmbeddedList(t *testing.T) {
	order := testOrder(t, 2, 0, 10)
	obs := mkObserved(t, [2]int{1, 9})
	baseAttr := mkAttr("a", "p1", "alpha", "AS1", tt(0), tt(10))
	extraAttr := mkAttr("b", "p2", "beta", "AS2", tt(0), tt(10))
	baseList := mkList("v1", baseAttr)
	w1 := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "a",
		observed: obs, cost: 1}, order)
	w1.IdentityList = &baseList
	w2 := mkTestWitness(witnessSpec{id: "w2", probe: "p2", version: "v1", attrID: "b",
		observed: obs, cost: 1}, order)
	req := Request{
		Order:      order,
		Identities: []IdentityList{baseList},
		Witnesses:  []Witness{w1, w2},
		Supplements: []IdentitySupplement{{
			Version:     "v1",
			Attribution: extraAttr,
		}},
		Now: tt(9),
	}
	b := mustAggregate(t, req)
	requireIDs(t, b, "w1", "w2")
	requireAccept(t, req, b)
}

func TestAllAttributionChoicesMustHoldSimultaneously(t *testing.T) {
	order := testOrder(t, 2, 0, 10)
	attrs := []Attribution{
		mkAttr("w1-early", "p1", "alpha", "AS1", tt(0), tt(4)), // valid [0,4)
		mkAttr("w1-late", "p1", "gamma", "AS3", tt(6), tt(10)), // valid [6,10)
		mkAttr("w2", "p2", "beta", "AS2", tt(0), tt(10)),
	}
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attrs...)},
		Witnesses: []Witness{
			// Each of w1's choices is non-empty against its own observation,
			// but no single choice overlaps w2 at a common instant.
			mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1",
				observed: mkObserved(t, [2]int{0, 10}), cost: 1}, order),
			mkTestWitness(witnessSpec{id: "w2", probe: "p2", version: "v1", attrID: "w2",
				observed: mkObserved(t, [2]int{4, 6}), cost: 1}, order),
		},
		Now: tt(9),
	}
	if _, err := Aggregate(req); !errors.Is(err, ErrNoValidEvidence) {
		t.Fatalf("Aggregate error = %v, want no valid evidence", err)
	}
}

func TestRevocationBeforeIssuanceBlocks(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attr := mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10))
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attr)},
		Witnesses:  []Witness{w},
		Revocations: []Revocation{{
			Kind: RevokeProbe, Subject: "p1", RevokedAt: tt(4),
		}},
		Now: tt(5),
	}
	if _, err := Aggregate(req); !errors.Is(err, ErrNoValidEvidence) {
		t.Fatalf("Aggregate error = %v, want no valid evidence", err)
	}
}

func TestRevocationAfterIssuanceDoesNotPoisonHistory(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attr := mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10))
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attr)},
		Witnesses:  []Witness{w},
		Now:        tt(5),
	}
	b := mustAggregate(t, req)
	if !b.IssuedAt.Equal(tt(5)) {
		t.Fatalf("IssuedAt = %v", b.IssuedAt)
	}

	// The revocation happens later and must not retroactively invalidate the
	// bundle when revalidated at its own issuance time.
	later := req
	later.Now = tt(9)
	later.Revocations = []Revocation{{
		Kind: RevokeProbe, Subject: "p1", RevokedAt: tt(6),
	}}
	requireAccept(t, later, b)

	// Re-stamping the bundle at a later time changes the historical question.
	tampered := b
	tampered.IssuedAt = tt(7)
	if err := Accept(later, tampered); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Accept at later time = %v, want ErrRevoked", err)
	}
}

func TestRevocationAtExactIssuanceTimeApplies(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attr := mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10))
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attr)},
		Witnesses:  []Witness{w},
		Revocations: []Revocation{{
			Kind: RevokeProbe, Subject: "p1", RevokedAt: tt(5),
		}},
		Now: tt(5),
	}
	if _, err := Aggregate(req); !errors.Is(err, ErrNoValidEvidence) {
		t.Fatalf("Aggregate error = %v, want no valid evidence", err)
	}
}

func TestRouteOriginAndRouteProofRevocation(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attr := mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10))
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	w.RouteProof = DigestBytes([]byte("route-proof"))
	base := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attr)},
		Witnesses:  []Witness{w},
		Now:        tt(5),
	}
	for _, rev := range []Revocation{
		{Kind: RevokeRouteOrigin, Subject: "as1", RevokedAt: tt(4)},
		{Kind: RevokeRouteProof, Subject: w.RouteProof.Hex(), RevokedAt: tt(4)},
		{Kind: RevokeRouteProof, Subject: "AS1", RevokedAt: tt(4)},
	} {
		req := base
		req.Revocations = []Revocation{rev}
		if _, err := Aggregate(req); !errors.Is(err, ErrNoValidEvidence) {
			t.Fatalf("revocation %+v: error = %v, want no valid evidence", rev, err)
		}
	}

	// Independent revalidation must also see the route-proof digest carried by
	// the receipt, even when the identity attribution itself has no copy.
	locally := base
	locally.Revocations = nil
	b := mustAggregate(t, locally)
	locally.Revocations = []Revocation{{
		Kind: RevokeRouteProof, Subject: w.RouteProof.Hex(), RevokedAt: tt(4),
	}}
	if err := Accept(locally, b); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Accept route-proof revocation = %v, want ErrRevoked", err)
	}
}
