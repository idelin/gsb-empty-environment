package graphser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// Safety limits, validated before any graph memory is allocated. They are
// variables so tests can tighten them.
var (
	maxObjects = 1 << 20 // max distinct objects in one document
	maxLen     = 1 << 22 // max fields/elements/entries in one value
	maxDepth   = 512     // max nesting depth of the value tree
)

// Unmarshal decodes a document produced by Marshal into out, which must be
// a non-nil pointer. Decoding runs in four phases:
//
//  1. validate: version, duplicate IDs, dangling refs, duplicate field
//     names, unknown kinds, depth and length limits — all before any graph
//     memory is allocated.
//  2. resolve: walk the document guided by the output type, checking every
//     value and object against the expected Go type and assigning each
//     object ID exactly one expected type (conflicts are rejected).
//  3. allocate: create a zero placeholder per object ID (reflect.New for
//     pointer pointees, MakeMapWithSize for maps).
//  4. fill: populate placeholders and link references, so shared objects
//     keep one identity and cycles connect correctly.
//
// No host code runs during decoding: values are only constructed via
// reflect.New/Set for types reachable from the output type.
func Unmarshal(data []byte, out any) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("graphser: out must be a non-nil pointer")
	}
	var doc Document
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("graphser: invalid document: %v", err)
	}
	if dec.More() {
		return fmt.Errorf("graphser: trailing data after document")
	}
	d := &decoder{
		doc:    &doc,
		objs:   make(map[uint64]*Object, len(doc.Objects)),
		expect: make(map[uint64]expectation),
	}
	if err := d.validate(); err != nil {
		return err
	}
	if err := d.resolve(&doc.Root, rv.Elem().Type(), 0); err != nil {
		return err
	}
	d.allocate()
	if err := d.fill(); err != nil {
		return err
	}
	return d.fillValue(rv.Elem(), &doc.Root)
}

type decoder struct {
	doc    *Document
	objs   map[uint64]*Object
	expect map[uint64]expectation
	ptrs   map[uint64]reflect.Value // placeholder pointers
	maps   map[uint64]reflect.Value // placeholder maps
}

// expectation is the single Go type an object ID must decode as.
type expectation struct {
	asPtr bool         // placeholder is a pointer to typ
	typ   reflect.Type // pointee type if asPtr, else the map type
}

// ---- phase 1: structural validation, before any allocation ----

func (d *decoder) validate() error {
	if d.doc.Version != Version {
		return fmt.Errorf("graphser: unsupported version %d", d.doc.Version)
	}
	if len(d.doc.Objects) > maxObjects {
		return fmt.Errorf("graphser: too many objects: %d exceeds limit %d", len(d.doc.Objects), maxObjects)
	}
	for i := range d.doc.Objects {
		obj := &d.doc.Objects[i]
		if obj.ID == 0 {
			return fmt.Errorf("graphser: object id 0 is reserved")
		}
		if _, dup := d.objs[obj.ID]; dup {
			return fmt.Errorf("graphser: duplicate definition of object %d", obj.ID)
		}
		d.objs[obj.ID] = obj
	}
	done := make(map[uint64]bool, len(d.objs))
	if err := d.check(&d.doc.Root, 0, done); err != nil {
		return err
	}
	for id := range d.objs {
		if !done[id] {
			return fmt.Errorf("graphser: object %d defined but never referenced", id)
		}
	}
	return nil
}

// check walks the value tree verifying kinds, refs, field names, depth and
// lengths. done marks objects already expanded (cycles terminate).
func (d *decoder) check(w *Value, depth int, done map[uint64]bool) error {
	if depth > maxDepth {
		return fmt.Errorf("graphser: document exceeds max depth %d", maxDepth)
	}
	switch w.Kind {
	case KindNull, KindBool, KindInt, KindUint, KindFloat, KindString:
		return nil
	case KindRef:
		obj, ok := d.objs[w.Ref]
		if !ok {
			return fmt.Errorf("graphser: dangling reference to object %d", w.Ref)
		}
		if done[w.Ref] {
			return nil
		}
		done[w.Ref] = true
		return d.check(&obj.Value, depth+1, done)
	case KindStruct:
		if len(w.Fields) > maxLen {
			return fmt.Errorf("graphser: struct field count %d exceeds limit %d", len(w.Fields), maxLen)
		}
		seen := make(map[string]bool, len(w.Fields))
		for i := range w.Fields {
			name := w.Fields[i].Name
			if seen[name] {
				return fmt.Errorf("graphser: duplicate field %q in one struct value", name)
			}
			seen[name] = true
			if err := d.check(&w.Fields[i].Value, depth+1, done); err != nil {
				return err
			}
		}
		return nil
	case KindSlice, KindArray:
		if len(w.Elems) > maxLen {
			return fmt.Errorf("graphser: sequence length %d exceeds limit %d", len(w.Elems), maxLen)
		}
		for i := range w.Elems {
			if err := d.check(&w.Elems[i], depth+1, done); err != nil {
				return err
			}
		}
		return nil
	case KindMap:
		if len(w.Entries) > maxLen {
			return fmt.Errorf("graphser: map entry count %d exceeds limit %d", len(w.Entries), maxLen)
		}
		for i := range w.Entries {
			if err := d.check(&w.Entries[i].Key, depth+1, done); err != nil {
				return err
			}
			if err := d.check(&w.Entries[i].Val, depth+1, done); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("graphser: unknown value kind %q", w.Kind)
}

// ---- phase 2: type resolution against the output type ----

func (d *decoder) resolve(w *Value, t reflect.Type, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("graphser: document exceeds max depth %d", maxDepth)
	}
	switch w.Kind {
	case KindNull:
		switch t.Kind() {
		case reflect.Ptr, reflect.Map, reflect.Slice:
			return nil
		}
		return fmt.Errorf("graphser: null cannot fill %s", t)
	case KindBool:
		return expectKind(t, reflect.Bool)
	case KindInt:
		return expectKind(t, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64)
	case KindUint:
		return expectKind(t, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr)
	case KindFloat:
		return expectKind(t, reflect.Float32, reflect.Float64)
	case KindString:
		return expectKind(t, reflect.String)
	case KindStruct:
		if t.Kind() != reflect.Struct {
			return fmt.Errorf("graphser: struct value cannot fill %s", t)
		}
		if w.Type != t.String() {
			return fmt.Errorf("graphser: type mismatch: document has %s, want %s", w.Type, t)
		}
		for i := range w.Fields {
			idx, ok := findField(t, w.Fields[i].Name)
			if !ok {
				return fmt.Errorf("graphser: unknown field %q for %s", w.Fields[i].Name, t)
			}
			if err := d.resolve(&w.Fields[i].Value, t.Field(idx).Type, depth+1); err != nil {
				return err
			}
		}
		return nil
	case KindSlice:
		if t.Kind() != reflect.Slice {
			return fmt.Errorf("graphser: slice value cannot fill %s", t)
		}
		if w.Type != t.String() {
			return fmt.Errorf("graphser: type mismatch: document has %s, want %s", w.Type, t)
		}
		for i := range w.Elems {
			if err := d.resolve(&w.Elems[i], t.Elem(), depth+1); err != nil {
				return err
			}
		}
		return nil
	case KindArray:
		if t.Kind() != reflect.Array {
			return fmt.Errorf("graphser: array value cannot fill %s", t)
		}
		if w.Type != t.String() {
			return fmt.Errorf("graphser: type mismatch: document has %s, want %s", w.Type, t)
		}
		if len(w.Elems) != t.Len() {
			return fmt.Errorf("graphser: array length mismatch: document has %d, %s needs %d", len(w.Elems), t, t.Len())
		}
		for i := range w.Elems {
			if err := d.resolve(&w.Elems[i], t.Elem(), depth+1); err != nil {
				return err
			}
		}
		return nil
	case KindMap:
		if t.Kind() != reflect.Map {
			return fmt.Errorf("graphser: map value cannot fill %s", t)
		}
		if w.Type != t.String() {
			return fmt.Errorf("graphser: type mismatch: document has %s, want %s", w.Type, t)
		}
		for i := range w.Entries {
			if err := d.resolve(&w.Entries[i].Key, t.Key(), depth+1); err != nil {
				return err
			}
			if err := d.resolve(&w.Entries[i].Val, t.Elem(), depth+1); err != nil {
				return err
			}
		}
		return nil
	case KindRef:
		obj := d.objs[w.Ref] // existence already validated
		var exp expectation
		switch t.Kind() {
		case reflect.Ptr:
			exp = expectation{asPtr: true, typ: t.Elem()}
		case reflect.Map:
			exp = expectation{asPtr: false, typ: t}
		default:
			return fmt.Errorf("graphser: reference cannot fill %s", t)
		}
		if obj.Type != exp.typ.String() {
			return fmt.Errorf("graphser: object %d has type %s, want %s", w.Ref, obj.Type, exp.typ)
		}
		if !exp.asPtr && obj.Value.Kind != KindMap {
			return fmt.Errorf("graphser: object %d holds a %s value, not a map", w.Ref, obj.Value.Kind)
		}
		if prev, ok := d.expect[w.Ref]; ok {
			if prev.asPtr != exp.asPtr || prev.typ != exp.typ {
				return fmt.Errorf("graphser: conflicting types for object %d: %s vs %s", w.Ref, typeName(prev), typeName(exp))
			}
			return nil
		}
		d.expect[w.Ref] = exp
		return d.resolve(&obj.Value, exp.typ, depth+1)
	}
	return fmt.Errorf("graphser: unknown value kind %q", w.Kind)
}

func typeName(e expectation) string {
	if e.asPtr {
		return "*" + e.typ.String()
	}
	return e.typ.String()
}

func expectKind(t reflect.Type, kinds ...reflect.Kind) error {
	for _, k := range kinds {
		if t.Kind() == k {
			return nil
		}
	}
	return fmt.Errorf("graphser: scalar cannot fill %s", t)
}

// findField returns the index of the exported struct field with the given
// name.
func findField(t reflect.Type, name string) (int, bool) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath == "" && sf.Name == name {
			return i, true
		}
	}
	return 0, false
}

// ---- phase 3: allocate placeholders (lengths already validated) ----

func (d *decoder) allocate() {
	d.ptrs = make(map[uint64]reflect.Value, len(d.expect))
	d.maps = make(map[uint64]reflect.Value)
	for id, exp := range d.expect {
		if exp.asPtr {
			d.ptrs[id] = reflect.New(exp.typ)
		} else {
			d.maps[id] = reflect.MakeMapWithSize(exp.typ, len(d.objs[id].Value.Entries))
		}
	}
}

// ---- phase 4: fill placeholders and link references ----

func (d *decoder) fill() error {
	for id, ptr := range d.ptrs {
		if err := d.fillValue(ptr.Elem(), &d.objs[id].Value); err != nil {
			return err
		}
	}
	for id, m := range d.maps {
		obj := d.objs[id]
		for i := range obj.Value.Entries {
			kv := reflect.New(m.Type().Key()).Elem()
			if err := d.fillValue(kv, &obj.Value.Entries[i].Key); err != nil {
				return err
			}
			vv := reflect.New(m.Type().Elem()).Elem()
			if err := d.fillValue(vv, &obj.Value.Entries[i].Val); err != nil {
				return err
			}
			m.SetMapIndex(kv, vv)
		}
	}
	return nil
}

func (d *decoder) fillValue(dst reflect.Value, w *Value) error {
	switch w.Kind {
	case KindNull:
		// leave zero value
	case KindBool:
		dst.SetBool(w.Bool)
	case KindInt:
		if dst.OverflowInt(w.Int) {
			return fmt.Errorf("graphser: %d overflows %s", w.Int, dst.Type())
		}
		dst.SetInt(w.Int)
	case KindUint:
		if dst.OverflowUint(w.Uint) {
			return fmt.Errorf("graphser: %d overflows %s", w.Uint, dst.Type())
		}
		dst.SetUint(w.Uint)
	case KindFloat:
		if dst.OverflowFloat(w.Float) {
			return fmt.Errorf("graphser: %v overflows %s", w.Float, dst.Type())
		}
		dst.SetFloat(w.Float)
	case KindString:
		dst.SetString(w.Str)
	case KindStruct:
		t := dst.Type()
		for i := range w.Fields {
			idx, _ := findField(t, w.Fields[i].Name) // verified in resolve
			if err := d.fillValue(dst.Field(idx), &w.Fields[i].Value); err != nil {
				return err
			}
		}
	case KindSlice:
		n := len(w.Elems) // validated against maxLen in phase 1
		s := reflect.MakeSlice(dst.Type(), n, n)
		for i := range w.Elems {
			if err := d.fillValue(s.Index(i), &w.Elems[i]); err != nil {
				return err
			}
		}
		dst.Set(s)
	case KindArray:
		for i := range w.Elems {
			if err := d.fillValue(dst.Index(i), &w.Elems[i]); err != nil {
				return err
			}
		}
	case KindMap:
		m := reflect.MakeMapWithSize(dst.Type(), len(w.Entries))
		for i := range w.Entries {
			kv := reflect.New(dst.Type().Key()).Elem()
			if err := d.fillValue(kv, &w.Entries[i].Key); err != nil {
				return err
			}
			vv := reflect.New(dst.Type().Elem()).Elem()
			if err := d.fillValue(vv, &w.Entries[i].Val); err != nil {
				return err
			}
			m.SetMapIndex(kv, vv)
		}
		dst.Set(m)
	case KindRef:
		if dst.Kind() == reflect.Ptr {
			dst.Set(d.ptrs[w.Ref])
		} else {
			dst.Set(d.maps[w.Ref])
		}
	}
	return nil
}
