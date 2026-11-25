package spectest

import (
	"errors"
	"testing"

	. "prescreen.local/ps-c7499fbc67"
)

func TestGreedyDomainChoiceWouldHitRouteConflict(t *testing.T) {
	order := testOrder(t, 2, 0, 30)
	obs := mkObserved(t, [2]int{10, 20})
	attrs := []Attribution{
		mkAttr("a1", "pa", "alpha", "AS1", tt(0), tt(30)),
		mkAttr("a2", "pa2", "alpha", "AS2", tt(0), tt(30)),
		mkAttr("b1", "pb", "beta", "AS1", tt(0), tt(30)),
		mkAttr("b2", "pb2", "beta", "AS2", tt(0), tt(30)),
	}
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attrs...)},
		Witnesses: []Witness{
			mkTestWitness(witnessSpec{id: "a1", probe: "pa", version: "v1", attrID: "a1", observed: obs, cost: 1}, order),
			mkTestWitness(witnessSpec{id: "a2", probe: "pa2", version: "v1", attrID: "a2", observed: obs, cost: 10}, order),
			mkTestWitness(witnessSpec{id: "b1", probe: "pb", version: "v1", attrID: "b1", observed: obs, cost: 1}, order),
			mkTestWitness(witnessSpec{id: "b2", probe: "pb2", version: "v1", attrID: "b2", observed: obs, cost: 4}, order),
		},
		Now: tt(25),
	}
	b := mustAggregate(t, req)
	// The cheapest per-domain choice (a1+b1) shares AS1.  The correct
	// exhaustive choice is a1+b2.
	requireIDs(t, b, "a1", "b2")
	requireAccept(t, req, b)

	// Remove b2: only the conflicting pair remains.
	req.Witnesses = []Witness{req.Witnesses[0], req.Witnesses[2]}
	if _, err := Aggregate(req); !errors.Is(err, ErrNoValidEvidence) {
		t.Fatalf("Aggregate error = %v, want ErrNoValidEvidence", err)
	}
}

func TestAttributionChoiceAvoidsRouteConflict(t *testing.T) {
	order := testOrder(t, 2, 0, 20)
	obs := mkObserved(t, [2]int{5, 15})
	attrs := []Attribution{
		mkAttr("w1-x", "p1", "alpha", "AS1", tt(0), tt(20)),
		mkAttr("w1-y", "p1", "alpha", "AS2", tt(0), tt(20)),
		mkAttr("w2-x", "p2", "beta", "AS1", tt(0), tt(20)),
	}
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attrs...)},
		Witnesses: []Witness{
			// No AttributionID: the receipt has two legal identities, and the
			// aggregator must pick exactly one that makes the set consistent.
			mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", observed: obs, cost: 1}, order),
			mkTestWitness(witnessSpec{id: "w2", probe: "p2", version: "v1", attrID: "w2-x", observed: obs, cost: 1}, order),
		},
		Now: tt(19),
	}
	b := mustAggregate(t, req)
	requireIDs(t, b, "w1", "w2")
	if len(b.Evidence) != 2 {
		t.Fatalf("evidence = %+v", b.Evidence)
	}
	for _, ev := range b.Evidence {
		if ev.Witness.ID == "w1" && ev.AttributionID != "w1-y" {
			t.Fatalf("w1 chose %q, want w1-y", ev.AttributionID)
		}
	}
	requireAccept(t, req, b)
}

func TestPairwiseIntersectionIsNotEnough(t *testing.T) {
	order := testOrder(t, 3, 0, 10)
	x := [2]int{0, 1}
	y := [2]int{2, 3}
	z := [2]int{4, 5}
	attrs := []Attribution{
		mkAttr("a", "pa", "alpha", "AS1", tt(0), tt(10)),
		mkAttr("b", "pb", "beta", "AS2", tt(0), tt(10)),
		mkAttr("c", "pc", "gamma", "AS3", tt(0), tt(10)),
	}
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attrs...)},
		Witnesses: []Witness{
			mkTestWitness(witnessSpec{id: "a", probe: "pa", version: "v1", attrID: "a",
				observed: mkObserved(t, x, y), cost: 1}, order),
			mkTestWitness(witnessSpec{id: "b", probe: "pb", version: "v1", attrID: "b",
				observed: mkObserved(t, y, z), cost: 1}, order),
			mkTestWitness(witnessSpec{id: "c", probe: "pc", version: "v1", attrID: "c",
				observed: mkObserved(t, z, x), cost: 1}, order),
		},
		Now: tt(9),
	}
	if _, err := Aggregate(req); !errors.Is(err, ErrNoValidEvidence) {
		t.Fatalf("Aggregate error = %v, want ErrNoValidEvidence", err)
	}
}

func TestMultiSegmentObservationUnion(t *testing.T) {
	order := testOrder(t, 3, 0, 10)
	attrs := []Attribution{
		mkAttr("a", "pa", "alpha", "AS1", tt(0), tt(10)),
		mkAttr("b", "pb", "beta", "AS2", tt(0), tt(10)),
		mkAttr("c", "pc", "gamma", "AS3", tt(0), tt(10)),
	}
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attrs...)},
		Witnesses: []Witness{
			mkTestWitness(witnessSpec{id: "a", probe: "pa", version: "v1", attrID: "a",
				observed: mkObserved(t, [2]int{0, 2}, [2]int{4, 7}), cost: 1}, order),
			mkTestWitness(witnessSpec{id: "b", probe: "pb", version: "v1", attrID: "b",
				observed: mkObserved(t, [2]int{1, 3}, [2]int{5, 6}), cost: 1}, order),
			mkTestWitness(witnessSpec{id: "c", probe: "pc", version: "v1", attrID: "c",
				observed: mkObserved(t, [2]int{5, 6}, [2]int{7, 9}), cost: 1}, order),
		},
		Now: tt(9),
	}
	b := mustAggregate(t, req)
	requireIDs(t, b, "a", "b", "c")
	want := MustNewTimeSpan(Interval{Start: tt(5), End: tt(6)})
	if !b.CommonObservation.Equal(want) {
		t.Fatalf("common observation = %s, want %s", b.CommonObservation, want)
	}
	requireAccept(t, req, b)
}

func TestDuplicateReceiptsAreCountedOnce(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attr := mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10))
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attr)},
		Witnesses:  []Witness{w, w, w},
		Now:        tt(9),
	}
	b := mustAggregate(t, req)
	if len(b.Evidence) != 1 || b.Duplicates != 2 {
		t.Fatalf("evidence=%d duplicates=%d", len(b.Evidence), b.Duplicates)
	}
	requireAccept(t, req, b)
}

func TestDuplicateReceiptWithAndWithoutEmbeddedListDedupes(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attr := mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10))
	list := mkList("v1", attr)
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	wWithList := w
	wWithList.IdentityList = &list
	req := Request{
		Order:     order,
		Witnesses: []Witness{w, wWithList},
		Now:       tt(9),
	}
	b := mustAggregate(t, req)
	if len(b.Evidence) != 1 || b.Duplicates != 1 {
		t.Fatalf("evidence=%d duplicates=%d", len(b.Evidence), b.Duplicates)
	}
	requireAccept(t, req, b)
}

func TestForkedReceiptIsIsolated(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	attrs := []Attribution{
		mkAttr("w1", "p1", "alpha", "AS1", tt(0), tt(10)),
		mkAttr("w2", "p2", "beta", "AS2", tt(0), tt(10)),
	}
	obs := mkObserved(t, [2]int{1, 9})
	w1 := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: obs, cost: 1}, order)
	w1fork := w1
	w1fork.ProofDigest = DigestBytes([]byte("different-proof"))
	w2 := mkTestWitness(witnessSpec{id: "w2", probe: "p2", version: "v1", attrID: "w2",
		observed: obs, cost: 5}, order)
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attrs...)},
		Witnesses:  []Witness{w1, w1fork, w2},
		Now:        tt(9),
	}
	b := mustAggregate(t, req)
	if len(b.Forks) != 1 || b.Forks[0].WitnessID != "w1" ||
		b.Forks[0].Kind != ForkDifferentProofDigest {
		t.Fatalf("forks = %+v", b.Forks)
	}
	requireIDs(t, b, "w2")
	requireAccept(t, req, b)

	// A bundle that tries to use the forked id must be rejected independently.
	bad := Bundle{
		OrderDigest: order.Digest(),
		IssuedAt:    tt(9),
		Evidence: []Evidence{{
			Witness:       w1,
			AttributionID: "w1",
			Attribution:   attrs[0],
			Domain:        "alpha",
			RouteOrigin:   "AS1",
		}},
	}
	a := Audit(req, bad)
	if a.Valid || !errors.Is(a.Err(), ErrForkedReceipt) {
		t.Fatalf("Audit = %+v, err = %v", a, a.Err())
	}
}

func TestSelectionCostThenLexicographicID(t *testing.T) {
	order := testOrder(t, 2, 0, 10)
	obs := mkObserved(t, [2]int{1, 9})
	attrs := []Attribution{
		mkAttr("a1", "pa1", "alpha", "AS1", tt(0), tt(10)),
		mkAttr("a2", "pa2", "beta", "AS2", tt(0), tt(10)),
		mkAttr("b1", "pb1", "gamma", "AS3", tt(0), tt(10)),
		mkAttr("b2", "pb2", "delta", "AS4", tt(0), tt(10)),
	}
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attrs...)},
		Witnesses: []Witness{
			mkTestWitness(witnessSpec{id: "b1", probe: "pb1", version: "v1", attrID: "b1", observed: obs, cost: 1}, order),
			mkTestWitness(witnessSpec{id: "b2", probe: "pb2", version: "v1", attrID: "b2", observed: obs, cost: 1}, order),
			mkTestWitness(witnessSpec{id: "a1", probe: "pa1", version: "v1", attrID: "a1", observed: obs, cost: 1}, order),
			mkTestWitness(witnessSpec{id: "a2", probe: "pa2", version: "v1", attrID: "a2", observed: obs, cost: 1}, order),
		},
		Now: tt(9),
	}
	b := mustAggregate(t, req)
	requireIDs(t, b, "a1", "a2")

	// Make the b-pair cheaper; cost wins before ids.
	for i := range req.Witnesses {
		if req.Witnesses[i].ID == "b1" || req.Witnesses[i].ID == "b2" {
			req.Witnesses[i].Cost = 0
		}
	}
	b = mustAggregate(t, req)
	requireIDs(t, b, "b1", "b2")
}

func TestSelectMostMode(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	obs := mkObserved(t, [2]int{1, 9})
	attrs := []Attribution{
		mkAttr("a", "pa", "alpha", "AS1", tt(0), tt(10)),
		mkAttr("b", "pb", "beta", "AS2", tt(0), tt(10)),
		mkAttr("c", "pc", "gamma", "AS3", tt(0), tt(10)),
	}
	req := Request{
		Order:      order,
		Identities: []IdentityList{mkList("v1", attrs...)},
		Witnesses: []Witness{
			mkTestWitness(witnessSpec{id: "c", probe: "pc", version: "v1", attrID: "c", observed: obs, cost: 1}, order),
			mkTestWitness(witnessSpec{id: "b", probe: "pb", version: "v1", attrID: "b", observed: obs, cost: 1}, order),
			mkTestWitness(witnessSpec{id: "a", probe: "pa", version: "v1", attrID: "a", observed: obs, cost: 1}, order),
		},
		Now: tt(9),
	}
	few := mustAggregate(t, req)
	requireIDs(t, few, "a")
	req.Selection = SelectMost
	many := mustAggregate(t, req)
	requireIDs(t, many, "a", "b", "c")
	requireAccept(t, req, many)
}
