package netsec

import (
	"errors"
	"fmt"
)

// ByteView is the only interface a comparison function may use to inspect
// its inputs. Calls to ByteAt are recorded by the verifier.
type ByteView interface {
	Len() int
	ByteAt(index int) byte
}

// CompareFunc compares two byte views and reports whether their contents are
// equal. A constant-time implementation must use ByteView for all byte
// accesses; it must not recover the underlying slices.
type CompareFunc func(a, b ByteView) bool

// Source identifies which operand supplied a recorded byte.
type Source uint8

const (
	// SourceA is the first operand passed to CompareFunc.
	SourceA Source = iota
	// SourceB is the second operand passed to CompareFunc.
	SourceB
)

func (s Source) String() string {
	switch s {
	case SourceA:
		return "a"
	case SourceB:
		return "b"
	default:
		return fmt.Sprintf("source(%d)", uint8(s))
	}
}

// Access is one byte read performed by a comparison function.
type Access struct {
	Source Source
	Index  int
}

func (a Access) String() string {
	return fmt.Sprintf("%s[%d]", a.Source, a.Index)
}

type recordingView struct {
	source Source
	data   []byte
	trace  *[]Access
}

func (v *recordingView) Len() int {
	return len(v.data)
}

func (v *recordingView) ByteAt(index int) byte {
	*v.trace = append(*v.trace, Access{Source: v.source, Index: index})
	if index < 0 || index >= len(v.data) {
		panic(fmt.Sprintf("%s index %d out of range for length %d", v.source, index, len(v.data)))
	}
	return v.data[index]
}

// Trace runs one comparison and returns its result together with the exact
// sequence of byte accesses it performed. A panic in the comparison is
// returned as an error rather than propagated.
func Trace(compare CompareFunc, a, b []byte) (result bool, accesses []Access, err error) {
	if compare == nil {
		return false, nil, errors.New("nil comparison function")
	}

	deferred := func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("comparison panicked: %v", recovered)
		}
	}
	defer deferred()

	left := &recordingView{source: SourceA, data: a, trace: &accesses}
	right := &recordingView{source: SourceB, data: b, trace: &accesses}
	result = compare(left, right)
	return result, accesses, nil
}
