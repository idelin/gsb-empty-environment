package admission

import (
	"fmt"
	"net/netip"
)

// ROA is one Route Origin Authorization entry: AS MaxLen is permitted to
// originate prefixes within Prefix up to MaxLen bits. A MaxLen of zero means
// only the exact prefix length is authorized.
type ROA struct {
	AS     uint32
	Prefix netip.Prefix
	MaxLen int
}

// NewROA parses a ROA from its textual prefix. A non-positive maxLen defaults
// to the prefix length, which authorizes the exact prefix only.
func NewROA(as uint32, prefix string, maxLen int) (ROA, error) {
	p, err := netip.ParsePrefix(prefix)
	if err != nil {
		return ROA{}, fmt.Errorf("invalid ROA prefix %q: %w", prefix, err)
	}
	if maxLen <= 0 {
		maxLen = p.Bits()
	}
	return ROA{AS: as, Prefix: p.Masked(), MaxLen: maxLen}, nil
}

// MustROA panics if prefix cannot be parsed; it is intended for tests and
// static configuration tables.
func MustROA(as uint32, prefix string, maxLen int) ROA {
	r, err := NewROA(as, prefix, maxLen)
	if err != nil {
		panic(err)
	}
	return r
}

// RPKI state values follow RFC 6483 section 2, plus the undetermined origin
// treatment of RFC 6811 for AS_SET routes.
const (
	RPKIValid    = "VALID"
	RPKINotFound = "NOT_FOUND"
	RPKIInvalid  = "INVALID"
)

type rpkiResult struct {
	state  string
	detail string
}

// covers reports whether the ROA covers the announced prefix: the prefix must
// be numerically inside the ROA prefix and no longer than the ROA max length.
func (r ROA) covers(announced netip.Prefix) bool {
	if r.Prefix.Addr().BitLen() != announced.Addr().BitLen() || r.Prefix.Bits() > announced.Bits() {
		return false
	}
	return r.Prefix.Contains(announced.Addr()) && announced.Bits() <= r.MaxLen
}

// validate implements origin validation (RFC 6483).
//
//   - No covering ROA: NOT_FOUND.
//   - Known origin and a covering ROA for the same AS: VALID.
//   - Known origin and covering ROAs only for other ASes: INVALID.
//   - AS 0 ROAs only invalidate; they can never make a route VALID.
//   - RFC 6811 AS_SET routes have no determined origin (NONE): any covering
//     ROA makes them INVALID, otherwise NOT_FOUND.
func (t ROATable) validate(prefix netip.Prefix, origin uint32, originKnown bool) rpkiResult {
	prefix = prefix.Masked()
	var covering []ROA
	for _, r := range t.roas {
		if r.covers(prefix) {
			covering = append(covering, r)
		}
	}

	if len(covering) == 0 {
		return rpkiResult{
			state:  RPKINotFound,
			detail: fmt.Sprintf("no ROA covers %s", prefix),
		}
	}

	if !originKnown {
		return rpkiResult{
			state:  RPKIInvalid,
			detail: fmt.Sprintf("AS_SET route has no determined origin (RFC 6811) and %s is covered by %d ROA(s)", prefix, len(covering)),
		}
	}

	for _, r := range covering {
		// AS 0 is the explicit "no origin AS is authorized" signal (RFC 6483).
		if r.AS == 0 {
			continue
		}
		if r.AS == origin {
			return rpkiResult{
				state:  RPKIValid,
				detail: fmt.Sprintf("ROA authorizes AS %d for %s (maxLen %d)", origin, prefix, r.MaxLen),
			}
		}
	}

	for _, r := range covering {
		if r.AS == 0 {
			return rpkiResult{
				state:  RPKIInvalid,
				detail: fmt.Sprintf("AS 0 ROA explicitly invalidates %s", prefix),
			}
		}
	}
	return rpkiResult{
		state:  RPKIInvalid,
		detail: fmt.Sprintf("origin AS %d is not authorized by any of the %d covering ROA(s) for %s", origin, len(covering), prefix),
	}
}
