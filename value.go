package cellengine

import (
	"fmt"
	"strconv"
	"strings"
)

// Value is the result of evaluating a cell: either an integer or an error.
type Value struct {
	IsErr bool
	Num   int64
	Err   string // e.g. "#CYCLE!(A1->B1->A1)", "#REF!(Q9)", "#ERR!", "#PARSE!(...)"
}

// NumVal builds an integer value.
func NumVal(n int64) Value { return Value{Num: n} }

func errVal(msg string) Value { return Value{IsErr: true, Err: msg} }

// errRef reports a reference to a cell that does not exist.
func errRef(id string) Value { return errVal("#REF!(" + id + ")") }

// errLiteral is the value of the #ERR! literal.
func errLiteral() Value { return errVal("#ERR!") }

// errParse reports a formula that failed to parse.
func errParse(err error) Value { return errVal("#PARSE!(" + err.Error() + ")") }

// errCycle builds the cycle evidence for a loop. loop holds the cells on the
// evaluation stack from the first occurrence of the repeated cell, without
// repeating the entry cell at the end. The loop is rotated so the smallest
// cell ID comes first, making the evidence deterministic.
func errCycle(loop []string) Value {
	min := 0
	for i := range loop {
		if loop[i] < loop[min] {
			min = i
		}
	}
	rot := append(append([]string{}, loop[min:]...), loop[:min]...)
	rot = append(rot, rot[0])
	return errVal("#CYCLE!(" + strings.Join(rot, "->") + ")")
}

// IsCycleErr reports whether the value is a cycle error.
func (v Value) IsCycleErr() bool { return v.IsErr && strings.HasPrefix(v.Err, "#CYCLE!(") }

func (v Value) String() string {
	if v.IsErr {
		return v.Err
	}
	return strconv.FormatInt(v.Num, 10)
}

// Equal compares two values exactly, including error text.
func (v Value) Equal(o Value) bool {
	if v.IsErr != o.IsErr {
		return false
	}
	if v.IsErr {
		return v.Err == o.Err
	}
	return v.Num == o.Num
}

func (v Value) GoString() string {
	if v.IsErr {
		return fmt.Sprintf("err(%s)", v.Err)
	}
	return fmt.Sprintf("num(%d)", v.Num)
}
