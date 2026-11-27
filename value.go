// Package minijit implements a small dynamic-language bytecode interpreter
// together with a speculative optimizer that inlines calls and scalar-replaces
// non-escaping objects. When a speculative guard fails the runtime deoptimizes:
// it rebuilds independent interpreter frames from a single physical machine
// frame and materializes virtual (delayed-allocation) objects with a two-phase
// protocol that safely handles shared and circular references.
package minijit

import (
	"fmt"
	"math"
)

// Value tags.
const (
	tagNil = iota
	tagInt
	tagFloat
	tagBool
	tagString
	tagObject
	tagFunction
)

// Value is a tagged dynamic-language value. The zero value is nil.
type Value struct {
	tag uint8
	i   int64
	f   float64
	b   bool
	s   string
	o   *Object
	fn  *Function
}

// IntValue builds an integer tagged value.
func IntValue(v int64) Value { return Value{tag: tagInt, i: v} }

// FloatValue builds a floating point tagged value.
func FloatValue(v float64) Value { return Value{tag: tagFloat, f: v} }

// BoolValue builds a boolean tagged value.
func BoolValue(v bool) Value { return Value{tag: tagBool, b: v} }

// StringValue builds a string tagged value.
func StringValue(v string) Value { return Value{tag: tagString, s: v} }

// ObjectValue wraps a heap object.
func ObjectValue(o *Object) Value { return Value{tag: tagObject, o: o} }

// FunctionValue wraps a function reference.
func FunctionValue(fn *Function) Value { return Value{tag: tagFunction, fn: fn} }

// NilValue returns the nil tagged value.
func NilValue() Value { return Value{} }

// IsNil reports whether v is the nil value.
func (v Value) IsNil() bool { return v.tag == tagNil }

// IsInt reports whether v is an integer.
func (v Value) IsInt() bool { return v.tag == tagInt }

// IsFloat reports whether v is a float.
func (v Value) IsFloat() bool { return v.tag == tagFloat }

// IsBool reports whether v is a boolean.
func (v Value) IsBool() bool { return v.tag == tagBool }

// IsString reports whether v is a string.
func (v Value) IsString() bool { return v.tag == tagString }

// IsObject reports whether v is a heap object reference.
func (v Value) IsObject() bool { return v.tag == tagObject }

// IsFunction reports whether v is a function reference.
func (v Value) IsFunction() bool { return v.tag == tagFunction }

// AsInt returns the integer payload.
func (v Value) AsInt() int64 { return v.i }

// AsFloat returns the float payload.
func (v Value) AsFloat() float64 { return v.f }

// AsBool returns the boolean payload.
func (v Value) AsBool() bool { return v.b }

// AsString returns the string payload.
func (v Value) AsString() string { return v.s }

// AsObject returns the heap object pointer, or nil.
func (v Value) AsObject() *Object { return v.o }

// AsFunction returns the function pointer, or nil.
func (v Value) AsFunction() *Function { return v.fn }

// AsNumber coerces integer and float values to float64.
func (v Value) AsNumber() float64 {
	switch v.tag {
	case tagInt:
		return float64(v.i)
	case tagFloat:
		return v.f
	default:
		return math.NaN()
	}
}

// StrictEqual implements reference identity for objects and structural
// equality for primitives.
func StrictEqual(a, b Value) bool {
	if a.tag != b.tag {
		return false
	}
	switch a.tag {
	case tagNil:
		return true
	case tagInt:
		return a.i == b.i
	case tagFloat:
		return a.f == b.f
	case tagBool:
		return a.b == b.b
	case tagString:
		return a.s == b.s
	case tagObject:
		return a.o == b.o
	case tagFunction:
		return a.fn == b.fn
	default:
		return false
	}
}

// String renders a value for diagnostics.
func (v Value) String() string {
	switch v.tag {
	case tagNil:
		return "nil"
	case tagInt:
		return fmt.Sprintf("%d", v.i)
	case tagFloat:
		return fmt.Sprintf("%g", v.f)
	case tagBool:
		if v.b {
			return "true"
		}
		return "false"
	case tagString:
		return v.s
	case tagObject:
		if v.o == nil {
			return "object(nil)"
		}
		return fmt.Sprintf("object#%d[%s]", v.o.id, v.o.klass)
	case tagFunction:
		if v.fn == nil {
			return "func(nil)"
		}
		return fmt.Sprintf("func#%d[%s]", v.fn.id, v.fn.Name)
	default:
		return "?"
	}
}

var nextObjectID int64

// Object is a heap-allocated instance with a hidden class (map) and ordered
// named fields.
type Object struct {
	id     int64
	klass  string
	fields []Field
}

// Field is a single named object slot.
type Field struct {
	Name  string
	Value Value
}

// NewObject allocates an empty heap object with the given hidden class.
func NewObject(klass string) *Object {
	nextObjectID++
	return &Object{id: nextObjectID, klass: klass}
}

// ID returns the unique runtime object id (diagnostics only; identity is the
// pointer).
func (o *Object) ID() int64 { return o.id }

// Class returns the hidden class name.
func (o *Object) Class() string { return o.klass }

// SetClass changes the hidden class (a map transition).
func (o *Object) SetClass(klass string) { o.klass = klass }

// Fields returns a copy of the object's fields.
func (o *Object) Fields() []Field {
	out := make([]Field, len(o.fields))
	copy(out, o.fields)
	return out
}

// Field looks up a field by name and reports whether it exists.
func (o *Object) Field(name string) (Value, bool) {
	for idx := range o.fields {
		if o.fields[idx].Name == name {
			return o.fields[idx].Value, true
		}
	}
	return NilValue(), false
}

// SetField sets an existing field in place or appends a new one.
func (o *Object) SetField(name string, v Value) {
	for idx := range o.fields {
		if o.fields[idx].Name == name {
			o.fields[idx].Value = v
			return
		}
	}
	o.fields = append(o.fields, Field{Name: name, Value: v})
}
