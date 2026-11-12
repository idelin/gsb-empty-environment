package ndadmit_test

import (
	"errors"
	"fmt"
	"net/netip"

	"example.org/netsec/q0153/ndadmit"
)

// A host passes the IPv6 hop limit, addresses and the raw ICMPv6 payload into
// the admission parser before touching any Neighbor Discovery state.
func ExampleParse() {
	src := netip.MustParseAddr("fe80::1")
	dst := netip.MustParseAddr("ff02::1")

	// Neighbor Solicitation: type 135, code 0, checksum 0 (stack-verified),
	// reserved word, target address, then an SLLA option.
	payload := []byte{
		135, 0, 0, 0,
		0, 0, 0, 0,
		0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xab, 0xcd,
		1, 1, 0x02, 0, 0, 0, 0, 1,
	}

	msg, err := ndadmit.Parse(ndadmit.HopLimit, src, dst, payload)
	if err != nil {
		switch {
		case errors.Is(err, ndadmit.ErrZeroOption):
			fmt.Println("reject: zero-length option")
		case errors.Is(err, ndadmit.ErrDuplicateOption):
			fmt.Println("reject: duplicate identity option")
		default:
			fmt.Println("reject:", err)
		}
		return
	}
	fmt.Printf("admitted type %d with %d option(s)\n", msg.Type, msg.OptionCount)
	// Output: admitted type 135 with 1 option(s)
}
