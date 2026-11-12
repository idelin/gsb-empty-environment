package shrink

// Value is the assigned value of a present field. Exactly one of Int and Str
// is meaningful, matching the field kind.
type Value struct {
	Int int64
	Str string
}

// Candidate is one fully assigned structured input produced in minimization
// order.
type Candidate struct {
	schema  *Schema
	present []bool
	vals    []Value
	bytes   int
}

// AssertionID returns the identifier of the original failing assertion. It is
// copied verbatim onto every candidate so the test host can correlate replays.
func (c *Candidate) AssertionID() string { return c.schema.assertion }

// SchemaName returns the name of the schema that produced the candidate.
func (c *Candidate) SchemaName() string { return c.schema.name }

// Present reports whether field i is present.
func (c *Candidate) Present(i int) bool {
	if i < 0 || i >= len(c.present) {
		return false
	}
	return c.present[i]
}

// ValueAt returns the value of field i and whether it is present.
func (c *Candidate) ValueAt(i int) (Value, bool) {
	if !c.Present(i) {
		return Value{}, false
	}
	return c.vals[i], true
}

// PresentCount reports how many fields are present.
func (c *Candidate) PresentCount() int {
	count := 0
	for _, ok := range c.present {
		if ok {
			count++
		}
	}
	return count
}

// OptionalPresentCount reports how many optional fields are present. Required
// fields are always present and are excluded.
func (c *Candidate) OptionalPresentCount() int {
	count := 0
	for i, ok := range c.present {
		if ok && c.schema.fields[i].Optional {
			count++
		}
	}
	return count
}

// TotalBytes is the deterministic serialized size used for minimization:
// presence mask plus the encoded cost of every present field.
func (c *Candidate) TotalBytes() int { return c.bytes }

// Snapshot returns independent copies of the presence flags and values in
// field order.
func (c *Candidate) Snapshot() (present []bool, values []Value) {
	present = append([]bool(nil), c.present...)
	values = append([]Value(nil), c.vals...)
	return present, values
}

// val is one enumerated value of a field, pre-sorted by encoded cost.
type val struct {
	integer bool
	i       int64
	s       string
	cost    int
}

func (v val) toValue() Value {
	if v.integer {
		return Value{Int: v.i}
	}
	return Value{Str: v.s}
}

// lessVal orders values by encoded cost and then by content so enumeration is
// deterministic.
func lessVal(a, b val) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.integer != b.integer {
		return a.integer // integers before strings within equal cost
	}
	if a.integer {
		return a.i < b.i
	}
	if len(a.s) != len(b.s) {
		return len(a.s) < len(b.s)
	}
	return a.s < b.s
}
