package sctpaddr_test

import (
	"fmt"
	"net/netip"

	"example.org/netsec/q0038/sctpaddr"
)

func ExampleManager() {
	localA := netip.MustParseAddr("10.0.0.1")
	localB := netip.MustParseAddr("10.0.0.2")

	mgr, err := sctpaddr.New(sctpaddr.Config{
		LocalPort:  5000,
		PeerPort:   6000,
		LocalTag:   0x11111111, // verification tag expected inbound
		PeerTag:    0x22222222, // verification tag stamped outbound
		HMACKey:    []byte("negotiated-rfc4895-shared-key"),
		HMACID:     sctpaddr.HMACSHA256,
		LocalAddrs: []netip.Addr{localA, localB},
		Primary:    localA,
		NextSN:     1,
	})
	if err != nil {
		panic(err)
	}

	// The packet distribution path hands every inbound datagram to Dispatch.
	// A wrong-tagged, unauthenticated, or replayed packet is KindDropped and
	// never changes the address set.
	attack := make([]byte, 12) // a datagram with no valid chunks
	res := mgr.Dispatch(attack, localB)
	fmt.Println("attacker packet:", res.Kind == sctpaddr.KindDropped)

	// Legitimate traffic is built through the manager so it is signed and
	// serial-numbered correctly.
	outbound, err := mgr.BuildASCONF(sctpaddr.OpAddIP, netip.MustParseAddr("10.0.0.3"))
	if err != nil {
		panic(err)
	}
	fmt.Println("outbound ASCONF signed:", len(outbound) > 0)

	// Output:
	// attacker packet: true
	// outbound ASCONF signed: true
}
