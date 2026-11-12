package netsec

import "crypto/sha512"

// preauthHashLen is the fixed size of a SHA-512 digest and of the initial
// all-zero value specified by MS-SMB2 section 3.2.5.3.
const preauthHashLen = 64

// preauthHasher accumulates the SMB 3.1.1 pre-authentication integrity hash.
//
// The initial value H0 is 64 zero bytes. Every full negotiate request and
// negotiate response wire message M is folded in as
//
//	H = SHA-512(H || M)
//
// where M is the exact byte sequence of the message as sent on the wire.
type preauthHasher struct {
	state []byte
}

func newPreauthHasher() *preauthHasher {
	return &preauthHasher{state: make([]byte, preauthHashLen)}
}

// fold applies H = SHA-512(H || message) without retaining the concatenated
// buffer beyond the call.
func (h *preauthHasher) fold(message []byte) {
	buf := make([]byte, 0, len(h.state)+len(message))
	buf = append(buf, h.state...)
	buf = append(buf, message...)
	sum := sha512.Sum512(buf)
	h.state = sum[:]
}

// value returns a copy of the current hash so callers cannot mutate state.
func (h *preauthHasher) value() []byte {
	out := make([]byte, len(h.state))
	copy(out, h.state)
	return out
}
