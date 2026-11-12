package netsec

import "unsafe"

func maxIntValue() uint64 {
	return uint64(^uint(0) >> 1)
}

// AddUint64 is Add for an attacker-supplied unsigned offset. Values that
// cannot be represented safely as a uintptr before validation are reported as
// length overflow.
func (c *Checker) AddUint64(p unsafe.Pointer, offset uint64, target ...Layout) (unsafe.Pointer, error) {
	layout := defaultLayout(target)
	if offset > uint64(^uintptr(0)) {
		return nil, c.report("unsafe.Add", &Violation{
			Kind:    KindLengthOverflow,
			Op:      "unsafe.Add",
			Address: uintptr(p),
			Offset:  uintptr(offset),
			Target:  layout,
			Detail:  "offset does not fit in uintptr",
		})
	}
	return c.Add(p, uintptr(offset), target...)
}

// SliceUint64 is Slice for an attacker-supplied unsigned element count.
// Lengths that cannot be represented as a Go int are rejected before any
// multiplication.
func (c *Checker) SliceUint64(base unsafe.Pointer, length uint64, elem ...Layout) (unsafe.Pointer, error) {
	if length > maxIntValue() {
		return nil, c.report("slice-reinterpret", &Violation{
			Kind:    KindLengthOverflow,
			Op:      "slice-reinterpret",
			Address: uintptr(base),
			Length:  uintptr(length),
			Target:  firstLayout(elem),
			Detail:  "element count does not fit in int",
		})
	}
	return c.Slice(base, int(length), elem...)
}

// SliceAtUint64 is SliceAt for an attacker-supplied unsigned byte offset and
// element count.
func (c *Checker) SliceAtUint64(base unsafe.Pointer, byteOffset, length uint64, elem ...Layout) (unsafe.Pointer, error) {
	if byteOffset > uint64(^uintptr(0)) {
		return nil, c.report("slice-reinterpret", &Violation{
			Kind:    KindLengthOverflow,
			Op:      "slice-reinterpret",
			Address: uintptr(base),
			Offset:  uintptr(byteOffset),
			Length:  uintptr(length),
			Target:  firstLayout(elem),
			Detail:  "byte offset does not fit in uintptr",
		})
	}
	if length > maxIntValue() {
		return nil, c.report("slice-reinterpret", &Violation{
			Kind:    KindLengthOverflow,
			Op:      "slice-reinterpret",
			Address: uintptr(base),
			Offset:  uintptr(byteOffset),
			Length:  uintptr(length),
			Target:  firstLayout(elem),
			Detail:  "element count does not fit in int",
		})
	}
	return c.SliceAt(base, uintptr(byteOffset), int(length), elem...)
}

// CheckAddUint64 validates an unsigned offset and returns only the error.
func (c *Checker) CheckAddUint64(p unsafe.Pointer, offset uint64, target ...Layout) error {
	_, err := c.AddUint64(p, offset, target...)
	return err
}

// CheckSliceUint64 validates an unsigned element count and returns only the
// error.
func (c *Checker) CheckSliceUint64(base unsafe.Pointer, length uint64, elem ...Layout) error {
	_, err := c.SliceUint64(base, length, elem...)
	return err
}

// CheckSliceAtUint64 validates an unsigned offset and element count and
// returns only the error.
func (c *Checker) CheckSliceAtUint64(base unsafe.Pointer, byteOffset, length uint64, elem ...Layout) error {
	_, err := c.SliceAtUint64(base, byteOffset, length, elem...)
	return err
}

func firstLayout(elem []Layout) Layout {
	if len(elem) == 0 {
		return Layout{}
	}
	return elem[0].Normalize()
}

// Package-level unsigned-length helpers use Default.

// AddUint64 is AddUint64 using Default.
func AddUint64(p unsafe.Pointer, offset uint64, target ...Layout) (unsafe.Pointer, error) {
	return Default.AddUint64(p, offset, target...)
}

// SliceUint64 is SliceUint64 using Default.
func SliceUint64(base unsafe.Pointer, length uint64, elem ...Layout) (unsafe.Pointer, error) {
	return Default.SliceUint64(base, length, elem...)
}

// SliceAtUint64 is SliceAtUint64 using Default.
func SliceAtUint64(base unsafe.Pointer, byteOffset, length uint64, elem ...Layout) (unsafe.Pointer, error) {
	return Default.SliceAtUint64(base, byteOffset, length, elem...)
}

// CheckAddUint64 validates an unsigned offset using Default.
func CheckAddUint64(p unsafe.Pointer, offset uint64, target ...Layout) error {
	return Default.CheckAddUint64(p, offset, target...)
}

// CheckSliceUint64 validates an unsigned element count using Default.
func CheckSliceUint64(base unsafe.Pointer, length uint64, elem ...Layout) error {
	return Default.CheckSliceUint64(base, length, elem...)
}

// CheckSliceAtUint64 validates an unsigned offset and element count using
// Default.
func CheckSliceAtUint64(base unsafe.Pointer, byteOffset, length uint64, elem ...Layout) error {
	return Default.CheckSliceAtUint64(base, byteOffset, length, elem...)
}
