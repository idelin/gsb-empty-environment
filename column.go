package infer

import "fmt"

// Column tracks the values added to a single column and infers the
// narrowest Kind that can losslessly represent all of them.
//
// It stores one evidence count per Kind: how many of the live values
// require at least that Kind. The inferred Kind is the widest Kind
// with a non-zero count, so removing values (Remove) can narrow the
// inferred type again. A column with no values defaults to Bool, the
// narrowest Kind, which vacuously represents the empty set of values.
type Column struct {
	counts [4]int // live value count per Kind, indexed by Kind
	total  int
}

// NewColumn returns an empty Column.
func NewColumn() *Column {
	return &Column{}
}

// Add records one value in the column.
func (c *Column) Add(value string) {
	c.counts[Classify(value)]++
	c.total++
}

// Remove retracts one previously added value. It returns an error if
// the value cannot be present, i.e. no live value of its Kind remains.
func (c *Column) Remove(value string) error {
	k := Classify(value)
	if c.counts[k] == 0 {
		return fmt.Errorf("infer: cannot remove %q: no live %s value in column", value, k)
	}
	c.counts[k]--
	c.total--
	return nil
}

// Kind returns the narrowest Kind representing all live values.
func (c *Column) Kind() Kind {
	for k := Text; k > Bool; k-- {
		if c.counts[k] > 0 {
			return k
		}
	}
	return Bool
}

// Len returns the number of live values in the column.
func (c *Column) Len() int {
	return c.total
}

// Count returns the number of live values whose narrowest Kind is k.
func (c *Column) Count(k Kind) int {
	return c.counts[k]
}

// Counts returns a snapshot of the per-Kind evidence counts.
func (c *Column) Counts() map[Kind]int {
	return map[Kind]int{
		Bool:     c.counts[Bool],
		Int:      c.counts[Int],
		Rational: c.counts[Rational],
		Text:     c.counts[Text],
	}
}
