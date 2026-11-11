package pdfactions

import "fmt"

// ObjectID identifies an indirect object by number and generation.
type ObjectID struct {
	Number     int
	Generation int
}

func (id ObjectID) String() string {
	return fmt.Sprintf("%d %d R", id.Number, id.Generation)
}

// Value is any value that can appear inside a PDF object: dictionaries,
// arrays, references, names, strings, numbers, booleans or null.
type Value interface {
	pdfValue()
}

// Dict is an ordered PDF dictionary. The parser preserves insertion order so
// deterministic tests can reason about the raw document.
type Dict struct {
	Keys   []Name
	values map[Name]Value
}

// NewDict creates a dictionary holding the alternating key/value pairs.
func NewDict(pairs ...Value) *Dict {
	dict := &Dict{values: make(map[Name]Value)}
	for i := 0; i+1 < len(pairs); i += 2 {
		key, ok := pairs[i].(Name)
		if !ok {
			continue
		}
		dict.Set(key, pairs[i+1])
	}
	return dict
}

// Get returns the value stored under key and whether it was present.
func (d *Dict) Get(key Name) (Value, bool) {
	if d == nil || d.values == nil {
		return nil, false
	}
	value, ok := d.values[key]
	return value, ok
}

// Set stores value under key, keeping key order stable.
func (d *Dict) Set(key Name, value Value) {
	if d.values == nil {
		d.values = make(map[Name]Value)
	}
	if _, ok := d.values[key]; !ok {
		d.Keys = append(d.Keys, key)
	}
	d.values[key] = value
}

// Delete removes key from the dictionary.
func (d *Dict) Delete(key Name) {
	if d.values == nil {
		return
	}
	delete(d.values, key)
	for i, existing := range d.Keys {
		if existing == key {
			d.Keys = append(d.Keys[:i], d.Keys[i+1:]...)
			return
		}
	}
}

func (d *Dict) pdfValue() {}

// Array is a PDF array.
type Array []Value

func (Array) pdfValue() {}

// Name is a PDF name without the leading slash.
type Name string

func (Name) pdfValue() {}

// Ref is an indirect reference (N G R).
type Ref ObjectID

func (r Ref) ID() ObjectID { return ObjectID(r) }
func (Ref) pdfValue()      {}

// String is a PDF literal or hexadecimal string, already decoded.
type String string

func (String) pdfValue() {}

// Stream is an indirect stream object. The raw (still encoded) bytes are kept;
// action resolution never needs to decode a stream.
type Stream struct {
	Dict *Dict
	Raw  []byte
}

func (*Stream) pdfValue() {}

// Integer is an integral numeric value.
type Integer int64

func (Integer) pdfValue() {}

// Real is a floating point numeric value.
type Real float64

func (Real) pdfValue() {}

// Boolean is a PDF boolean.
type Boolean bool

func (Boolean) pdfValue() {}

// Null is the PDF null value.
type Null struct{}

func (Null) pdfValue() {}

// asDict dereferences value using rev when it is an indirect reference and
// reports whether the final value is a dictionary (or stream dictionary).
func asDict(rev *Revision, value Value, seen map[ObjectID]bool) (*Dict, bool) {
	resolved, ok := rev.dereference(value, seen)
	if !ok {
		return nil, false
	}
	switch typed := resolved.(type) {
	case *Dict:
		return typed, true
	case *Stream:
		return typed.Dict, true
	default:
		return nil, false
	}
}

// asArray dereferences value and reports whether it is an array.
func asArray(rev *Revision, value Value, seen map[ObjectID]bool) (Array, bool) {
	resolved, ok := rev.dereference(value, seen)
	if !ok {
		return nil, false
	}
	array, ok := resolved.(Array)
	return array, ok
}

// asName dereferences value and reports whether it is a name.
func asName(rev *Revision, value Value, seen map[ObjectID]bool) (Name, bool) {
	resolved, ok := rev.dereference(value, seen)
	if !ok {
		return "", false
	}
	name, ok := resolved.(Name)
	return name, ok
}
