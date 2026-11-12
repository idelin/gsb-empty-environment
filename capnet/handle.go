package capnet

import "fmt"

// Handle is an opaque, unforgeable reference to one simulated endpoint in one
// trial generation.
//
// Handle deliberately has no exported fields and no exported constructor: the
// only valid instances come from Trial.Mint. A value obtained from an address
// string cannot be turned into a Handle, copying a Handle merely copies a
// capability reference, and every field is retained even when zero so the
// layout can never be partially forged.
type Handle struct {
	host *Host
	gen  uint64
	id   uint64
	tok  [tokenLen]byte
}

const tokenLen = 16

// String never reveals the generation, endpoint id, token or real address.
func (h Handle) String() string {
	return "capnet:handle[redacted]"
}

// GoString keeps the same redaction under %#v.
func (h Handle) GoString() string {
	return fmt.Sprintf("capnet.Handle(%q)", h.String())
}

// isZero reports whether the handle is the zero value (no token set).
func (h Handle) isZero() bool {
	var zero [tokenLen]byte
	return h.host == nil && h.gen == 0 && h.id == 0 && h.tok == zero
}
