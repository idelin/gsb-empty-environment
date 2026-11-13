package netsec

import "fmt"

// ASN is a 32-bit Autonomous System Number.
type ASN uint32

const (
	// ASNTrans is the AS_TRANS value reserved for 32-bit ASN transitions.
	ASNTrans ASN = 23456
	// ASNMax is the largest possible 32-bit ASN.
	ASNMax ASN = 1<<32 - 1
)

// IsReservedASN reports whether an ASN is reserved by IANA or by the BGP
// protocol and therefore must not be accepted as a routing identity.
//
// Private-use and documentation ASNs remain valid for a host: they are
// special-purpose allocations, not reserved values. Hosts that have a stricter
// local policy can reject them before calling this package.
func IsReservedASN(asn ASN) bool {
	switch {
	case asn == 0, asn == ASNTrans, asn == 65535, asn == ASNMax:
		return true
	case asn >= 64496 && asn <= 64511:
		return true
	case asn >= 65536 && asn <= 65551:
		return true
	default:
		return false
	}
}

// ValidateASN rejects zero, reserved, and out-of-range ASNs.
func ValidateASN(asn ASN) error {
	if asn == 0 || IsReservedASN(asn) {
		return fmt.Errorf("%w: %d", ErrInvalidASN, uint32(asn))
	}
	return nil
}
