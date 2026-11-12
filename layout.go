package netsec

import "unsafe"

// Layout describes the memory footprint and alignment required by a Go type.
//
// Size is the number of bytes occupied by one value of the type. Align is the
// address alignment required for a pointer to that type.
type Layout struct {
	Size  uintptr
	Align uintptr
}

// LayoutOf returns the size and alignment of T.
//
// For example, LayoutOf[uint32]() is {Size: 4, Align: 4}.
func LayoutOf[T any]() Layout {
	var zero T
	return Layout{
		Size:  unsafe.Sizeof(zero),
		Align: unsafe.Alignof(zero),
	}
}

// TypeLayout is an alias for LayoutOf.
func TypeLayout[T any]() Layout {
	return LayoutOf[T]()
}

// Normalize replaces a zero alignment with Go's minimum alignment of one.
// Other invalid alignments are left untouched so Valid can report them.
func (l Layout) Normalize() Layout {
	if l.Align == 0 {
		l.Align = 1
	}
	return l
}

// Valid reports whether l can describe a Go type layout. Go alignments are
// always a power of two, and a non-zero type size is a multiple of its
// alignment (so every element in an array is correctly aligned).
func (l Layout) Valid() bool {
	align := l.Align
	if align == 0 {
		align = 1
	}
	return align&(align-1) == 0 && l.Size%align == 0
}

// End returns base+size, reporting whether the addition overflowed uintptr.
func (l Layout) End(base uintptr) (uintptr, bool) {
	size := l.Size
	if size > ^uintptr(0)-base {
		return 0, true
	}
	return base + size, false
}
