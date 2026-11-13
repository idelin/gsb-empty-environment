package netsec

import "fmt"

// Memory geometry. The model is a fixed 64-word, 32-bit address space.
const (
	WordCount   = 64
	WordBits    = 32
	HeaderAlign = 4

	PrevField = 0
	NextField = 1
)

// Memory is the complete address space used by the unlink oracle.
type Memory [WordCount]uint32

// Clone returns an independent copy of m.
func (m Memory) Clone() Memory {
	return m
}

// Load reads one in-range word.
func (m Memory) Load(addr uint32) (uint32, bool) {
	if addr >= WordCount {
		return 0, false
	}
	return m[addr], true
}

// Store writes one in-range word and reports whether the address was valid.
func (m *Memory) Store(addr uint32, value uint32) bool {
	if m == nil || addr >= WordCount {
		return false
	}
	m[addr] = value
	return true
}

// ValueSet is an explicitly supplied set of attacker-chosen 32-bit values.
// Duplicates and order do not affect membership; search sorts values before
// enumerating pairs so that lexicographic order is well defined.
type ValueSet []uint32

// Contains reports whether v is in the set.
func (s ValueSet) Contains(v uint32) bool {
	for _, candidate := range s {
		if candidate == v {
			return true
		}
	}
	return false
}

// HeaderSet is a set of block-header addresses.
type HeaderSet []uint32

// Contains reports whether a block header is in the set.
func (s HeaderSet) Contains(v uint32) bool {
	for _, candidate := range s {
		if candidate == v {
			return true
		}
	}
	return false
}

// WordSet is a set of word addresses.
type WordSet []uint32

// Contains reports whether a word address is in the set.
func (s WordSet) Contains(v uint32) bool {
	for _, candidate := range s {
		if candidate == v {
			return true
		}
	}
	return false
}

// AccessKind distinguishes the two observable memory operations.
type AccessKind uint8

const (
	Read AccessKind = iota + 1
	Write
)

// String returns "read" or "write".
func (k AccessKind) String() string {
	switch k {
	case Read:
		return "read"
	case Write:
		return "write"
	default:
		return fmt.Sprintf("AccessKind(%d)", uint8(k))
	}
}

// Access is one read or write in the exact order executed by an unlink.
type Access struct {
	Kind  AccessKind
	Addr  uint32
	Value uint32
}

// String renders the address and value of an access, for example
// "read m[8] = 0".
func (a Access) String() string {
	return fmt.Sprintf("%s m[%d] = %d", a.Kind, a.Addr, a.Value)
}

// Version selects the unlink precheck policy.
type Version uint8

const (
	// VersionA only requires every read and write address to be in range.
	VersionA Version = iota + 1
	// VersionB additionally requires both backlinks to point at the block
	// being removed.
	VersionB
	// VersionC additionally requires both neighbours to be registered free
	// block headers.
	VersionC
)

// String returns "A", "B", or "C".
func (v Version) String() string {
	switch v {
	case VersionA:
		return "A"
	case VersionB:
		return "B"
	case VersionC:
		return "C"
	default:
		return fmt.Sprintf("Version(%d)", uint8(v))
	}
}

// Valid reports whether v is one of the supported versions.
func (v Version) Valid() bool {
	return v == VersionA || v == VersionB || v == VersionC
}
