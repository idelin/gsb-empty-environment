package netsec

// Interval is a half-open [Start, End) machine-address interval.
//
// It describes either a generated code page or the instruction range a
// thread is currently executing inside. The zero value is invalid.
type Interval struct {
	Start uintptr
	End   uintptr
}

// Valid reports whether the interval is well-formed and non-empty.
func (i Interval) Valid() bool {
	return i.End > i.Start
}

// Len returns the byte length of the interval.
func (i Interval) Len() uintptr {
	if !i.Valid() {
		return 0
	}
	return i.End - i.Start
}

// Contains reports whether addr falls inside the half-open interval.
// An address equal to End is not contained.
func (i Interval) Contains(addr uintptr) bool {
	return addr >= i.Start && addr < i.End
}

// Overlaps reports whether two half-open intervals share any address.
// Adjacent intervals ([0,100) and [100,200)) do not overlap.
func (i Interval) Overlaps(other Interval) bool {
	return i.Valid() && other.Valid() && i.Start < other.End && other.Start < i.End
}
