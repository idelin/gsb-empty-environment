package fieldbinding

// State is the tri-state occupancy of a field in a record. Missing and
// Null are distinct states and are never merged.
type State int

const (
	// Missing means the field (or an ancestor) was absent from the record.
	Missing State = iota
	// Null means the record carried an explicit null for the field.
	Null
	// Present means the record carried a concrete value.
	Present
)

func (s State) String() string {
	switch s {
	case Missing:
		return "missing"
	case Null:
		return "null"
	case Present:
		return "present"
	}
	return "unknown"
}

// Value is a single field value in a record. The zero value is Missing.
// Struct children are keyed by stable field ID.
type Value struct {
	State State
	K     Kind // representation category of a Present value
	I64   int64
	F64   float64
	S     string
	B     bool
	Kids  map[int]Value
}

// Int builds a Present integer value.
func Int(v int64) Value { return Value{State: Present, K: Int64, I64: v} }

// Float builds a Present floating-point value.
func Float(v float64) Value { return Value{State: Present, K: Float64, F64: v} }

// Str builds a Present string value.
func Str(s string) Value { return Value{State: Present, K: String, S: s} }

// Boolean builds a Present boolean value.
func Boolean(v bool) Value { return Value{State: Present, K: Bool, B: v} }

// Record builds a Present struct value from children keyed by field ID.
func Record(kids map[int]Value) Value { return Value{State: Present, K: Struct, Kids: kids} }

// ExplicitNull builds an explicit null value, distinct from Missing.
func ExplicitNull() Value { return Value{State: Null} }

// Child returns the child value for id, or Missing when absent.
func (v Value) Child(id int) Value {
	if v.State != Present {
		return Value{}
	}
	if c, ok := v.Kids[id]; ok {
		return c
	}
	return Value{}
}
