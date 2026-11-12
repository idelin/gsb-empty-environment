package netsec

import "unsafe"

// ReinterpretSlice validates base as length elements of T and returns the
// resulting slice. It is the checked equivalent of
// unsafe.Slice((*T)(base), length).
func ReinterpretSlice[T any](c *Checker, base unsafe.Pointer, length int) ([]T, error) {
	if c == nil {
		c = Default
	}
	if length < 0 {
		return nil, c.report("slice-reinterpret", &Violation{
			Kind:    KindNegativeLength,
			Op:      "slice-reinterpret",
			Address: uintptr(base),
			Length:  uintptr(length),
			Detail:  "negative slice length",
		})
	}
	if base == nil {
		if length == 0 {
			return nil, nil
		}
		return nil, c.reportNil("slice-reinterpret", LayoutOf[T]())
	}

	ptr, err := c.Slice(base, length, LayoutOf[T]())
	if err != nil {
		return nil, err
	}
	if length == 0 {
		return unsafe.Slice((*T)(ptr), 0), nil
	}
	return unsafe.Slice((*T)(ptr), length), nil
}

// CheckedSlice is an alias for ReinterpretSlice using Default.
func CheckedSlice[T any](base unsafe.Pointer, length int) ([]T, error) {
	return ReinterpretSlice[T](Default, base, length)
}
