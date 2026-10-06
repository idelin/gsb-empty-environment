package infer

import "fmt"

// Counts records how much evidence of each class a column has seen.
// It is the state that makes undo possible: removing a record
// decrements the same counters that adding it incremented.
type Counts struct {
	Bool     int64 // values "true" or "false"
	Int      int64 // canonical integers fitting int64
	Rational int64 // other finite float64-representable numbers
	Text     int64 // everything else, including empty strings by default
	Null     int64 // empty strings, only when WithEmptyAsNull is set
}

// Total returns the number of non-null values seen.
func (c Counts) Total() int64 {
	return c.Bool + c.Int + c.Rational + c.Text
}

// Column accumulates per-type evidence for one column of records.
type Column struct {
	name        string
	emptyAsNull bool
	counts      Counts
}

func newColumn(name string, emptyAsNull bool) *Column {
	return &Column{name: name, emptyAsNull: emptyAsNull}
}

// Name returns the column name.
func (c *Column) Name() string { return c.name }

// Counts returns a snapshot of the per-type evidence counts.
func (c *Column) Counts() Counts { return c.counts }

// Add records one observed value.
func (c *Column) Add(v string) {
	c.counts = c.counts.add(c.classOf(v), 1)
}

// Remove undoes a previous Add of the same value. It returns an error
// if the value's evidence class has no remaining count, i.e. the value
// was never added or was already removed.
func (c *Column) Remove(v string) error {
	k := c.classOf(v)
	if c.counts.of(k) == 0 {
		return fmt.Errorf("infer: column %q: cannot remove %q: no matching evidence", c.name, v)
	}
	c.counts = c.counts.add(k, -1)
	return nil
}

// Type returns the narrowest type that losslessly represents every
// non-null value seen so far. A column with no evidence (empty, or all
// nulls) falls back to TypeText, the safe default.
func (c *Column) Type() Type {
	switch {
	case c.counts.Text > 0:
		return TypeText
	case c.counts.Rational > 0:
		return TypeRational
	case c.counts.Int > 0:
		return TypeInt
	case c.counts.Bool > 0:
		return TypeBool
	default:
		return TypeText
	}
}

func (c *Column) classOf(v string) kind {
	k := classify(v)
	if k == kindEmpty && !c.emptyAsNull {
		// Default: an empty string is text, not a null.
		return kindText
	}
	return k
}

func (c Counts) of(k kind) int64 {
	switch k {
	case kindBool:
		return c.Bool
	case kindInt:
		return c.Int
	case kindRational:
		return c.Rational
	case kindText:
		return c.Text
	default: // kindEmpty with emptyAsNull
		return c.Null
	}
}

func (c Counts) add(k kind, delta int64) Counts {
	switch k {
	case kindBool:
		c.Bool += delta
	case kindInt:
		c.Int += delta
	case kindRational:
		c.Rational += delta
	case kindText:
		c.Text += delta
	default: // kindEmpty with emptyAsNull
		c.Null += delta
	}
	return c
}
