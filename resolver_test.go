package netsec

import (
	"errors"
	"testing"
	"time"
)

// TestRootLabelLocalOnly verifies the central security property: a remote zone
// cannot explain the root label. The ego table is the sole source for the
// right-most label and no zone store lookup happens before it is consulted.
func TestRootLabelLocalOnly(t *testing.T) {
	r, egos, store, now := newTestResolver()

	rootKey := zoneKey(1)
	malicious := zoneKey(2)
	mustPutEgo(t, egos, "shop", rootKey)

	// The attacker's zone publishes a "shop" record identical to the local
	// root label. It must never be interpreted as a root binding.
	mustAddZone(t, store, malicious, map[string][]Record{
		"shop": {{Type: RecordPKEY, Value: malicious.Key, Expire: now.Add(time.Hour)}},
	})

	// A single-label name resolves straight from the ego table; the store is
	// never touched.
	res, err := r.Resolve("shop")
	if err != nil {
		t.Fatalf("resolve root label: %v", err)
	}
	if !res.Zone.Equal(rootKey) {
		t.Fatalf("root label hijacked: got %x want %x", res.Zone.Key, rootKey.Key)
	}
	if store.calls != 0 {
		t.Fatalf("remote store consulted for root label: %d lookups", store.calls)
	}

	// A name without an ego fails even though a remote zone advertises the
	// identical label.
	if _, err := r.Resolve("shop2"); !errors.Is(err, ErrEgoNotFound) {
		t.Fatalf("missing ego: got %v want %v", err, ErrEgoNotFound)
	}
}

// TestDelegationChain walks a normal multi-label chain and checks the returned
// zone and full delegation path, including a name fully consumed by PKEYs.
func TestDelegationChain(t *testing.T) {
	r, egos, store, now := newTestResolver()

	root, middle, leaf := zoneKey(1), zoneKey(2), zoneKey(3)
	mustPutEgo(t, egos, "example", root)
	mustAddZone(t, store, root, map[string][]Record{
		"www": {pkeyRecord(2, now.Add(time.Hour))},
	})
	mustAddZone(t, store, middle, map[string][]Record{
		"docs": {pkeyRecord(3, now.Add(time.Hour))},
	})
	mustAddZone(t, store, leaf, map[string][]Record{
		"v1": {{Type: RecordA, Value: []byte("127.0.0.1")}},
	})

	res, err := r.Resolve("v1.docs.www.example")
	if err != nil {
		t.Fatalf("resolve chain: %v", err)
	}
	if !res.Zone.Equal(leaf) {
		t.Fatalf("terminal zone: got %x want %x", res.Zone.Key, leaf.Key)
	}
	if len(res.Chain) != 3 ||
		!res.Chain[0].Equal(root) ||
		!res.Chain[1].Equal(middle) ||
		!res.Chain[2].Equal(leaf) {
		t.Fatalf("delegation chain mismatch: %x", res.Chain)
	}

	// All labels consumed by PKEY: the apex zone is the answer.
	res, err = r.Resolve("docs.www.example")
	if err != nil {
		t.Fatalf("resolve apex: %v", err)
	}
	if !res.Zone.Equal(leaf) || len(res.Chain) != 3 {
		t.Fatalf("apex result mismatch: zone=%x chainLen=%d", res.Zone.Key, len(res.Chain))
	}
}

// TestDelegationDepth builds a 10-zone chain: eight delegations are permitted,
// the ninth is rejected.
func TestDelegationDepth(t *testing.T) {
	r, egos, store, now := newTestResolver()

	const zones = 10
	mustPutEgo(t, egos, "deep", zoneKey(1))
	leftToRight := make([]string, 0, zones-1)
	for i := 1; i < zones; i++ {
		label := labelName(i)
		leftToRight = append(leftToRight, label)
		mustAddZone(t, store, zoneKey(byte(i)), map[string][]Record{
			label: {pkeyRecord(byte(i+1), now.Add(time.Hour))},
		})
	}

	// Dotted name must present l1 first after the root when read right-to-left,
	// so the dotted fragment is the traversal labels reversed.
	nameOf := func(hops int) string {
		fragment := make([]string, hops)
		for i := 0; i < hops; i++ {
			fragment[i] = leftToRight[hops-1-i]
		}
		return joinReverse(fragment) + ".deep"
	}

	res, err := r.Resolve(nameOf(8))
	if err != nil {
		t.Fatalf("8 delegations should succeed: %v", err)
	}
	if len(res.Chain) != 9 || !res.Chain[8].Equal(zoneKey(9)) {
		t.Fatalf("depth-8 chain wrong: %x", res.Chain)
	}

	if _, err := r.Resolve(nameOf(9)); !errors.Is(err, ErrDelegationTooDeep) {
		t.Fatalf("9 delegations: got %v want %v", err, ErrDelegationTooDeep)
	}
}
