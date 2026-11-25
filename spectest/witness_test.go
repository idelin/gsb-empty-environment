package spectest

import (
	"errors"
	netip "net/netip"
	"testing"

	. "prescreen.local/ps-c7499fbc67"
)

func TestWitnessOrderAndAddressBinding(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	w := mkTestWitness(witnessSpec{id: "w1", probe: "p1", version: "v1", attrID: "w1",
		observed: mkObserved(t, [2]int{1, 9}), cost: 1}, order)
	if err := w.Validate(order); err != nil {
		t.Fatalf("valid witness: %v", err)
	}

	changedOrder := order
	changedOrder.Nonce = "other-nonce"
	if err := w.Validate(changedOrder); !errors.Is(err, ErrOrderMismatch) {
		t.Fatalf("order digest mismatch = %v", err)
	}

	wrongAddr := w
	wrongAddr.ConnectedAddr = netip.AddrPortFrom(netip.MustParseAddr("1.1.1.1"), order.Port)
	if err := wrongAddr.Validate(order); !errors.Is(err, ErrAddressMismatch) {
		t.Fatalf("address mismatch = %v", err)
	}

	wrongPort := w
	wrongPort.ConnectedAddr = netip.AddrPortFrom(order.IP, order.Port+1)
	if err := wrongPort.Validate(order); !errors.Is(err, ErrAddressMismatch) {
		t.Fatalf("port mismatch = %v", err)
	}
}

func TestOrderDigestCoversEveryBindingField(t *testing.T) {
	order := testOrder(t, 1, 0, 10)
	base := order.Digest()
	mutations := []func(*Order){
		func(o *Order) { o.Port++ },
		func(o *Order) { o.Challenge = ChallengeTLSALPN01 },
		func(o *Order) { o.Account = "other-account" },
		func(o *Order) { o.Nonce = "other-nonce" },
		func(o *Order) { o.PublicKeyDigest = DigestBytes([]byte("other-key")) },
		func(o *Order) { o.MinDomains++ },
		func(o *Order) { o.AuthorizationWindow.End = tt(11) },
	}
	for i, mutate := range mutations {
		other := order
		mutate(&other)
		if other.Digest() == base {
			t.Fatalf("mutation %d did not change order digest", i)
		}
	}
}
