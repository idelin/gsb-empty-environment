// Package bufauth implements authorization forwarding for graphics buffer
// file descriptors that travel between processes over Unix domain sockets.
//
// Every forward produces a derived grant whose permissions can never exceed
// the upstream grant. Revocation cascades down the derived grant tree and
// reclaims every pending transfer, including buffers whose file descriptors
// have already been written into a socket but not yet claimed by the
// receiver.
package bufauth

// Perm is a bit mask of access rights on a graphics buffer.
type Perm uint32

const (
	// PermRead allows reading buffer contents.
	PermRead Perm = 1 << iota
	// PermWrite allows modifying buffer contents.
	PermWrite
	// PermReexport allows forwarding the grant to another receiver.
	PermReexport
	// PermMap allows mapping the buffer into the receiver address space.
	PermMap
)

// PermNone is the empty permission set.
const PermNone Perm = 0

// Contains reports whether p contains every permission in want.
func (p Perm) Contains(want Perm) bool {
	return p&want == want
}

// IsSubsetOf reports whether every permission in p is held by other.
func (p Perm) IsSubsetOf(other Perm) bool {
	return p&^other == 0
}

// String returns a fixed, dependency free rendering of the permission bits.
func (p Perm) String() string {
	if p == PermNone {
		return "none"
	}
	var buf [4]byte
	n := 0
	if p.Contains(PermRead) {
		buf[n] = 'r'
		n++
	}
	if p.Contains(PermWrite) {
		buf[n] = 'w'
		n++
	}
	if p.Contains(PermReexport) {
		buf[n] = 'x'
		n++
	}
	if p.Contains(PermMap) {
		buf[n] = 'm'
		n++
	}
	return string(buf[:n])
}
