package ptrauth

import "strconv"

// Kind is the authenticated access-kind bitmask. Rights are additive but
// write and execute are never held together (W^X).
type Kind uint64

const (
	// KindRead authorizes reading through the pointer.
	KindRead Kind = 1 << iota
	// KindWrite authorizes writing through the pointer.
	KindWrite
	// KindExecute authorizes invoking the pointer's target as code.
	KindExecute
)

// KindReadOnly is the read-only data pointer kind.
const KindReadOnly Kind = KindRead

// KindReadWrite is the writable data pointer kind. Writable pointers keep
// read rights so the span can be validated before a store.
const KindReadWrite Kind = KindRead | KindWrite

// KindCode is the executable pointer kind. It carries read rights so code
// metadata may be inspected, but never write rights.
const KindCode Kind = KindRead | KindExecute

// normalize validates the structural invariants of a kind and returns the
// canonical representation. An empty kind or simultaneous write+execute
// rights are rejected.
func (k Kind) normalize() (Kind, error) {
	if k == 0 {
		return 0, ErrZeroKind
	}
	if k&KindWrite != 0 && k&KindExecute != 0 {
		return 0, ErrWXConflict
	}
	return k, nil
}

// permits reports whether every right in required is held by k.
func (k Kind) permits(required Kind) bool {
	return k&required == required
}

// IsNarrowing reports whether child is equal to or strictly weaker than
// parent: a derived pointer may only preserve or remove rights, never gain
// new ones.
func IsNarrowing(parent, child Kind) bool {
	if child == 0 {
		return false
	}
	return parent.permits(child)
}

// String renders a kind for diagnostics.
func (k Kind) String() string {
	if normalized, err := k.normalize(); err == nil {
		k = normalized
	}
	switch k {
	case KindReadOnly:
		return "read-only"
	case KindReadWrite:
		return "read-write"
	case KindCode:
		return "execute"
	}
	return "kind(0x" + strconv.FormatUint(uint64(k), 16) + ")"
}
