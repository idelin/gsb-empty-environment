package netsec

import "fmt"

// Perm is a bitmask of access permissions granted by an unveil entry,
// mirroring the r/w/x/c letters of OpenBSD unveil(2).
type Perm uint8

const (
	PermRead   Perm = 1 << iota // 'r': read path
	PermWrite                   // 'w': write path
	PermExec                    // 'x': execute path
	PermCreate                  // 'c': create/remove path
)

// ParsePerm converts a permission string such as "rw" into a Perm.
// Each of the letters 'r', 'w', 'x', 'c' may appear at most once.
func ParsePerm(s string) (Perm, error) {
	var p Perm
	for _, r := range s {
		switch r {
		case 'r':
			p |= PermRead
		case 'w':
			p |= PermWrite
		case 'x':
			p |= PermExec
		case 'c':
			p |= PermCreate
		default:
			return 0, fmt.Errorf("netsec: invalid permission letter %q", r)
		}
	}
	return p, nil
}

// String renders the permission in canonical "rwxc" order.
func (p Perm) String() string {
	var b [4]byte
	n := 0
	if p&PermRead != 0 {
		b[n] = 'r'
		n++
	}
	if p&PermWrite != 0 {
		b[n] = 'w'
		n++
	}
	if p&PermExec != 0 {
		b[n] = 'x'
		n++
	}
	if p&PermCreate != 0 {
		b[n] = 'c'
		n++
	}
	return string(b[:n])
}
