package ikecookie_test

import (
	"fmt"
	"net/netip"

	"example.org/netsec/q0752/ikecookie"
)

// ExampleAdmitter shows how a gateway wires the gate into IKE_SA_INIT
// handling: parse the message, ask for a verdict, and only run DH on Allow.
func ExampleAdmitter() {
	gate, err := ikecookie.NewAdmitter(
		ikecookie.WithHalfOpenLimit(2),
		ikecookie.WithRotationInterval(0), // manual rotation in this example
	)
	if err != nil {
		panic(err)
	}
	defer gate.Close()

	// Saturate the half-open table with two in-progress exchanges.
	for i := 0; i < 2; i++ {
		r := gate.Admit(ikecookie.InitRequest{
			SourceIP: netip.AddrFrom4([4]byte{10, 1, 0, byte(i)}),
			SPIi:     ikecookie.SPIi{byte(i)},
			Ni:       []byte("bulk"),
		})
		if r.Decision != ikecookie.DecisionAllow {
			panic("setup should be allowed")
		}
	}

	src := netip.MustParseAddr("203.0.113.5")
	spii := ikecookie.SPIi{0xAA, 0xBB}
	ni := []byte("initiator-nonce")

	// 1. Flood request with no COOKIE while saturated: challenge only, no DH.
	challenge := gate.Admit(ikecookie.InitRequest{SourceIP: src, SPIi: spii, Ni: ni})
	fmt.Println("no-cookie:", challenge.Decision)

	// 2. Retransmission carrying the matching COOKIE and identical tuple.
	ok := gate.Admit(ikecookie.InitRequest{
		SourceIP: src, SPIi: spii, Ni: ni, Cookie: challenge.Cookie,
	})
	fmt.Println("valid-cookie:", ok.Decision)
	if ok.Decision == ikecookie.DecisionAllow {
		defer ok.Release() // SA finished or abandoned
	}

	// 3. Attacker reuses the cookie but changes Ni.
	changedNi := gate.Admit(ikecookie.InitRequest{
		SourceIP: src, SPIi: spii, Ni: []byte("different-nonce"), Cookie: challenge.Cookie,
	})
	fmt.Println("changed-ni:", changedNi.Decision)

	// 4. Attacker replays the cookie from a different source address.
	changedSrc := gate.Admit(ikecookie.InitRequest{
		SourceIP: netip.MustParseAddr("203.0.113.6"), SPIi: spii, Ni: ni, Cookie: challenge.Cookie,
	})
	fmt.Println("changed-source:", changedSrc.Decision)

	// Output:
	// no-cookie: 2
	// valid-cookie: 1
	// changed-ni: 3
	// changed-source: 3
}
